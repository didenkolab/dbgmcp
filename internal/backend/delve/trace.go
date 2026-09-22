package delve

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/go-delve/delve/service/api"
)

// Trace records expressions at a set of probes and returns the whole transcript
// from one call.
//
// Delve evaluates the expressions itself at every hit and resumes on its own,
// so the agent is not in the loop and the program is not waiting on it. The
// same question asked with set_breakpoint, resume and wait costs a round trip
// per stop, and forces the agent to carry the running story across all of them.
//
// Every breakpoint this creates is removed before returning, including on
// failure: a tool that leaves state behind for the caller to clean up will
// eventually be called by a caller who forgets.
func (b *Backend) Trace(ctx context.Context, probes []model.Probe, timeout time.Duration) (model.Transcript, error) {
	c, err := b.sup.rpc()
	if err != nil {
		return model.Transcript{}, err
	}
	if len(probes) == 0 {
		return model.Transcript{}, fmt.Errorf("a trace needs at least one probe")
	}

	byBreakpointID := map[int]int{}
	var created []int
	defer func() {
		for _, id := range created {
			_, _ = c.ClearBreakpoint(id)
		}
	}()

	for i, p := range probes {
		req := &api.Breakpoint{Tracepoint: true, Variables: p.Record}
		switch {
		case p.Location.Symbol != "":
			locs, _, err := c.FindLocation(api.EvalScope{GoroutineID: -1}, p.Location.Symbol, false, nil)
			if err != nil || len(locs) == 0 {
				return model.Transcript{}, fmt.Errorf("probe %d: could not resolve %q", i, p.Location.Symbol)
			}
			req.Addr = locs[0].PC
		case p.Location.File != "":
			req.File = b.pathMap.ToRuntime(p.Location.File)
			req.Line = p.Location.Line
		default:
			return model.Transcript{}, fmt.Errorf("probe %d needs either a symbol, or a file and line", i)
		}

		bp, err := c.CreateBreakpoint(req)
		if err != nil {
			return model.Transcript{}, probePlacementError(c, i, p, err)
		}
		created = append(created, bp.ID)
		byBreakpointID[bp.ID] = i
	}

	b.mu.Lock()
	if b.continueCh != nil {
		b.mu.Unlock()
		return model.Transcript{}, fmt.Errorf("the target is already running; wait for it to stop before tracing")
	}
	ch := c.Continue()
	b.continueCh = ch
	b.mu.Unlock()
	defer b.clearContinue()

	out := model.Transcript{PerturbsTiming: false, Hits: []model.TraceHit{}}
	counts := make([]int, len(probes))
	deadline := time.After(timeout)

	for {
		select {
		case state, open := <-ch:
			if !open {
				out.Status = model.TraceFinished
				return b.finishTranscript(out, probes, counts), nil
			}
			if state.Exited {
				out.Status = model.TraceFinished
				return b.finishTranscript(out, probes, counts), nil
			}
			if !isTracepointOnly(state) {
				out.Status = model.TraceStopped
				out.Message = "The target stopped at something that is not a probe, so the trace ends here."
				return b.finishTranscript(out, probes, counts), nil
			}

			b.collectHits(state, probes, byBreakpointID, counts, &out)
			if budgetsMet(probes, counts) {
				// Every probe has what it was asked for. Halting now is the
				// difference between a bounded trace and waiting out a server
				// that never exits.
				_, _ = c.Halt()
				out.Status = model.TraceCompleted
				return b.finishTranscript(out, probes, counts), nil
			}

		case <-deadline:
			_, _ = c.Halt()
			out.Status = model.TraceTimeout
			out.Message = fmt.Sprintf("The trace stopped after %s with the transcript collected so far.", timeout)
			return b.finishTranscript(out, probes, counts), nil

		case <-ctx.Done():
			_, _ = c.Halt()
			return out, ctx.Err()
		}
	}
}

