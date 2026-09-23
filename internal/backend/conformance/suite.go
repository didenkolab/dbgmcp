// Package conformance is one test suite run against every backend.
//
// It exists to keep describe_backend honest. A capability that is declared but
// not exercised is worse than no capability model at all, because an agent will
// plan against it and be wrong in a way it cannot detect. Every field of
// Capabilities is checked here, in both directions: a declared capability must
// work, and an undeclared one must refuse with a message that names what is
// missing.
package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// Fixture describes a program the suite can drive. A backend supplies one
// pointing at a target in its own language.
type Fixture struct {
	Name string
	// New returns a fresh, unstarted backend. Each subtest gets its own,
	// because stepping and watchpoints both mutate session state.
	New func() backend.Backend
	// Launch starts the fixture program.
	Launch model.LaunchRequest
	// LaunchWithAncestry is Launch plus whatever the runtime needs to record
	// where its execution units came from. A zero value means the backend needs
	// nothing extra.
	LaunchWithAncestry model.LaunchRequest

	// CallSymbol is a function called more than once, with at least one
	// argument, resolvable by name without a line number.
	CallSymbol string
	// CallFile and CallLine name the same place as CallSymbol, for backends
	// that cannot resolve a function name. Requiring both is not duplication:
	// it is what lets this suite run against a backend whose capability set is
	// smaller, which is the only way to find out whether the abstraction holds.
	CallFile string
	CallLine int
	// IntExpr is an int-valued, settable expression in scope at CallSymbol.
	IntExpr string
	// CallExpr calls a function. Used to check how evaluation is guarded.
	CallExpr string

	// LoopSymbol is a function containing a local that changes across a loop.
	LoopSymbol string
	// LoopFile and LoopLine name a line inside that loop, for the same reason.
	LoopFile string
	LoopLine int
	// LoopLocal is that local's name, in scope at LoopSymbol.
	LoopLocal string

	// SpawnedSymbol is a function that runs in a unit created by another unit.
	SpawnedSymbol string
	SpawnedFile   string
	SpawnedLine   int
}

// Run executes the whole suite. Call it from each backend's own test file.
func Run(t *testing.T, f Fixture) {
	t.Run("capabilities are internally consistent", func(t *testing.T) { testConsistency(t, f) })
	t.Run("breakpoint by symbol", func(t *testing.T) { testBySymbol(t, f) })
	t.Run("hit counts", func(t *testing.T) { testHitCounts(t, f) })
	t.Run("stepping", func(t *testing.T) { testStepping(t, f) })
	t.Run("frame selection", func(t *testing.T) { testFrameSelection(t, f) })
	t.Run("set variable", func(t *testing.T) { testSetVariable(t, f) })
	t.Run("watchpoints", func(t *testing.T) { testWatchpoints(t, f) })
	t.Run("stepping with a watchpoint set", func(t *testing.T) { testStepWithWatchpoint(t, f) })
	t.Run("trace mode", func(t *testing.T) { testTrace(t, f) })
	t.Run("evaluation guard", func(t *testing.T) { testEvalGuard(t, f) })
	t.Run("ancestry", func(t *testing.T) { testAncestry(t, f) })
}

func start(t *testing.T, f Fixture, req model.LaunchRequest) backend.Backend {
	t.Helper()
	b := f.New()
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := b.Launch(ctx, req); err != nil {
		t.Fatalf("launch: %v", err)
	}
	return b
}

