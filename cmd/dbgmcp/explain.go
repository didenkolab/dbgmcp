package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// explainCommand follows one value and writes its history.
//
// The agent has had this since the beginning; a pipeline has not, and the gap
// showed: this server advertised three questions it answers in one call and let a
// script reach only two of them.
func explainCommand(args []string) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		language  = fs.String("lang", "go", "runtime of the target: go, python or node")
		dir       = fs.String("dir", ".", "directory to run in")
		mode      = fs.String("mode", "test", "test, debug or exec")
		target    = fs.String("target", ".", "package for test/debug, or binary for exec")
		testRun   = fs.String("test", "", "test filter, for -mode test")
		expr      = fs.String("expr", "", "the value to follow, written as it appears in the scope")
		scope     = fs.String("scope", "", "where the value lives: symbol, or file:line")
		when      = fs.String("when", "", "which call to follow, for a function called many times, e.g. 'order.ID == 42'")
		maxWrites = fs.Int("max-writes", 0, "stop after this many changes (0 = until the frame returns)")
		timeout   = fs.Duration("timeout", 2*time.Minute, "give up after this long")
		format    = fs.String("format", "md", "md or json")
		out       = fs.String("out", "-", "file to write, or - for stdout")
		tags      = fs.String("tags", "", "build tags the target needs, space or comma separated")
		buildFlg  = fs.String("build-flags", "", "anything else the build needs, passed through verbatim")
		shuffle   = fs.Bool("shuffle", false, "leave test-order randomisation on (off by default, so a named test stays where the scope expects it)")
	)

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, explainUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *expr == "" || *scope == "" {
		fmt.Fprintln(os.Stderr, "dbgmcp explain: both -expr and -scope are required")
		fs.Usage()
		return 2
	}
	if model.LaunchMode(*mode).IsAttach() {
		fmt.Fprintln(os.Stderr, "dbgmcp explain: mode=attach is not available here -- a value's history starts when its frame does, and an attached process is already past that. Use `dbgmcp trace` on the write sites instead.")
		return 2
	}

	workDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp explain:", err)
		return 1
	}
	location, err := parseLocation(*scope, workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbgmcp explain: -scope %q: %v\n", *scope, err)
		return 2
	}

	b, err := backendFor(*language)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp explain:", err)
		return 1
	}
	// Named by capability rather than by backend, so a runtime added later gets
	// the same answer without this command being edited.
	explainer, canExplain := b.(interface {
		ExplainValue(context.Context, model.ExplainRequest, time.Duration) (model.ValueHistory, error)
	})
	if !canExplain {
		fmt.Fprintf(os.Stderr, "dbgmcp explain: the %s backend cannot follow a value's history -- it needs watchpoints. "+
			"Run `dbgmcp doctor` and `describe_backend`, and use `dbgmcp trace` at the write sites instead.\n", b.Name())
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+2*time.Minute)
	defer cancel()
	defer func() { _ = b.Stop(context.Background()) }()

	req := model.LaunchRequest{
		Mode: model.LaunchMode(*mode), Target: *target, WorkDir: workDir, TestRun: *testRun,
		BuildTags: splitTags(*tags), BuildFlags: *buildFlg, Deterministic: !*shuffle,
	}
	if err := b.Launch(ctx, req); err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp explain:", err)
		return 1
	}

	history, err := explainer.ExplainValue(ctx, model.ExplainRequest{
		Scope: location, Expression: *expr, Condition: *when, MaxWrites: *maxWrites,
	}, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp explain:", err)
		return 1
	}

	var rendered string
	switch *format {
	case "json":
		encoded, err := json.MarshalIndent(history, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "dbgmcp explain:", err)
			return 1
		}
		rendered = string(encoded) + "\n"
	case "md":
		rendered = renderExplainMarkdown(req, history)
	default:
		fmt.Fprintf(os.Stderr, "dbgmcp explain: unknown format %q (md or json)\n", *format)
		return 2
	}

	if *out == "-" {
		fmt.Print(rendered)
	} else if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp explain:", err)
		return 1
	}
	// Zero even when the history shows the bug. The failing test fails the build,
	// not the diagnostic: one that can break a pipeline stops being run.
	return 0
}

const explainUsage = `Follow one value and write out every change to it.

For "why is this wrong" rather than "what is this now". Answers with the sequence
of writes -- what the value was, what it became, where, and in which call --
instead of somewhere to start guessing.

  dbgmcp explain -dir . -mode test -target ./internal/billing \
    -test TestSubtotal -scope billing.Subtotal -expr total

A function called many times needs the call named, or the history is whichever
call happened to be first:

  dbgmcp explain -scope billing.Charge -expr amount -when 'order.ID == 42'

Options:
`
