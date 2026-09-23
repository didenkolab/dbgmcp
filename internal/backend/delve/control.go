package delve

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/go-delve/delve/service/api"
)

// Pause interrupts a running target. Delve's Halt is synchronous and returns
// the state it stopped in, so the agent learns where it landed immediately.
func (b *Backend) Pause(context.Context) (model.StopEvent, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	state, err := c.Halt()
	if err != nil {
		return model.StopEvent{}, fmt.Errorf("could not pause the target: %w", err)
	}
	// A pending Continue is now finished; forgetting this would make the next
	// Resume refuse on the grounds that the target is still running.
	b.mu.Lock()
	b.continueCh = nil
	b.mu.Unlock()

	ev, err := b.stopFromState(state)
	if err == nil && ev.State == model.StatePaused {
		ev.Reason = model.StopManual
	}
	return ev, err
}

func (b *Backend) Step(_ context.Context, kind model.StepKind) (model.StopEvent, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	b.mu.Lock()
	running := b.continueCh != nil
	b.mu.Unlock()
	if running {
		return model.StopEvent{}, fmt.Errorf("Session must be paused to step")
	}

	var state *api.DebuggerState
	switch kind {
	case model.StepOver:
		state, err = c.Next()
	case model.StepInto:
		state, err = c.Step()
	case model.StepOut:
		state, err = c.StepOut()
	default:
		return model.StopEvent{}, fmt.Errorf("unknown step kind %q (expected over, into or out)", kind)
	}
	if err != nil {
		return model.StopEvent{}, fmt.Errorf("could not step %s: %w", kind, err)
	}
	b.curFrame = 0
	return b.stopFromState(state)
}

// RunToLine continues until a location is reached and removes the breakpoint it
// used, including when the run fails or the process exits first. An agent that
// has to clean up after a tool will eventually forget to.
func (b *Backend) RunToLine(ctx context.Context, file string, line int, timeout time.Duration) (model.StopEvent, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	temp, err := c.CreateBreakpoint(&api.Breakpoint{File: b.pathMap.ToRuntime(file), Line: line})
	if err != nil {
		return model.StopEvent{}, fmt.Errorf("Cannot run to %s:%d (not a valid breakpoint location): %w", file, line, err)
	}
	defer func() { _, _ = c.ClearBreakpoint(temp.ID) }()

	if err := b.Resume(ctx); err != nil {
		return model.StopEvent{}, err
	}
	return b.WaitForStop(ctx, timeout)
}

// SetWatchpoint stops the program when a value is accessed.
//
// These are hardware watchpoints: there are four of them, and each is bound to
// the stack frame it was set in, so one goes out of scope when its frame
// returns. The error says so, because "could not set watchpoint" with no reason
// leads an agent to retry the same call.
func (b *Backend) SetWatchpoint(_ context.Context, frameIndex int, expr string, mode model.WatchMode) (model.Breakpoint, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.Breakpoint{}, err
	}
	var wtype api.WatchType
	switch mode {
	case model.WatchRead:
		wtype = api.WatchRead
	case model.WatchReadWrite:
		wtype = api.WatchRead | api.WatchWrite
	case model.WatchWrite, "":
		wtype = api.WatchWrite
	default:
		return model.Breakpoint{}, fmt.Errorf("unknown watch mode %q (expected write, read or read_write)", mode)
	}
	if frameIndex < 0 {
		frameIndex = b.curFrame
	}

	bp, err := c.CreateWatchpoint(api.EvalScope{GoroutineID: -1, Frame: frameIndex}, expr, wtype)
	if err != nil {
		return model.Breakpoint{}, fmt.Errorf("could not watch %q: %w.%s", expr, err, watchpointAdvice(err))
	}
	out := toBreakpoint(bp)
	out.Kind = model.BreakWatch
	out.Selector = expr
	out.Location.File = b.pathMap.ToAgent(out.Location.File)
	return out, nil
}