// runTo is the "get somewhere interesting" helper every other check needs.
//
// It takes both a symbol and a file/line for the same place, and uses whichever
// the backend can do. An earlier version took only a symbol, which quietly made
// the whole suite unrunnable against a backend without that capability -- the
// first thing a second implementation revealed.
func runTo(t *testing.T, b backend.Backend, symbol, file string, line int) model.StopEvent {
	t.Helper()
	ctx := context.Background()

	where := model.Location{Symbol: symbol}
	if !b.Capabilities().BreakpointBySymbol {
		if file == "" || line <= 0 {
			t.Skipf("backend cannot break by symbol and the fixture gives no file and line for %s", symbol)
		}
		where = model.Location{File: file, Line: line}
	}
	if _, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: where}); err != nil {
		t.Fatalf("set breakpoint at %v: %v", where, err)
	}
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	ev, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if ev.State != model.StatePaused {
		t.Fatalf("expected to stop at %s, got state=%s reason=%s: %s", symbol, ev.State, ev.Reason, ev.Message)
	}
	return ev
}

// callLocation names the call site in whichever form the backend can accept.
// Every check that places a breakpoint goes through this rather than reaching
// for the symbol, because reaching for the symbol is what made the suite
// unrunnable against a backend without that capability.
func callLocation(b backend.Backend, f Fixture) model.Location {
	if b.Capabilities().BreakpointBySymbol {
		return model.Location{Symbol: f.CallSymbol}
	}
	return model.Location{File: f.CallFile, Line: f.CallLine}
}

func runToCall(t *testing.T, b backend.Backend, f Fixture) model.StopEvent {
	t.Helper()
	return runTo(t, b, f.CallSymbol, f.CallFile, f.CallLine)
}

func runToLoop(t *testing.T, b backend.Backend, f Fixture) model.StopEvent {
	t.Helper()
	return runTo(t, b, f.LoopSymbol, f.LoopFile, f.LoopLine)
}

func requireUnsupported(t *testing.T, err error, capability string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s is not declared, so it must refuse, but it succeeded", capability)
	}
	var ue *backend.UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("%s is not declared, so it must refuse with an UnsupportedError naming it; got %v", capability, err)
	}
	if !strings.Contains(ue.Error(), capability) {
		t.Errorf("refusal does not name %q: %v", capability, ue)
	}
}

func testConsistency(t *testing.T, f Fixture) {
	caps := f.New().Capabilities()

	declaresWatchKind := false
	hasLine := false
	for _, k := range caps.BreakpointKinds {
		if k == string(model.BreakWatch) {
			declaresWatchKind = true
		}
		if k == string(model.BreakLine) {
			hasLine = true
		}
	}
	if (caps.Watchpoints != backend.SupportNone) != declaresWatchKind {
		t.Errorf("Watchpoints=%s but breakpoint kinds are %v: the two must agree",
			caps.Watchpoints, caps.BreakpointKinds)
	}
	if !hasLine {
		t.Error("every backend must support line breakpoints")
	}
}

func testBySymbol(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()

	bp, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: model.Location{Symbol: f.CallSymbol}})
	if !b.Capabilities().BreakpointBySymbol {
		requireUnsupported(t, err, "breakpoint_by_symbol")
		return
	}
	if err != nil {
		t.Fatalf("breakpoint_by_symbol is declared but setting one failed: %v", err)
	}
	// A symbol must resolve to a real place, or the agent cannot tell where it
	// will stop.
	if bp.Location.File == "" || bp.Location.Line == 0 {
		t.Errorf("symbol resolved to no location: %+v", bp.Location)
	}
}

func testHitCounts(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	caps := b.Capabilities()

	runToCall(t, b, f)
	// Go round twice, so a count of one cannot pass by accident.
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := b.WaitForStop(ctx, 60*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}

	bps, err := b.ListBreakpoints(ctx)
	if err != nil || len(bps) == 0 {
		t.Fatalf("list breakpoints: %v (%d found)", err, len(bps))
	}
	bp := bps[0]

	switch caps.HitCounts {
	case backend.HitCountsNone:
		if bp.HitCount != 0 {
			t.Errorf("hit_counts is 'none' but a count of %d was reported", bp.HitCount)
		}
	case backend.HitCountsTotal, backend.HitCountsPerUnit:
		if bp.HitCount < 2 {
			t.Errorf("hit_counts is %q but after two hits the count is %d", caps.HitCounts, bp.HitCount)
		}
		if caps.HitCounts == backend.HitCountsPerUnit && len(bp.HitCountByUnit) == 0 {
			t.Error("hit_counts is 'per_unit' but no per-unit breakdown was reported")
		}
	}
}

