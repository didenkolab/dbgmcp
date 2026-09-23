package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/didenkolab/dbgmcp/internal/diffruns"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// diffCommand runs the same probes over two runs and reports where they first
// disagreed.
//
// This is the form a pipeline needs. "It passes locally and fails in CI", or
// "this test is flaky", is not a question one transcript can answer: nothing in
// a single run says what the value should have been. Two runs do, and the
// artifact left behind names the first place they parted.
func diffCommand(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		language = fs.String("lang", "go", "runtime of the target: go, python or node")
		dir      = fs.String("dir", ".", "directory to run in")
		mode     = fs.String("mode", "test", "test, debug or exec")
		target   = fs.String("target", ".", "package for test/debug, or binary for exec")
		testA    = fs.String("test-a", "", "test filter for the first run")
		testB    = fs.String("test-b", "", "test filter for the second run")
		labelA   = fs.String("label-a", "run_a", "name for the first run in the report")
		labelB   = fs.String("label-b", "run_b", "name for the second run in the report")
		timeout  = fs.Duration("timeout", 2*time.Minute, "how long to let each run proceed")
		format   = fs.String("format", "md", "md or json")
		out      = fs.String("out", "-", "file to write, or - for stdout")
		maxHits  = fs.Int("max-hits", 0, "stop each probe after this many hits (0 = until the target ends)")
		tags     = fs.String("tags", "", "build tags the target needs, space or comma separated")
		buildFlg = fs.String("build-flags", "", "anything else the build needs, passed through verbatim")
	)
	var probeFlags, argsA, argsB stringList
	fs.Var(&probeFlags, "probe", "where and what to record: file:line=expr,expr or symbol=expr,expr (repeatable)")
	fs.Var(&argsA, "arg-a", "argument for the first run (repeatable)")
	fs.Var(&argsB, "arg-b", "argument for the second run (repeatable)")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, diffUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(probeFlags) == 0 {
		fmt.Fprintln(os.Stderr, "dbgmcp diff: at least one -probe is required")
		fs.Usage()
		return 2
	}
	if model.LaunchMode(*mode).IsAttach() {
		fmt.Fprintln(os.Stderr, "dbgmcp diff: mode=attach cannot be diffed -- comparing two runs means starting both. Use `dbgmcp trace` instead.")
		return 2
	}

	workDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp diff:", err)
		return 1
	}
	probes, err := parseProbes(probeFlags, workDir, *maxHits)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp diff:", err)
		return 2
	}

	base := model.LaunchRequest{
		Mode: model.LaunchMode(*mode), Target: *target, WorkDir: workDir,
		BuildTags: splitTags(*tags), BuildFlags: *buildFlg,
		// Always on here, unlike `trace`, where it is a flag. Two runs of a
		// shuffled suite execute different tests in a different order, so the
		// divergence found would be the shuffle rather than the bug.
		Deterministic: true,
	}

	left, err := traceVariant(*language, base, *testA, argsA, probes, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbgmcp diff: the %s run failed before it could be compared: %v\n", *labelA, err)
		return 1
	}
	right, err := traceVariant(*language, base, *testB, argsB, probes, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbgmcp diff: the %s run failed, so there is nothing to compare: %v\n", *labelB, err)
		return 1
	}

	comparison := diffruns.Compare(left, right, probes)

	var rendered string
	switch *format {
	case "json":
		encoded, err := json.MarshalIndent(comparison, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "dbgmcp diff:", err)
			return 1
		}
		rendered = string(encoded) + "\n"
	case "md":
		rendered = renderDiffMarkdown(base, *labelA, *labelB, left, right, comparison)
	default:
		fmt.Fprintf(os.Stderr, "dbgmcp diff: unknown format %q (md or json)\n", *format)
		return 2
	}

	if *out == "-" {
		fmt.Print(rendered)
	} else if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "dbgmcp diff:", err)
		return 1
	}
	// Zero even when the runs diverged. A divergence is the expected output here
	// -- it is why the command was run -- and a diagnostic that can break a
	// pipeline stops being run.
	return 0
}

// traceVariant runs one side and tears it down, so the second run starts from
// the same state the first did rather than alongside it. Two debuggers on one
// target collide over the build output and over anything the program binds, and
// a diff that fails for that reason looks just like a diff that found something.
func traceVariant(language string, base model.LaunchRequest, testRun string, args []string, probes []model.Probe, timeout time.Duration) (model.Transcript, error) {
	b, err := backendFor(language)
	if err != nil {
		return model.Transcript{}, err
	}
	defer func() { _ = b.Stop(context.Background()) }()

	req := base
	req.TestRun = testRun
	req.Args = args

	ctx, cancel := context.WithTimeout(context.Background(), timeout+2*time.Minute)
	defer cancel()
	if err := b.Launch(ctx, req); err != nil {
		return model.Transcript{}, err
	}
	return b.Trace(ctx, probes, timeout)
}

const diffUsage = `Run the same probes over two runs and report where they first disagreed.

For the question one transcript cannot answer: this input works and that one does
not, or this test passes only sometimes. The passing run is the specification,
and the first place the other departs from it is where to look.

  dbgmcp diff -dir . -target ./internal/pricing \
    -test-a TestOrdinaryCustomer -label-a passing \
    -test-b TestLoyalCustomer    -label-b failing \
    -probe internal/pricing/price.go:35=price

Two runs of the same test, to chase something that is flaky rather than wrong:

  dbgmcp diff -test-a TestCharge -test-b TestCharge \
    -probe internal/billing/charge.go:88=amount,attempt

Options:
`
