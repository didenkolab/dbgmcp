package delve

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/service/api"
)

// Name is how this backend identifies itself in capabilities and error text.
const Name = "delve"

// Backend drives one Delve process. It is not safe for concurrent use by
// design: one session, one debuggee, one conversation.
type Backend struct {
	mu    sync.Mutex
	sup   *supervisor
	goVer *goversion.GoVersion

	// continueCh is live only between a Resume and the stop it produces. Delve
	// closes it after the first non-tracepoint stop, so a nil channel means
	// "not running" and is the single source of truth for that.
	continueCh <-chan *api.DebuggerState

	curFrame int
	pathMap  model.PathMapping
	info     Info
	// mode is kept because what a failure means depends on how the target was
	// started: a symbol missing from a binary this server built with
	// optimisations off means a typo, and from one it merely attached to it
	// usually means inlining.
	mode model.LaunchMode
}

// Tool reports which Delve this backend is actually using, so an agent
// diagnosing odd behaviour can see the version rather than assume the pinned
// one.
func (b *Backend) Tool() (name, path, version, supports string) {
	info := b.info
	if info.Path == "" {
		if resolved, err := Resolve(); err == nil {
			info = resolved
		}
	}
	return "dlv", info.Path, info.Version, info.SupportedGo
}

func New() *Backend { return &Backend{sup: &supervisor{}} }

func (b *Backend) Name() string { return Name }

// Capabilities reports what Delve can actually do. Every entry here is covered
// by the conformance suite; a claim that is not tested is worse than no claim,
// because the agent will plan against it.
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		Watchpoints: backend.SupportFull,
		// Not "buffered". Delve only buffers hits with eBPF uprobes, which are
		// Linux-only and privileged and which this server does not enable; the
		// macOS backend cannot do it at all. What Delve does here is resume
		// after each hit itself -- no agent round trip, but a real stop each
		// time. Claiming "buffered" would tell an agent chasing a race that the
		// trace was free of observer effect, which is the one thing it must not
		// believe.
		TraceMode: backend.TraceAutoContinue,
		HitCounts: backend.HitCountsPerUnit,
		// Delve can call functions in the target, but doing so runs arbitrary
		// code in the debuggee, so it is opt-in rather than on.
		EvalCallsFunctions: backend.SupportGuarded,
		SetVariable:        backend.SupportFull,
		BreakpointBySymbol: true,
		Ancestry:           true,
		BreakpointKinds:    []string{string(model.BreakLine), string(model.BreakWatch)},
	}
}

func (b *Backend) Launch(ctx context.Context, req model.LaunchRequest) error {
	if req.Mode.IsAttach() {
		if err := checkProcessExists(req.PID); err != nil {
			return err
		}
	}
	info, err := Resolve()
	if err != nil {
		return err
	}
	b.info = info
	b.mode = req.Mode
	if _, err := b.sup.start(ctx, info.Path, req); err != nil {
		return err
	}
	if c, err := b.sup.rpc(); err == nil {
		if v := c.GetVersion(); v != nil && v.TargetGoVersion != "" {
			if parsed, ok := goversion.Parse(v.TargetGoVersion); ok {
				b.goVer = &parsed
			}
		}
	}
	return nil
}

// OptimisationsDisabled reports whether the target was rebuilt with the
// optimiser and inliner off. It matters to the agent: when true, the binary
// under the debugger is not the binary that ships.
func (b *Backend) OptimisationsDisabled(mode model.LaunchMode) bool {
	return mode == model.LaunchTest || mode == model.LaunchDebug
}

func (b *Backend) Stop(context.Context) error {
	b.sup.kill()
	return nil
}