func testStepping(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	before := runToCall(t, b, f)

	after, err := b.Step(ctx, model.StepOver)
	if err != nil {
		t.Fatalf("step over: %v", err)
	}
	// Stepping must return where it landed. Making the agent ask again for the
	// answer it just paid a call for is exactly the cost this design avoids.
	if len(after.Frames) == 0 {
		t.Error("step returned no frames")
	}
	if after.State != model.StatePaused {
		t.Fatalf("after a step the target should be paused, got %s", after.State)
	}
	if len(before.Frames) > 0 && len(after.Frames) > 0 &&
		before.Frames[0].Line == after.Frames[0].Line && before.Frames[0].Function == after.Frames[0].Function {
		t.Errorf("step over did not move: still at %s:%d", after.Frames[0].Function, after.Frames[0].Line)
	}

	if _, err := b.Step(ctx, model.StepOut); err != nil {
		t.Errorf("step out: %v", err)
	}
}

// testFrameSelection checks that choosing a frame actually changes where the
// calls that do not name one look.
//
// A selection that is accepted and then ignored is worse than one refused:
// every later reading is of a frame the agent believes it left.
//
// The evidence is a name that exists only in the caller. An earlier version
// compared the same expression before and after, which passed on Go by an
// accident of naming and failed on Python and JavaScript, where the loop
// variable in the caller happens to share the callee's parameter name -- the
// test was wrong, not the backends.
func testFrameSelection(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	stop := runToCall(t, b, f)
	if len(stop.Frames) < 2 {
		t.Skipf("only %d frame(s) here, nothing to select between", len(stop.Frames))
	}

	// f.LoopLocal lives in the caller and not in the callee we stopped in.
	if _, err := b.Evaluate(ctx, 0, f.LoopLocal, model.ValueBudget{}); err == nil {
		t.Skipf("%q is readable from the innermost frame too, so it cannot show a selection", f.LoopLocal)
	}

	caller, err := b.SelectFrame(ctx, 1)
	if err != nil {
		t.Fatalf("select the caller: %v", err)
	}
	if caller.Index != 1 {
		t.Errorf("selected frame 1 and got back index %d", caller.Index)
	}

	if _, err := b.Evaluate(ctx, -1, f.LoopLocal, model.ValueBudget{}); err != nil {
		t.Errorf("%q is a local of the selected frame and still could not be read: %v", f.LoopLocal, err)
	}

	// And a frame that is not there must be refused, not silently clamped to
	// one that is.
	if _, err := b.SelectFrame(ctx, 9999); err == nil {
		t.Error("selecting a frame beyond the stack was accepted")
	}
}

func testSetVariable(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	runToCall(t, b, f)
	caps := b.Capabilities()

	err := b.SetVariable(ctx, 0, f.IntExpr, "7")
	if caps.SetVariable == backend.SupportNone {
		requireUnsupported(t, err, "set_variable")
		return
	}
	if err != nil {
		t.Fatalf("set_variable is declared %q but setting an int failed: %v", caps.SetVariable, err)
	}
	// Read it back: a write that cannot be observed is not a write.
	got, err := b.Evaluate(ctx, 0, f.IntExpr, model.ValueBudget{})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Value != "7" {
		t.Errorf("set %s to 7 but it reads as %s", f.IntExpr, got.Value)
	}
}

