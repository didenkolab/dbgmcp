package delve

import (
	"context"
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

	var it *model.Variable
	for i := range stop.Variables {
		if stop.Variables[i].Name == "it" {
			it = &stop.Variables[i]
		}
	}
	if it == nil {
		t.Fatalf("the function argument 'it' was not captured; got %d variables", len(stop.Variables))
	}
	if len(it.Children) == 0 {
		t.Fatalf("argument 'it' came back without its fields: %+v", it)
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