func (b *Backend) SetBreakpoint(ctx context.Context, bp model.Breakpoint) (model.Breakpoint, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.Breakpoint{}, err
	}
	if bp.Kind != "" && bp.Kind != model.BreakLine {
		return model.Breakpoint{}, backend.Unsupported(Name, "breakpoint kind "+string(bp.Kind),
			"Delve supports 'line' breakpoints and 'watch' watchpoints; use set_watchpoint for the latter.")
	}

	req := &api.Breakpoint{
		Cond:      bp.Condition,
		HitCond:   bp.HitCondition,
		Variables: bp.Record,
	}
	// A tracepoint records and lets the process run on. This is what makes a
	// long trace cost one call instead of one call per iteration.
	if bp.Suspend == model.SuspendNone {
		req.Tracepoint = true
	}

	switch {
	case bp.Location.Symbol != "":
		locs, _, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, bp.Location.Symbol, false, nil)
		if err != nil {
			return model.Breakpoint{}, fmt.Errorf("could not resolve %q: %w", bp.Location.Symbol, err)
		}
		if len(locs) == 0 {
			return model.Breakpoint{}, fmt.Errorf("no location matches %q", bp.Location.Symbol)
		}
		req.Addr = locs[0].PC
	case bp.Location.File != "":
		req.File = b.pathMap.ToRuntime(bp.Location.File)
		req.Line = bp.Location.Line
	default:
		return model.Breakpoint{}, fmt.Errorf("a breakpoint needs either a file and line, or a symbol")
	}

	created, err := c.CreateBreakpoint(req)
	if err != nil {
		return model.Breakpoint{}, fmt.Errorf("could not set a breakpoint: %w", err)
	}
	out := toBreakpoint(created)
	out.Location.File = b.pathMap.ToAgent(out.Location.File)
	return out, nil
}

func (b *Backend) ListBreakpoints(context.Context) ([]model.Breakpoint, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return nil, err
	}
	bps, err := c.ListBreakpoints(false)
	if err != nil {
		return nil, err
	}
	out := make([]model.Breakpoint, 0, len(bps))
	for _, bp := range bps {
		// Delve's internal breakpoints have negative IDs (unrecovered-panic and
		// friends). They are real, but they are not the agent's, and listing
		// them as if they were invites an agent to delete them.
		if bp.ID < 0 {
			continue
		}
		m := toBreakpoint(bp)
		m.Location.File = b.pathMap.ToAgent(m.Location.File)
		out = append(out, m)
	}
	return out, nil
}

func (b *Backend) RemoveBreakpoint(_ context.Context, id string) error {
	c, err := b.sup.rpc()
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("breakpoint id %q is not valid", id)
	}
	if _, err := c.ClearBreakpoint(n); err != nil {
		return fmt.Errorf("breakpoint %s could not be removed: %w", id, err)
	}
	return nil
}

func (b *Backend) Resume(context.Context) error {
	c, err := b.sup.rpc()
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.continueCh != nil {
		return fmt.Errorf("the target is already running; wait for it to stop first")
	}
	b.curFrame = 0
	b.continueCh = c.Continue()
	return nil
}

func (b *Backend) WaitForStop(ctx context.Context, timeout time.Duration) (model.StopEvent, error) {
	b.mu.Lock()
	ch := b.continueCh
	b.mu.Unlock()

	if ch == nil {
		// Not running. Report where we already are rather than making the agent
		// guess whether it forgot to resume.
		return b.currentStop()
	}

	// Delve keeps the channel open and keeps streaming while every stop is a
	// tracepoint, closing it only at a real one. Treating the first state as
	// the answer would turn a tracepoint into a spurious pause and strand the
	// channel, so tracepoint states are skipped here; their recorded values are
	// drained separately.
	deadline := time.After(timeout)
	for {
		select {
		case state, ok := <-ch:
			if !ok {
				b.clearContinue()
				return b.currentStop()
			}
			if isTracepointOnly(state) {
				continue
			}
			b.clearContinue()
			return b.stopFromState(state)
		case <-deadline:
			return model.StopEvent{State: model.StateRunning, Reason: model.StopUnknown,
				Message: fmt.Sprintf("still running after %s", timeout)}, nil
		case <-ctx.Done():
			return model.StopEvent{}, ctx.Err()
		}
	}
}

func (b *Backend) clearContinue() {
	b.mu.Lock()
	b.continueCh = nil
	b.mu.Unlock()
}

