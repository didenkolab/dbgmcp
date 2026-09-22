package delve

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/go-delve/delve/service/api"
)

// maxStepsIntoScope bounds the walk from a function's entry to the point where
// its local actually exists.
const maxStepsIntoScope = 12

// ExplainValue answers "why is this value wrong" by watching it change.
//
// An agent can technically assemble this from primitives -- break, step, watch,
// resume, read, repeat -- but not in a sane number of round trips, and not
// without carrying the running story across all of them. It is also the
// question people ask a debugger most often, which is why it is worth one tool
// rather than a documented recipe.
func (b *Backend) ExplainValue(ctx context.Context, req model.ExplainRequest, timeout time.Duration) (model.ValueHistory, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.ValueHistory{}, err
	}
	if req.Expression == "" {
		return model.ValueHistory{}, fmt.Errorf("explain_value needs an expression to follow")
	}
	if req.Scope.IsZero() {
		return model.ValueHistory{}, fmt.Errorf("explain_value needs a scope: the symbol, or file and line, where %q lives", req.Expression)
	}

	history := model.ValueHistory{Expression: req.Expression, Scope: req.Scope, Writes: []model.ValueWrite{}}
	deadline := time.Now().Add(timeout)

	// 1. Get into the frame that holds the value.
	entry, err := b.SetBreakpoint(ctx, model.Breakpoint{
		Location: req.Scope, Condition: req.Condition,
	})
	if err != nil {
		return history, err
	}
	entryID, _ := strconv.Atoi(entry.ID)

	if err := b.Resume(ctx); err != nil {
		_, _ = c.ClearBreakpoint(entryID)
		return history, err
	}
	stop, err := b.WaitForStop(ctx, time.Until(deadline))
	// The entry breakpoint has done its job; leaving it armed would stop every
	// later call and turn the history into someone else's.
	_, _ = c.ClearBreakpoint(entryID)
	if err != nil {
		return history, err
	}
	if stop.State != model.StatePaused {
		history.Status = model.ExplainExited
		history.Message = fmt.Sprintf("%s was never reached, so there is no history to report.", describeLocation(req.Scope))
		return history, nil
	}

	// 2. A local does not exist at its function's entry. Step until it does --
	// the same thing a person does, and the reason the failure below can say
	// which of the two problems it was.
	inScope := false
	for i := 0; i < maxStepsIntoScope; i++ {
		if _, evalErr := b.Evaluate(ctx, 0, req.Expression, model.ValueBudget{}); evalErr == nil {
			inScope = true
			break
		}
		if _, stepErr := b.Step(ctx, model.StepOver); stepErr != nil {
			break
		}
	}
	if !inScope {
		return history, fmt.Errorf(
			"%q never came into scope within %d steps of %s. Either the name is wrong there, or it is declared further in than this tool walks",
			req.Expression, maxStepsIntoScope, describeLocation(req.Scope))
	}

	current := b.read(ctx, req.Expression)
	history.Initial, history.Final = current, current

	// 3. Watch it. Hardware watchpoints are scoped to this frame, which is what
	// makes "the frame returned" a real ending rather than a timeout.
	wp, err := b.SetWatchpoint(ctx, 0, req.Expression, model.WatchWrite)
	if err != nil {
		return history, err
	}
	wpID, _ := strconv.Atoi(wp.ID)
	defer func() { _, _ = c.ClearBreakpoint(wpID) }()

	// 4. Collect the writes.
	for {
		if time.Now().After(deadline) {
			history.Status = model.ExplainTimeout
			break
		}
		if req.MaxWrites > 0 && len(history.Writes) >= req.MaxWrites {
			history.Status = model.ExplainBudgetReached
			break
		}
		if err := b.Resume(ctx); err != nil {
			history.Status = model.ExplainTimeout
			history.Message = err.Error()
			break
		}
		ev, err := b.WaitForStop(ctx, time.Until(deadline))
		if err != nil {
			history.Status = model.ExplainTimeout
			break
		}
		if ev.State == model.StateExited {
			history.Status = model.ExplainExited
			break
		}
		if b.watchWentOutOfScope(c, wpID) {
			history.Status = model.ExplainFrameReturned
			break
		}

		next := b.read(ctx, req.Expression)
		write := model.ValueWrite{
			Seq: len(history.Writes) + 1, From: current, To: next,
			Frames: trimFrames(ev.Frames, 4),
		}
		if len(ev.Frames) > 0 {
			write.File, write.Line, write.Function = ev.Frames[0].File, ev.Frames[0].Line, ev.Frames[0].Function
		}
		if ev.Unit != nil {
			write.UnitID = ev.Unit.ID
		}
		history.Writes = append(history.Writes, write)
		current = next
		history.Final = next
	}

	history.Message = summarise(history)
	return history, nil
}

// watchWentOutOfScope asks Delve whether the watchpoint still exists. When the
// frame holding the value returns, Delve retires the watchpoint -- which is the
// precise end of the value's life, not a guess based on a timeout.
func (b *Backend) watchWentOutOfScope(c interface {
	GetBreakpoint(int) (*api.Breakpoint, error)
}, wpID int) bool {
	_, err := c.GetBreakpoint(wpID)
	return err != nil
}

func (b *Backend) read(ctx context.Context, expr string) string {
	v, err := b.Evaluate(ctx, 0, expr, model.ValueBudget{})
	if err != nil {
		return "<out of scope>"
	}
	return v.Value
}

func trimFrames(frames []model.Frame, n int) []model.Frame {
	if len(frames) <= n {
		return frames
	}
	return frames[:n]
}

func describeLocation(l model.Location) string {
	if l.Symbol != "" {
		return l.Symbol
	}
	return fmt.Sprintf("%s:%d", l.File, l.Line)
}

func summarise(h model.ValueHistory) string {
	switch {
	case len(h.Writes) == 0 && h.Status == model.ExplainFrameReturned:
		return fmt.Sprintf("%s was never written after it came into scope; it stayed %s. Whatever set it did so before this point.",
			h.Expression, h.Initial)
	case h.Status == model.ExplainFrameReturned:
		return fmt.Sprintf("%s went from %s to %s in %d writes, and the history is complete: the frame holding it returned.",
			h.Expression, h.Initial, h.Final, len(h.Writes))
	case h.Status == model.ExplainBudgetReached:
		return fmt.Sprintf("%s reached %s after %d writes, which was the requested budget; there may be more.",
			h.Expression, h.Final, len(h.Writes))
	case h.Status == model.ExplainExited:
		return fmt.Sprintf("The process exited with %s at %s.", h.Expression, h.Final)
	default:
		return fmt.Sprintf("Ran out of time with %s at %s after %d writes.", h.Expression, h.Final, len(h.Writes))
	}
}
