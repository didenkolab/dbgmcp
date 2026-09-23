package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/backend/delve"
	"github.com/didenkolab/dbgmcp/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This suite runs diff_runs against a real target under a real debugger. A
// mocked comparison would only prove that Compare works, which internal/diffruns
// already establishes; what is in doubt here is whether two runs launched and
// torn down in sequence produce transcripts that are actually comparable.

func diffFixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "diffpair")
}

// contentText flattens a refusal into the string the model would actually read.
func contentText(content []mcp.Content) string {
	var b strings.Builder
	for _, c := range content {
		if text, isText := c.(*mcp.TextContent); isText {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// lineContaining finds a line by its content, so editing the fixture cannot
// silently point a probe at the wrong place.
func lineContaining(t *testing.T, file, needle string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("%s no longer contains %q", file, needle)
	return 0
}

func diffFixture(t *testing.T) (*Registry, DiffRunsIn) {
	t.Helper()
	if _, err := delve.FindDelve(); err != nil {
		t.Skipf("delve is not installed: %v", err)
	}
	dir := diffFixtureDir(t)
	src := filepath.Join(dir, "pricing.go")

	return NewRegistry(session.NewStore()), DiffRunsIn{
		Mode: "test", Target: ".", WorkDir: dir,
		Probes: []ProbeIn{
			// Probe 0 agrees in both runs. It is here so the test can show that
			// an agreeing earlier probe does not mask the divergence after it.
			{File: src, Line: lineContaining(t, src, "// COUPON"), Record: []string{"price", "percent"}},
			// Probe 1 is where the coupon disappears.
			{File: src, Line: lineContaining(t, src, "// CHARGE"), Record: []string{"price"}},
		},
		TimeoutSec: 90,
	}
}

func TestLiveDiffRunsFindsWhereTheCouponDisappeared(t *testing.T) {
	r, in := diffFixture(t)
	in.RunA = RunVariantIn{Label: "ordinary", TestRun: "TestOrdinaryCustomerGetsTheCoupon"}
	in.RunB = RunVariantIn{Label: "loyal", TestRun: "TestLoyalCustomerAlsoGetsTheCoupon"}

	res, out, err := r.diffRuns(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("diff_runs: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("diff_runs failed: %v", res.Content)
	}

	if out.Compared == 0 {
		t.Fatalf("nothing was compared, so agreement would be meaningless: %+v", out)
	}
	if out.First == nil {
		t.Fatalf("the two runs were reported as agreeing. run_a=%+v run_b=%+v", out.RunA, out.RunB)
	}

	// The divergence must be the computed price, at the second probe -- not the
	// first probe, which agrees, and not the input.
	if out.First.Probe != 1 {
		t.Errorf("first divergence at probe %d, want 1 (probe 0 agrees in both runs)", out.First.Probe)
	}
	if out.First.Expression != "price" {
		t.Errorf("first divergence on %q, want price", out.First.Expression)
	}
	if out.First.Left != "800" || out.First.Right != "900" {
		t.Errorf("prices = %q vs %q, want 800 vs 900", out.First.Left, out.First.Right)
	}
	if out.First.Line != lineContaining(t, filepath.Join(in.WorkDir, "pricing.go"), "// CHARGE") {
		t.Errorf("divergence reported at line %d, want the CHARGE line", out.First.Line)
	}

	// Both labels must survive into the report: without them a reader cannot
	// tell which side of "800 vs 900" was the failing run.
	if out.RunA.Label != "ordinary" || out.RunB.Label != "loyal" {
		t.Errorf("labels lost: %q and %q", out.RunA.Label, out.RunB.Label)
	}
	for _, run := range []RunSummary{out.RunA, out.RunB} {
		if run.Hits == 0 {
			t.Errorf("%s recorded nothing", run.Label)
		}
		if len(run.ProbesNeverHit) > 0 {
			t.Errorf("%s missed probes %v -- a misplaced probe would look like agreement", run.Label, run.ProbesNeverHit)
		}
	}
}

func TestLiveDiffRunsDoesNotInventDifferencesBetweenIdenticalRuns(t *testing.T) {
	// The control, and the test most likely to fail for a real reason: if an
	// address, a goroutine id or an iteration order leaked into a recorded
	// value, every diff would report noise and the tool would be worthless.
	r, in := diffFixture(t)
	in.RunA = RunVariantIn{Label: "first", TestRun: "TestOrdinaryCustomerGetsTheCoupon"}
	in.RunB = RunVariantIn{Label: "second", TestRun: "TestOrdinaryCustomerGetsTheCoupon"}

	res, out, err := r.diffRuns(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("diff_runs: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("diff_runs failed: %v", res.Content)
	}
	if out.Compared == 0 {
		t.Fatal("nothing was compared, so this proves nothing about noise")
	}
	if out.First != nil {
		t.Errorf("two runs of the same test diverged at %+v -- that is measurement noise, not a bug", *out.First)
	}
}

func TestDiffRunsRefusesToAttach(t *testing.T) {
	// Refusing by name, with the alternative, rather than failing obscurely
	// inside a launch that could never have worked.
	r := NewRegistry(session.NewStore())
	res, _, err := r.diffRuns(context.Background(), nil, DiffRunsIn{
		Mode: "attach", WorkDir: "/tmp",
		Probes: []ProbeIn{{Symbol: "main.main", Record: []string{"x"}}},
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("mode=attach was accepted")
	}
	text := contentText(res.Content)
	if !strings.Contains(text, "attach") || !strings.Contains(text, "trace_execution") {
		t.Errorf("refusal does not name the problem and the alternative: %q", text)
	}
}

// testPython is the interpreter the Python fixture runs under. The repository
// keeps an isolated virtualenv for it so the suite never depends on whatever is
// installed in the developer's active environment.
func testPython(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	venv := filepath.Join(filepath.Dir(thisFile), "..", "..", ".venv-test", "bin", "python")
	if _, err := os.Stat(venv); err != nil {
		t.Skip("no .venv-test with debugpy; create it with: python3 -m venv .venv-test && .venv-test/bin/pip install debugpy")
	}
	return venv
}

func TestLiveDiffRunsWorksOnASecondRuntime(t *testing.T) {
	// diff_runs is language-neutral by construction: it compares transcripts,
	// and a transcript is the same shape whichever backend produced it. That is
	// an argument, not evidence, until it has been run against a backend that
	// reaches the debuggee by a completely different route -- here a DAP adapter
	// instead of Delve's own RPC.
	//
	// It also exercises the args path, which the Go case does not: there the two
	// runs differ by a test filter.
	t.Setenv("DBGMCP_PYTHON", testPython(t))

	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "diffpair_py")
	src := filepath.Join(dir, "pricing.py")

	in := DiffRunsIn{
		Language: "python", Mode: "debug", Target: "pricing.py", WorkDir: dir,
		RunA: RunVariantIn{Label: "ordinary"},
		RunB: RunVariantIn{Label: "loyal", Args: []string{"--loyal"}},
		Probes: []ProbeIn{
			{File: src, Line: lineContaining(t, src, "# COUPON"), Record: []string{"price", "percent"}},
			{File: src, Line: lineContaining(t, src, "# CHARGE"), Record: []string{"price"}},
		},
		TimeoutSec: 120,
	}

	r := NewRegistry(session.NewStore())
	res, out, err := r.diffRuns(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("diff_runs: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("diff_runs failed: %s", contentText(res.Content))
	}

	if out.Compared == 0 {
		t.Fatalf("nothing was compared: run_a=%+v run_b=%+v", out.RunA, out.RunB)
	}
	if out.First == nil {
		t.Fatalf("the two runs were reported as agreeing. run_a=%+v run_b=%+v", out.RunA, out.RunB)
	}
	// The same answer as the Go fixture, from a different debugger.
	if out.First.Probe != 1 || out.First.Expression != "price" {
		t.Errorf("first divergence at probe %d on %q, want probe 1 on price", out.First.Probe, out.First.Expression)
	}
	if out.First.Left != "800" || out.First.Right != "900" {
		t.Errorf("prices = %q vs %q, want 800 vs 900", out.First.Left, out.First.Right)
	}
}
