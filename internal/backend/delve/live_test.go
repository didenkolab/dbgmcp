package delve

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// fixtureDir is the deliberately buggy program this suite debugs for real.
// There are no mocks in this file on purpose: a mocked debugger proves nothing
// about whether the plugin can debug.
func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "buggy")
}

func startFixture(t *testing.T, mode model.LaunchMode) *Backend {
	t.Helper()
	if _, err := FindDelve(); err != nil {
		t.Skipf("delve is not installed: %v", err)
	}
	b := New()
	t.Cleanup(func() { _ = b.Stop(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req := model.LaunchRequest{Mode: mode, Target: ".", WorkDir: fixtureDir(t)}
	if mode == model.LaunchTest {
		req.TestRun = "TestSubtotal"
	}
	if err := b.Launch(ctx, req); err != nil {
		t.Fatalf("launch: %v", err)
	}
	return b
}

func TestLiveBreakpointBySymbolStopsWithFullContext(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	ctx := context.Background()

	// By symbol, not by line: the agent should not have to read the file and
	// count lines to break on a function.
	bp, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: model.Location{Symbol: "main.lineTotal"}})
	if err != nil {
		t.Fatalf("set breakpoint by symbol: %v", err)
	}
	if bp.Location.Line == 0 || !strings.HasSuffix(bp.Location.File, "main.go") {
		t.Fatalf("symbol did not resolve to a real location: %+v", bp.Location)
	}

	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	stop, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	if stop.State != model.StatePaused || stop.Reason != model.StopBreakpoint {
		t.Fatalf("expected a breakpoint pause, got state=%s reason=%s msg=%s", stop.State, stop.Reason, stop.Message)
	}
	// The whole point of returning everything at once: the agent must not need
	// four more calls to learn where it is.
	if len(stop.Frames) == 0 {
		t.Error("no stack frames in the stop event")
	}
	if stop.Unit == nil || stop.Unit.Kind != model.UnitGoroutine {
		t.Errorf("execution unit missing or mislabelled: %+v", stop.Unit)
	}
	if stop.Source == nil || stop.Source.MarkLine != bp.Location.Line {
		t.Errorf("source context missing or pointing at the wrong line: %+v", stop.Source)
	}

	byPath := map[string]model.Variable{}
	for _, v := range stop.Variables {
		byPath[v.Name] = v
	}
	it, ok := byPath["it"]
	if !ok {
		t.Fatalf("the function argument 'it' was not captured; got %d variables", len(stop.Variables))
	}
	// Fields must arrive as paths that are themselves valid expressions, so the
	// agent can go straight from reading a value to evaluating one.
	for _, path := range []string{"it.Name", "it.Price", "it.Qty"} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("no entry for %q; the flattened paths were %v", path, keysOf(byPath))
		}
	}
	if !strings.Contains(it.Value, "Price") {
		t.Errorf("the struct was not rendered readably: %q", it.Value)
	}
	t.Logf("stopped at %s:%d with it=%s", bp.Location.File, bp.Location.Line, it.Value)
}