// watchpointAdvice turns Delve's terse refusals into the next thing to try.
//
// Each branch names the reason that actually applies. The generic fallback used to
// answer every refusal, so a value too large to watch was met with advice about
// having too many watchpoints -- sending the reader to count watchpoints instead of
// looking at the type.
func watchpointAdvice(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "could not find symbol value"):
		// Reads like a missing variable and almost never is: an agent told only
		// Delve's wording retries the identical call.
		return " A watchpoint needs the variable to exist already: stopping at a function's entry is " +
			"before its locals are declared. Step past the declaration, then set the watchpoint."
	case strings.Contains(text, "can not watch variable of type"):
		// Delve refuses anything wider than a pointer, because a hardware
		// watchpoint covers one machine word.
		return " A watchpoint covers at most one machine word (8 bytes on a 64-bit target), so a string, " +
			"slice, interface, map or struct cannot be watched whole. Watch a field inside it that fits -- " +
			"a length, an id, a pointer -- or record the value with trace_execution at the lines that assign it."
	case strings.Contains(text, "stack allocated variable for reads"):
		// Checked before the broader "can not watch" below, which this contains.
		return " Reads of a stack variable cannot be watched; watch writes instead, which is usually the " +
			"question anyway: what changed this value."
	case strings.Contains(text, "can not watch"):
		// No address of its own: a register-resident or synthesised value.
		return " That expression has no address of its own to watch -- the compiler keeps some values in " +
			"registers, and a computed expression never had one. Watch a plain variable, or record it with " +
			"trace_execution at the lines that assign it."
	}
	return " Watchpoints are a hardware feature: at most four can exist at once, and each is bound to " +
		"the stack frame it was set in, so it disappears when that frame returns."
}

func (b *Backend) SetVariable(_ context.Context, frameIndex int, name, value string) error {
	c, err := b.sup.rpc()
	if err != nil {
		return err
	}
	if frameIndex < 0 {
		frameIndex = b.curFrame
	}
	if err := c.SetVariable(api.EvalScope{GoroutineID: -1, Frame: frameIndex}, name, value); err != nil {
		return fmt.Errorf("could not set %s to %s: %w", name, value, err)
	}
	return nil
}

// tracebackAncestorsEnv is what the debuggee must be started with for goroutine
// ancestry to exist at all. It is not set by default because it makes every
// goroutine creation record a stack.
const tracebackAncestorsEnv = "GODEBUG=tracebackancestors=10"

func (b *Backend) Ancestors(_ context.Context, unitID string, depth int) (model.Ancestry, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.Ancestry{}, err
	}
	goroutineID := int64(-1)
	if unitID != "" {
		if n, parseErr := strconv.ParseInt(unitID, 10, 64); parseErr == nil {
			goroutineID = n
		}
	}
	if goroutineID < 0 {
		st, stErr := c.GetState()
		if stErr != nil || st.SelectedGoroutine == nil {
			return model.Ancestry{}, fmt.Errorf("Session must be paused to read goroutine ancestry")
		}
		goroutineID = st.SelectedGoroutine.ID
	}
	if depth <= 0 {
		depth = 16
	}

	ancestors, err := c.Ancestors(goroutineID, 10, depth)
	if err != nil {
		return model.Ancestry{}, fmt.Errorf(
			"could not read ancestry for goroutine %d: %w. Ancestry exists only when the target runs with %s, "+
				"which this session does not set by default because it slows every goroutine creation",
			goroutineID, err, tracebackAncestorsEnv)
	}

	out := model.Ancestry{UnitID: strconv.FormatInt(goroutineID, 10)}
	for _, a := range ancestors {
		anc := model.Ancestor{UnitID: strconv.FormatInt(a.ID, 10), Unreadable: a.Unreadable}
		anc.Frames = toFrames(a.Stack)
		for i := range anc.Frames {
			anc.Frames[i].File = b.pathMap.ToAgent(anc.Frames[i].File)
		}
		out.Chain = append(out.Chain, anc)
	}
	if len(out.Chain) == 0 {
		out.Note = "No ancestry was recorded. Start the session with env " + tracebackAncestorsEnv + " to collect it."
	}
	return out, nil
}

var _ backend.Backend = (*Backend)(nil)