func testWatchpoints(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	caps := b.Capabilities()

	runToLoop(t, b, f)
	// A watchpoint needs the variable to exist already. Stopping at a function's
	// entry is before its locals are declared, so step until the name resolves
	// -- which is what an agent has to do too, and why the backend's refusal
	// says so.
	bringIntoScope(t, b, f.LoopLocal)

	wp, err := b.SetWatchpoint(ctx, 0, f.LoopLocal, model.WatchWrite)
	if caps.Watchpoints == backend.SupportNone {
		requireUnsupported(t, err, "watchpoints")
		return
	}
	if err != nil {
		t.Fatalf("watchpoints declared %q but watching %q failed: %v", caps.Watchpoints, f.LoopLocal, err)
	}
	if wp.Kind != model.BreakWatch {
		t.Errorf("a watchpoint came back as kind %q", wp.Kind)
	}

	// It must actually fire: a watchpoint that never stops anything is a
	// capability claim with nothing behind it.
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	ev, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait after watchpoint: %v", err)
	}
	if ev.State != model.StatePaused {
		t.Fatalf("the watchpoint did not fire: state=%s reason=%s %s", ev.State, ev.Reason, ev.Message)
	}

	// And it must be listed with the breakpoints, not in a parallel registry.
	bps, err := b.ListBreakpoints(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var seen bool
	for _, bp := range bps {
		if bp.Kind == model.BreakWatch {
			seen = true
		}
	}
	if !seen {
		t.Error("the watchpoint is not visible through list_breakpoints")
	}
}

// bringIntoScope steps forward until a local is evaluable, or gives up with a
// message that says which it was.
func bringIntoScope(t *testing.T, b backend.Backend, name string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := b.Evaluate(ctx, 0, name, model.ValueBudget{}); err == nil {
			return
		}
		if _, err := b.Step(ctx, model.StepOver); err != nil {
			t.Fatalf("stepping to bring %q into scope: %v", name, err)
		}
	}
	t.Fatalf("%q never came into scope after ten steps", name)
}

// testStepWithWatchpoint exercises single-stepping while a hardware watchpoint
// is armed.
//
// This is not a theoretical combination. On macOS Delve reaches the target
// through debugserver, and that path carries a workaround for a Mach kernel
// issue where stepping over a breakpoint with watchpoints set can deliver a
// spurious exception. Whether that workaround is transparent is a property of
// the platform, not of the debugger's documentation, so it is measured on every
// matrix leg rather than assumed.
func testStepWithWatchpoint(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	if b.Capabilities().Watchpoints == backend.SupportNone {
		t.Skip("backend declares no watchpoints")
	}

	runToLoop(t, b, f)
	bringIntoScope(t, b, f.LoopLocal)
	if _, err := b.SetWatchpoint(ctx, 0, f.LoopLocal, model.WatchWrite); err != nil {
		t.Fatalf("set watchpoint: %v", err)
	}

	// Three steps is enough to cross the loop body, which is where a write to
	// the watched local happens and where a spurious exception would surface.
	for i := 0; i < 3; i++ {
		ev, err := b.Step(ctx, model.StepOver)
		if err != nil {
			t.Fatalf("step %d with a watchpoint armed: %v", i+1, err)
		}
		if ev.State == model.StateExited {
			return // the frame returned, taking the watchpoint with it
		}
		if ev.State != model.StatePaused {
			t.Fatalf("step %d left the session in state %s (%s)", i+1, ev.State, ev.Message)
		}
		if len(ev.Frames) == 0 {
			t.Fatalf("step %d returned no frames", i+1)
		}
	}

	// The session must still be usable afterwards, not merely un-crashed.
	if _, err := b.Evaluate(ctx, 0, f.LoopLocal, model.ValueBudget{}); err != nil {
		t.Errorf("the session is unusable after stepping with a watchpoint: %v", err)
	}
}