func TestLiveEvaluateSeesTheBug(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	ctx := context.Background()

	if _, err := b.SetBreakpoint(ctx, model.Breakpoint{
		Location:  model.Location{Symbol: "main.lineTotal"},
		Condition: `it.Price > 100`,
	}); err != nil {
		t.Fatalf("set conditional breakpoint: %v", err)
	}
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := b.WaitForStop(ctx, 60*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}

	// The condition must have selected the expensive item, and only it.
	name, err := b.Evaluate(ctx, 0, "it.Name", model.ValueBudget{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !strings.Contains(name.Value, "chair") {
		t.Fatalf("condition did not select the expensive item: it.Name = %s", name.Value)
	}

	// And here is the bug, visible as a disagreement between what the code
	// returns and what it should: Price alone, rather than Price * Qty.
	got, err := b.Evaluate(ctx, 0, "it.Price", model.ValueBudget{})
	if err != nil {
		t.Fatalf("evaluate price: %v", err)
	}
	want, err := b.Evaluate(ctx, 0, "it.Price * it.Qty", model.ValueBudget{})
	if err != nil {
		t.Fatalf("evaluate expected total: %v", err)
	}
	if got.Value == want.Value {
		t.Fatalf("fixture no longer demonstrates the bug: %s == %s", got.Value, want.Value)
	}
	t.Logf("bug confirmed at runtime: returns %s, should be %s", got.Value, want.Value)
}

func TestLiveStopLeavesNoProcessBehind(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Stop must be safe twice: a debuggee can legitimately have exited already.
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

func keysOf(m map[string]model.Variable) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLiveTraceRecordsEveryIterationInOneCall is the differentiating claim of
// this project, stated as a test: watching a loop must not cost a round trip
// per iteration.
func TestLiveTraceRecordsEveryIterationInOneCall(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	ctx := context.Background()

	tr, err := b.Trace(ctx, []model.Probe{{
		Location: model.Location{Symbol: "main.lineTotal"},
		Record:   []string{"it.Name", "it.Price", "it.Qty"},
	}}, 60*time.Second)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}

	// The cart has three items and the worker goroutine prices it again, so
	// every call must appear -- not just the first.
	if len(tr.Hits) < 3 {
		t.Fatalf("expected at least three recorded hits, got %d (%s)", len(tr.Hits), tr.Message)
	}
	// Delve without eBPF stops the debuggee at every hit and resumes it itself.
	// The saving is round trips, not observer effect, and the transcript has to
	// say so -- an agent chasing a race must not be told the run was untouched.
	if !tr.PerturbsTiming || tr.Mode != "auto_continue" {
		t.Errorf("expected mode=auto_continue with perturbs_timing=true, got mode=%q perturbs=%v",
			tr.Mode, tr.PerturbsTiming)
	}
	if len(tr.ProbesNeverHit) != 0 {
		t.Errorf("a probe that clearly fired was reported as never hit: %v", tr.ProbesNeverHit)
	}

	// The values are the point: each hit must carry the expressions asked for,
	// keyed by the expression itself.
	first := tr.Hits[0]
	for _, expr := range []string{"it.Name", "it.Price", "it.Qty"} {
		if _, ok := first.Values[expr]; !ok {
			t.Errorf("hit is missing %q; it has %v", expr, first.Values)
		}
	}

	// And the bug must be visible in the transcript alone: one item whose price
	// exceeds 100, which is the one the code mishandles.
	var expensive int
	for _, h := range tr.Hits {
		if h.Values["it.Price"] == "150" {
			expensive++
		}
	}
	if expensive == 0 {
		t.Errorf("the transcript never shows the expensive item: %+v", tr.Hits)
	}
	t.Logf("%d hits recorded in one call, status=%s", len(tr.Hits), tr.Status)
}

// TestLiveTraceStopsAtItsBudget proves a trace is bounded: a probe with a hit
// budget must not run the program to completion when it does not have to.
func TestLiveTraceStopsAtItsBudget(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	tr, err := b.Trace(context.Background(), []model.Probe{{
		Location: model.Location{Symbol: "main.lineTotal"},
		Record:   []string{"it.Price"},
		MaxHits:  2,
	}}, 60*time.Second)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if tr.Status != model.TraceCompleted {
		t.Errorf("a satisfied budget should report 'completed', got %q (%s)", tr.Status, tr.Message)
	}
	if len(tr.Hits) != 2 {
		t.Errorf("budget of 2 produced %d hits", len(tr.Hits))
	}
}

// TestLiveTraceNamesProbesThatNeverFired keeps an empty transcript
// distinguishable from a misplaced probe -- the same output, entirely
// different problems.
func TestLiveTraceNamesProbesThatNeverFired(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	tr, err := b.Trace(context.Background(), []model.Probe{{
		Location: model.Location{Symbol: "main.worker"},
		Record:   []string{"items"},
		MaxHits:  1,
	}, {
		// A real line in a function that does run, on a branch this program
		// never takes -- the realistic shape of a probe that never fires.
		Location: model.Location{File: filepath.Join(fixtureDir(t), "main.go"), Line: neverReachedLine(t)},
		Record:   []string{"it.Qty"},
		MaxHits:  1,
	}}, 30*time.Second)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(tr.ProbesNeverHit) != 1 || !strings.Contains(tr.ProbesNeverHit[0], "main.go") {
		t.Errorf("expected exactly the unused probe to be reported, got %v", tr.ProbesNeverHit)
	}
}

// neverReachedLine finds the fixture's deliberately dead branch by its marker,
// so the test does not break every time a line is inserted above it.
func neverReachedLine(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir(t), "main.go"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "NEVER-REACHED") {
			return i + 1
		}
	}
	t.Fatal("the fixture no longer contains a NEVER-REACHED marker")
	return 0
}