// isTracepointOnly reports a stop that Delve will resume from by itself: every
// thread that hit something hit a tracepoint. This mirrors the condition
// Delve's own client uses to decide whether to keep the channel open.
func isTracepointOnly(state *api.DebuggerState) bool {
	if state == nil || state.Exited {
		return false
	}
	hitSomething := false
	for i := range state.Threads {
		bp := state.Threads[i].Breakpoint
		if bp == nil {
			continue
		}
		hitSomething = true
		if !bp.Tracepoint && !bp.TraceReturn {
			return false
		}
	}
	return hitSomething
}

// Status answers "where are we" without waiting. It is the tool an agent
// reaches for after wandering off to read source: the pause is still there, and
// re-deriving it should not need another resume.
func (b *Backend) Status(context.Context) (model.StopEvent, error) {
	return b.currentStop()
}

func (b *Backend) Output(_ context.Context, since, limit int) (model.OutputPage, error) {
	return b.sup.output.page(since, limit), nil
}

func (b *Backend) currentStop() (model.StopEvent, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	state, err := c.GetState()
	if err != nil {
		return model.StopEvent{}, err
	}
	return b.stopFromState(state)
}

// stopFromState turns Delve's state into the whole answer an agent needs: where
// it stopped, why, what the stack was and what the variables were. Returning
// all of it here is the single biggest lever on how many round trips a
// debugging session costs.
func (b *Backend) stopFromState(state *api.DebuggerState) (model.StopEvent, error) {
	if state == nil {
		return model.StopEvent{State: model.StateExited, Reason: model.StopExited,
			Message: "the debugger reported no state"}, nil
	}
	if state.Exited {
		code := state.ExitStatus
		return model.StopEvent{State: model.StateExited, Reason: model.StopExited, ExitStatus: &code,
			Message: fmt.Sprintf("the process exited with status %d", code)}, nil
	}

	ev := model.StopEvent{State: model.StatePaused, Reason: model.StopStep}
	th := state.CurrentThread
	if th != nil && th.Breakpoint != nil {
		ev.Reason = model.StopBreakpoint
		ev.BreakpointID = strconv.Itoa(th.Breakpoint.ID)
		// Delve raises its own breakpoint for an unrecovered panic. Reporting it
		// as a plain breakpoint would hide the most important fact available.
		if th.Breakpoint.Name == "unrecovered-panic" {
			ev.Reason = model.StopPanic
			ev.BreakpointID = ""
		}
	}

	c, err := b.sup.rpc()
	if err != nil {
		return ev, nil
	}

	goroutineID := int64(-1)
	if state.SelectedGoroutine != nil {
		goroutineID = state.SelectedGoroutine.ID
		u := toExecUnit(state.SelectedGoroutine, true, b.goVer)
		u.State = model.UnitPaused
		ev.Unit = &u
	}

	budget := model.DefaultValueBudget()
	cfg := loadConfig(budget)
	if frames, err := c.Stacktrace(goroutineID, 32, 0, 0, &cfg); err == nil {
		ev.Frames = toFrames(frames)
		for i := range ev.Frames {
			ev.Frames[i].File = b.pathMap.ToAgent(ev.Frames[i].File)
		}
	}
	ev.Variables, _ = b.variablesAt(c, goroutineID, 0, budget)

	// The last few lines the program printed, so the commonest follow-up
	// question needs no second call.
	ev.RecentOutput = b.sup.output.tail(10)

	if th != nil && th.File != "" {
		ev.Source = readSourceSpan(th.File, th.Line, 4)
		if ev.Source != nil {
			ev.Source.File = b.pathMap.ToAgent(ev.Source.File)
		}
	}
	return ev, nil
}

func (b *Backend) variablesAt(c rpcClient, goroutineID int64, frame int, budget model.ValueBudget) ([]model.Variable, error) {
	scope := api.EvalScope{GoroutineID: goroutineID, Frame: frame}
	cfg := loadConfig(budget)

	// Arguments first: when an agent asks "why is this function producing the
	// wrong answer", what went in is almost always the more useful half.
	var out []model.Variable
	args, err := c.ListFunctionArgs(scope, cfg)
	if err != nil {
		return nil, err
	}
	for _, v := range args {
		flattenVariable(v, v.Name, budget, &out)
	}
	locals, err := c.ListLocalVariables(scope, cfg)
	if err != nil {
		return out, nil
	}
	for _, v := range locals {
		flattenVariable(v, v.Name, budget, &out)
	}
	return out, nil
}