// probePlacementError turns Delve's refusal into something an agent can act on.
//
// The common case is a collision with a breakpoint the agent set earlier to look
// around, and Delve reports it as "Breakpoint exists at <file>:<line> at <addr>"
// -- true, but it names neither the breakpoint nor the way out. Taking the
// existing breakpoint over silently would be worse: it may carry a condition
// somebody meant to keep.
func probePlacementError(c interface {
	ListBreakpoints(bool) ([]*api.Breakpoint, error)
}, index int, p model.Probe, cause error) error {
	where := p.Location.Symbol
	if where == "" {
		where = fmt.Sprintf("%s:%d", p.Location.File, p.Location.Line)
	}
	if !strings.Contains(cause.Error(), "Breakpoint exists") {
		return fmt.Errorf("probe %d (%s): could not place a probe: %w", index, where, cause)
	}

	detail := ""
	if existing, listErr := c.ListBreakpoints(false); listErr == nil {
		for _, bp := range existing {
			if bp.ID > 0 && strings.Contains(cause.Error(), fmt.Sprintf("%s:%d", bp.File, bp.Line)) {
				detail = fmt.Sprintf(" It collides with breakpoint %d at %s:%d.", bp.ID, bp.File, bp.Line)
				break
			}
		}
	}
	return fmt.Errorf(
		"probe %d (%s) cannot be placed: a breakpoint is already set there.%s "+
			"Remove it with remove_breakpoint first, or trace a different location. "+
			"It is not taken over automatically because it may carry a condition that was meant to stay",
		index, where, detail)
}

func (b *Backend) collectHits(state *api.DebuggerState, probes []model.Probe, byID map[int]int, counts []int, out *model.Transcript) {
	for i := range state.Threads {
		th := state.Threads[i]
		if th.Breakpoint == nil {
			continue
		}
		probeIdx, ok := byID[th.Breakpoint.ID]
		if !ok {
			continue
		}
		if p := probes[probeIdx]; p.MaxHits > 0 && counts[probeIdx] >= p.MaxHits {
			continue
		}
		counts[probeIdx]++

		hit := model.TraceHit{
			Probe: probeIdx, Hit: counts[probeIdx],
			File: b.pathMap.ToAgent(th.File), Line: th.Line,
			UnitID: strconv.FormatInt(th.GoroutineID, 10),
			Values: map[string]string{},
		}
		if th.Function != nil {
			hit.Function = th.Function.Name()
		}
		// Delve returns the recorded values in the order they were requested,
		// so the expression is recoverable and the transcript can be read
		// without consulting the request.
		if th.BreakpointInfo != nil {
			exprs := probes[probeIdx].Record
			for j, v := range th.BreakpointInfo.Variables {
				name := v.Name
				if j < len(exprs) {
					name = exprs[j]
				}
				hit.Values[name] = presentValue(v)
			}
		}
		out.Hits = append(out.Hits, hit)
	}
}

func budgetsMet(probes []model.Probe, counts []int) bool {
	for i, p := range probes {
		if p.MaxHits <= 0 {
			return false // an unbounded probe is never satisfied on its own
		}
		if counts[i] < p.MaxHits {
			return false
		}
	}
	return true
}

func (b *Backend) finishTranscript(out model.Transcript, probes []model.Probe, counts []int) model.Transcript {
	for i, n := range counts {
		if n == 0 {
			where := probes[i].Location.Symbol
			if where == "" {
				where = fmt.Sprintf("%s:%d", probes[i].Location.File, probes[i].Location.Line)
			}
			out.ProbesNeverHit = append(out.ProbesNeverHit, where)
		}
	}
	if out.Message == "" {
		out.Message = fmt.Sprintf("Recorded %d hits across %d probes.", len(out.Hits), len(probes))
	}
	if len(out.ProbesNeverHit) > 0 {
		out.Message += " Some probes never fired, so an empty transcript there means a misplaced probe rather than no data."
	}
	return out
}