// TestLiveTraceRefusesAProbeOnAnExistingBreakpointClearly covers the collision
// an agent walks into naturally: set a breakpoint to look around, then try to
// trace the same place. Delve's own message names neither the breakpoint nor
// the way out.
func TestLiveTraceRefusesAProbeOnAnExistingBreakpointClearly(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	ctx := context.Background()

	bp, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: model.Location{Symbol: "main.lineTotal"}})
	if err != nil {
		t.Fatalf("set breakpoint: %v", err)
	}

	_, err = b.Trace(ctx, []model.Probe{{
		Location: model.Location{Symbol: "main.lineTotal"},
		Record:   []string{"it.Price"},
	}}, 30*time.Second)
	if err == nil {
		t.Fatal("tracing onto an existing breakpoint must be refused, not silently taken over")
	}
	msg := err.Error()
	for _, want := range []string{"main.lineTotal", "remove_breakpoint", bp.ID} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}

	// And the failed trace must leave nothing of its own behind.
	bps, err := b.ListBreakpoints(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(bps) != 1 {
		t.Errorf("a failed trace left probes behind: %d breakpoints remain", len(bps))
	}
}

// TestLiveCapturesWhatTheDebuggeePrinted covers the gap that has no workaround
// outside an IDE: with no console to look at, a program's own output would be
// invisible to the agent entirely.
func TestLiveCapturesWhatTheDebuggeePrinted(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	ctx := context.Background()

	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	ev, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if ev.State != model.StateExited {
		t.Fatalf("expected the fixture to run to completion, got %s", ev.State)
	}

	page, err := b.Output(ctx, 0, 100)
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	var all string
	for _, c := range page.Chunks {
		all += c.Text + "\n"
	}
	// The fixture prints both of these; reading them back proves the redirect,
	// the tailer and the drain-on-exit all work.
	for _, want := range []string{"subtotal: 260", "from worker: 260"} {
		if !strings.Contains(all, want) {
			t.Errorf("captured output is missing %q; got:\n%s", want, all)
		}
	}
	if page.Dropped != 0 {
		t.Errorf("nothing should have been dropped, got %d", page.Dropped)
	}

	// The cursor must leave nothing new behind it.
	if next, err := b.Output(ctx, page.NextSince, 100); err != nil || len(next.Chunks) != 0 {
		t.Errorf("reading past the cursor returned %d chunks (err=%v)", len(next.Chunks), err)
	}
}

// TestLiveStatusReportsThePauseWithoutResuming keeps re-inspection free: an
// agent that wandered off to read source must not have to resume to find out
// where it still is.
func TestLiveStatusReportsThePauseWithoutResuming(t *testing.T) {
	b := startFixture(t, model.LaunchDebug)
	ctx := context.Background()

	if _, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: model.Location{Symbol: "main.lineTotal"}}); err != nil {
		t.Fatalf("set breakpoint: %v", err)
	}
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	first, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	again, err := b.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if again.State != model.StatePaused {
		t.Fatalf("status lost the pause: %s", again.State)
	}
	if len(again.Frames) == 0 || len(first.Frames) == 0 {
		t.Fatal("no frames to compare")
	}
	if again.Frames[0].Line != first.Frames[0].Line || again.Frames[0].Function != first.Frames[0].Function {
		t.Errorf("status moved the program: was %s:%d, now %s:%d",
			first.Frames[0].Function, first.Frames[0].Line, again.Frames[0].Function, again.Frames[0].Line)
	}
}