func (b *Backend) Variables(_ context.Context, frameIndex int, budget model.ValueBudget) ([]model.Variable, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return nil, err
	}
	if frameIndex < 0 {
		frameIndex = b.curFrame
	}
	return b.variablesAt(c, -1, frameIndex, budget)
}

func (b *Backend) Evaluate(_ context.Context, frameIndex int, expr string, budget model.ValueBudget) (model.Variable, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.Variable{}, err
	}
	if frameIndex < 0 {
		frameIndex = b.curFrame
	}
	v, err := c.EvalVariable(api.EvalScope{GoroutineID: -1, Frame: frameIndex}, expr, loadConfig(budget))
	if err != nil {
		return model.Variable{}, fmt.Errorf("could not evaluate %q: %w", expr, err)
	}
	var flat []model.Variable
	flattenVariable(*v, expr, budget, &flat)
	if len(flat) == 0 {
		return model.Variable{}, fmt.Errorf("could not evaluate %q", expr)
	}
	// The head of the flattened list is the value itself; its fields, if any,
	// are reachable by asking for "<expr>.field".
	return flat[0], nil
}

func (b *Backend) Stack(_ context.Context, unitID string, maxFrames int) ([]model.Frame, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return nil, err
	}
	goroutineID := int64(-1)
	if unitID != "" {
		if n, err := strconv.ParseInt(unitID, 10, 64); err == nil {
			goroutineID = n
		}
	}
	if maxFrames <= 0 {
		maxFrames = 32
	}
	cfg := loadConfig(model.DefaultValueBudget())
	frames, err := c.Stacktrace(goroutineID, maxFrames, 0, 0, &cfg)
	if err != nil {
		return nil, err
	}
	out := toFrames(frames)
	for i := range out {
		out[i].File = b.pathMap.ToAgent(out[i].File)
	}
	return out, nil
}

// SelectFrame makes a frame the default for later calls.
func (b *Backend) SelectFrame(ctx context.Context, index int) (model.Frame, error) {
	frames, err := b.Stack(ctx, "", 64)
	if err != nil {
		return model.Frame{}, err
	}
	if index < 0 || index >= len(frames) {
		return model.Frame{}, fmt.Errorf("frame %d does not exist; the stack has %d frames", index, len(frames))
	}
	b.mu.Lock()
	b.curFrame = index
	b.mu.Unlock()
	return frames[index], nil
}

func (b *Backend) ExecUnits(_ context.Context, limit int) ([]model.ExecUnit, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		// A Go program can have thousands of goroutines. An unbounded listing is
		// not a listing, it is a context-window incident.
		limit = 50
	}
	gs, _, err := c.ListGoroutines(0, limit)
	if err != nil {
		return nil, err
	}
	var currentID int64 = -1
	if st, err := c.GetState(); err == nil && st.SelectedGoroutine != nil {
		currentID = st.SelectedGoroutine.ID
	}
	out := make([]model.ExecUnit, 0, len(gs))
	for _, g := range gs {
		u := toExecUnit(g, g.ID == currentID, b.goVer)
		if u.TopFrame != nil {
			u.TopFrame.File = b.pathMap.ToAgent(u.TopFrame.File)
		}
		out = append(out, u)
	}
	return out, nil
}

func (b *Backend) Source(_ context.Context, file string, line, contextLines int) (*model.SourceSpan, error) {
	if contextLines <= 0 {
		contextLines = 5
	}
	span := readSourceSpan(b.pathMap.ToRuntime(file), line, contextLines)
	if span == nil {
		return nil, fmt.Errorf("could not read source at %s:%d", file, line)
	}
	span.File = file
	return span, nil
}

// rpcClient is the slice of Delve's client this package actually uses, named so
// the dependency is visible and so tests can stand in for it.
type rpcClient interface {
	ListFunctionArgs(scope api.EvalScope, cfg api.LoadConfig) ([]api.Variable, error)
	ListLocalVariables(scope api.EvalScope, cfg api.LoadConfig) ([]api.Variable, error)
}

var _ backend.Backend = (*Backend)(nil)