func testTrace(t *testing.T, f Fixture) {
	b := start(t, f, f.Launch)
	ctx := context.Background()
	caps := b.Capabilities()

	_, err := b.SetBreakpoint(ctx, model.Breakpoint{
		Location: callLocation(b, f),
		Suspend:  model.SuspendNone,
		Record:   []string{f.IntExpr},
	})
	if err != nil {
		t.Fatalf("set tracepoint: %v", err)
	}
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	ev, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	// Every mode must deliver the thing they share: the run completes without
	// the agent resuming anything. What differs is whether the debuggee was
	// stopped along the way, and that is what the transcript has to admit.
	switch caps.TraceMode {
	case backend.TraceBuffered, backend.TraceAutoContinue:
		if ev.State != model.StateExited {
			t.Errorf("trace_mode is %q, so the run should have completed without the agent resuming it; state=%s reason=%s",
				caps.TraceMode, ev.State, ev.Reason)
		}
	case backend.TraceSuspendOnly:
		if ev.State != model.StatePaused {
			t.Errorf("trace_mode is 'suspend_only' so the target should have stopped and waited, state=%s", ev.State)
		}
	}

	// And the transcript's own account of itself must match the capability.
	// Getting this wrong is the worst failure available here: an agent chasing
	// a race would be told the observation was free of observer effect.
	fresh := start(t, f, f.Launch)
	tr, err := fresh.Trace(ctx, []model.Probe{{
		Location: callLocation(fresh, f),
		Record:   []string{f.IntExpr},
		MaxHits:  1,
	}}, 60*time.Second)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if tr.Mode != string(caps.TraceMode) {
		t.Errorf("transcript reports mode %q but the backend declares %q", tr.Mode, caps.TraceMode)
	}
	wantPerturbs := caps.TraceMode != backend.TraceBuffered
	if tr.PerturbsTiming != wantPerturbs {
		t.Errorf("trace_mode %q implies perturbs_timing=%v, transcript says %v",
			caps.TraceMode, wantPerturbs, tr.PerturbsTiming)
	}
}

func testEvalGuard(t *testing.T, f Fixture) {
	if f.CallExpr == "" {
		t.Skip("fixture supplies no calling expression")
	}
	b := start(t, f, f.Launch)
	ctx := context.Background()
	runToCall(t, b, f)

	_, err := b.Evaluate(ctx, 0, f.CallExpr, model.ValueBudget{})
	switch b.Capabilities().EvalCallsFunctions {
	case backend.SupportFull:
		if err != nil {
			t.Errorf("eval_calls_functions is 'full' but calling a function failed: %v", err)
		}
	case backend.SupportGuarded, backend.SupportNone:
		// The point of declaring this is that an agent knows not to plan around
		// function calls. If they silently work, the declaration is a lie in
		// the safe direction, which is still a lie.
		if err == nil {
			t.Errorf("eval_calls_functions is %q but %q was evaluated without objection",
				b.Capabilities().EvalCallsFunctions, f.CallExpr)
		}
	}
}

func testAncestry(t *testing.T, f Fixture) {
	if f.SpawnedSymbol == "" {
		t.Skip("fixture supplies no spawned symbol")
	}
	caps := f.New().Capabilities()
	req := f.LaunchWithAncestry
	if req.Mode == "" {
		req = f.Launch
	}
	b := start(t, f, req)
	ctx := context.Background()

	runTo(t, b, f.SpawnedSymbol, f.SpawnedFile, f.SpawnedLine)

	anc, err := b.Ancestors(ctx, "", 16)
	if !caps.Ancestry {
		if err == nil && len(anc.Chain) > 0 {
			t.Error("ancestry is not declared but a chain came back")
		}
		return
	}
	if err != nil {
		t.Fatalf("ancestry is declared but reading it failed: %v", err)
	}
	if len(anc.Chain) == 0 {
		t.Fatalf("ancestry is declared but the chain is empty. Note: %s", anc.Note)
	}
	// The chain must lead somewhere real, or it is decoration.
	if len(anc.Chain[0].Frames) == 0 {
		t.Error("the first ancestor has no frames")
	}
}
