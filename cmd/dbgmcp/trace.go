package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/backend/dap"
	"github.com/didenkolab/dbgmcp/internal/backend/delve"
	"github.com/didenkolab/dbgmcp/internal/findings"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// traceCommand runs a trace with no agent in the loop and writes a report.
//
// This is the difference between a tool an agent can use and a tool a pipeline
// can use. A failing test in CI has no conversation to hold: something has to
// re-run it under the debugger, record the values, read the transcript and leave
// an artifact a human or a reviewer can open. That is all this does.
func traceCommand(args []string) int {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		language = fs.String("lang", "go", "runtime of the target: go or python")
		dir      = fs.String("dir", ".", "directory to run in")
		mode     = fs.String("mode", "test", "test, debug or exec")
		target   = fs.String("target", ".", "package for test/debug, or binary for exec")
		testRun  = fs.String("test", "", "test filter, for -mode test")
		timeout  = fs.Duration("timeout", 2*time.Minute, "how long to let the target run")
		format   = fs.String("format", "md", "md or json")
		out      = fs.String("out", "-", "file to write, or - for stdout")
		maxHits  = fs.Int("max-hits", 0, "stop each probe after this many hits (0 = until the target ends)")
		tags     = fs.String("tags", "", "build tags the target needs, space or comma separated")
		buildFlg = fs.String("build-flags", "", "anything else the build needs, passed through verbatim")
		shuffle  = fs.Bool("shuffle", false, "leave test-order randomisation on (off by default, so a named test stays where the probe expects it)")
	)
	var probeFlags stringList
	fs.Var(&probeFlags, "probe", "where and what to record: file:line=expr,expr or symbol=expr,expr (repeatable)")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, traceUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(probeFlags) == 0 {
		fmt.Fprintln(os.Stderr, "dbgmcp trace: at least one -probe is required")
		fs.Usage()
		return 2
	}

	workDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
		return 1
	}
	probes, err := parseProbes(probeFlags, workDir, *maxHits)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
		return 2
	}

	b, err := backendFor(*language)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+2*time.Minute)
	defer cancel()
	defer b.Stop(context.Background())

	req := model.LaunchRequest{
		Mode: model.LaunchMode(*mode), Target: *target, WorkDir: workDir, TestRun: *testRun,
		BuildTags: splitTags(*tags), BuildFlags: *buildFlg, Deterministic: !*shuffle,
	}
	if err := b.Launch(ctx, req); err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
		return 1
	}

	transcript, err := b.Trace(ctx, probes, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
		return 1
	}
	transcript.Findings = findings.Analyse(transcript, probes)

	var rendered string
	switch *format {
	case "json":
		encoded, err := json.MarshalIndent(transcript, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
			return 1
		}
		rendered = string(encoded) + "\n"
	case "md":
		rendered = renderMarkdown(req, probes, transcript)
	default:
		fmt.Fprintf(os.Stderr, "dbgmcp trace: unknown format %q (md or json)\n", *format)
		return 2
	}

	if *out == "-" {
		fmt.Print(rendered)
	} else if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp trace:", err)
		return 1
	}
	// Exit zero even when findings exist. The build is failed by the failing
	// test, not by this: a diagnostic that can break a pipeline stops being run.
	return 0
}

const traceUsage = `Record expressions while a target runs, and write a report.

Meant for a pipeline rather than a conversation: re-run a failing test under the
debugger, record what you want to see, and leave an artifact.

  dbgmcp trace -dir . -mode test -target ./internal/billing \
    -test TestSubtotal \
    -probe internal/billing/cart.go:26=total,i

A suite kept behind build tags needs them named, or it cannot be built at all:

  dbgmcp trace -tags "integration devsecrets" -test TestCharge \\
    -probe internal/billing/charge.go:88=amount

Options:
`

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseProbes reads "file:line=exprs" or "symbol=exprs".
func parseProbes(specs []string, workDir string, maxHits int) ([]model.Probe, error) {
	out := make([]model.Probe, 0, len(specs))
	for _, spec := range specs {
		where, exprs, found := strings.Cut(spec, "=")
		if !found || strings.TrimSpace(exprs) == "" {
			return nil, fmt.Errorf("probe %q has no expressions; write file:line=expr or symbol=expr", spec)
		}
		var record []string
		for _, e := range strings.Split(exprs, ",") {
			if e = strings.TrimSpace(e); e != "" {
				record = append(record, e)
			}
		}

		probe := model.Probe{Record: record, MaxHits: maxHits}
		// A trailing ":<number>" makes it a file and line; anything else is a
		// symbol. Package-qualified symbols contain dots, not colons, so the two
		// do not collide.
		if file, lineText, hasLine := strings.Cut(where, ":"); hasLine {
			line, err := strconv.Atoi(lineText)
			if err != nil {
				return nil, fmt.Errorf("probe %q: %q is not a line number", spec, lineText)
			}
			if !filepath.IsAbs(file) {
				file = filepath.Join(workDir, file)
			}
			probe.Location = model.Location{File: file, Line: line}
		} else {
			probe.Location = model.Location{Symbol: where}
		}
		out = append(out, probe)
	}
	return out, nil
}

// splitTags accepts either separator, because a project writes its tags both
// ways and neither spelling should be the one that fails.
func splitTags(raw string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func backendFor(language string) (backend.Backend, error) {
	switch strings.ToLower(language) {
	case "", "go", "golang":
		return delve.New(), nil
	default:
		return dap.New(language)
	}
}
