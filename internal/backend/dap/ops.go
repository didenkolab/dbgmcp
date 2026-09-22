package dap

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// tracePrefix marks output this server caused, so a debuggee that prints
// something similar is not mistaken for a trace hit.
const tracePrefix = "[dbgmcp] "

// ---------- breakpoints ----------

func (b *Backend) SetBreakpoint(ctx context.Context, bp model.Breakpoint) (model.Breakpoint, error) {
	if bp.Kind != "" && bp.Kind != model.BreakLine {
		return model.Breakpoint{}, backend.Unsupported(b.Name(), "breakpoint kind "+string(bp.Kind),
			"This adapter takes line breakpoints; check describe_backend for what else it declares.")
	}
	if bp.Location.Symbol != "" && bp.Location.File == "" {
		if !b.Capabilities().BreakpointBySymbol {
			return model.Breakpoint{}, backend.Unsupported(b.Name(), "breakpoint_by_symbol",
				"Give a file and a line instead; this adapter cannot resolve a function name.")
		}
		return b.setFunctionBreakpoint(ctx, bp)
	}
	if bp.Location.File == "" || bp.Location.Line <= 0 {
		return model.Breakpoint{}, fmt.Errorf("a breakpoint needs a file and a line")
	}

	file := b.pathMap.ToRuntime(bp.Location.File)
	spec := sourceBreakpoint{Line: bp.Location.Line, Condition: bp.Condition, HitCondition: bp.HitCondition}
	// A logpoint is how this backend records without the agent in the loop: the
	// adapter formats the expressions and carries on, and the values arrive as
	// output events.
	if bp.Suspend == model.SuspendNone && len(bp.Record) > 0 {
		spec.LogMessage = logMessageFor(bp.Record)
	}

	b.mu.Lock()
	b.nextLocalID++
	local := b.nextLocalID
	b.breakpoints[file] = append(b.breakpoints[file], trackedBreakpoint{localID: local, spec: spec, record: bp.Record})
	index := len(b.breakpoints[file]) - 1
	b.mu.Unlock()

	results, err := b.syncBreakpoints(ctx, file)
	if err != nil {
		b.forget(file, local)
		return model.Breakpoint{}, err
	}
	if index >= len(results) {
		b.forget(file, local)
		return model.Breakpoint{}, fmt.Errorf("the adapter did not report a breakpoint for %s:%d", bp.Location.File, bp.Location.Line)
	}
	got := results[index]
	if !got.Verified {
		b.forget(file, local)
		_, _ = b.syncBreakpoints(ctx, file)
		reason := got.Message
		if reason == "" {
			reason = "the adapter could not bind it, which usually means the line holds no executable code"
		}
		return model.Breakpoint{}, fmt.Errorf("Cannot set breakpoint at %s:%d (%s)", bp.Location.File, bp.Location.Line, reason)
	}

	out := bp
	out.ID = strconv.Itoa(local)
	out.Kind = model.BreakLine
	out.Enabled = true
	out.Location = model.Location{File: b.pathMap.ToAgent(file), Line: got.Line, Symbol: bp.Location.Symbol}
	if out.Suspend == "" {
		out.Suspend = model.SuspendAll
	}
	return out, nil
}

// logMessageFor builds a logpoint template whose output can be read back as
// values. Each expression is keyed by its own text, so a transcript is legible
// without consulting the request that produced it.
func logMessageFor(record []string) string {
	parts := make([]string, 0, len(record))
	for _, expr := range record {
		parts = append(parts, expr+"={"+expr+"}")
	}
	return tracePrefix + strings.Join(parts, " | ")
}

func (b *Backend) setFunctionBreakpoint(ctx context.Context, bp model.Breakpoint) (model.Breakpoint, error) {
	cl, err := b.rpc()
	if err != nil {
		return model.Breakpoint{}, err
	}
	body, err := cl.send(ctx, "setFunctionBreakpoints", map[string]any{
		"breakpoints": []map[string]any{{"name": bp.Location.Symbol, "condition": bp.Condition}},
	}, requestTimeout)
	if err != nil {
		return model.Breakpoint{}, err
	}
	var resp setBreakpointsResponse
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Breakpoints) == 0 {
		return model.Breakpoint{}, fmt.Errorf("the adapter did not report a breakpoint for %q", bp.Location.Symbol)
	}
	got := resp.Breakpoints[0]
	if !got.Verified {
		return model.Breakpoint{}, fmt.Errorf("could not resolve %q to a function", bp.Location.Symbol)
	}
	out := bp
	out.ID = "fn:" + bp.Location.Symbol
	out.Kind = model.BreakLine
	out.Enabled = true
	out.Location = model.Location{File: b.pathMap.ToAgent(got.Source.Path), Line: got.Line, Symbol: bp.Location.Symbol}
	out.Suspend = model.SuspendAll
	return out, nil
}

func (b *Backend) forget(file string, localID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.breakpoints[file][:0]
	for _, t := range b.breakpoints[file] {
		if t.localID != localID {
			kept = append(kept, t)
		}
	}
	b.breakpoints[file] = kept
}

func (b *Backend) ListBreakpoints(context.Context) ([]model.Breakpoint, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []model.Breakpoint{}
	for file, tracked := range b.breakpoints {
		for _, t := range tracked {
			suspend := model.SuspendAll
			if t.spec.LogMessage != "" {
				suspend = model.SuspendNone
			}
			out = append(out, model.Breakpoint{
				ID:           strconv.Itoa(t.localID),
				Kind:         model.BreakLine,
				Location:     model.Location{File: b.pathMap.ToAgent(file), Line: t.spec.Line},
				Enabled:      t.verified,
				Condition:    t.spec.Condition,
				HitCondition: t.spec.HitCondition,
				Suspend:      suspend,
				Record:       t.record,
			})
		}
	}
	return out, nil
}

func (b *Backend) RemoveBreakpoint(ctx context.Context, id string) error {
	local, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("breakpoint id %q is not valid", id)
	}
	b.mu.Lock()
	var file string
	for f, tracked := range b.breakpoints {
		for _, t := range tracked {
			if t.localID == local {
				file = f
			}
		}
	}
	b.mu.Unlock()
	if file == "" {
		return fmt.Errorf("breakpoint %s not found", id)
	}
	b.forget(file, local)
	_, err = b.syncBreakpoints(ctx, file)
	return err
}

func (b *Backend) SetWatchpoint(context.Context, int, string, model.WatchMode) (model.Breakpoint, error) {
	return model.Breakpoint{}, backend.Unsupported(b.Name(), "watchpoints",
		"Set a line breakpoint where the value is written, or use trace_execution to record it at those places.")
}

func (b *Backend) Ancestors(context.Context, string, int) (model.Ancestry, error) {
	return model.Ancestry{}, backend.Unsupported(b.Name(), "ancestry",
		"The protocol carries no record of which unit created another. Use get_stack_trace on the unit itself.")
}

// ---------- execution ----------

func (b *Backend) threadID() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentThread != 0 {
		return b.currentThread
	}
	return 1
}

func (b *Backend) Resume(ctx context.Context) error {
	cl, err := b.rpc()
	if err != nil {
		return err
	}
	_, err = cl.send(ctx, "continue", map[string]any{"threadId": b.threadID()}, requestTimeout)
	return err
}

func (b *Backend) Pause(ctx context.Context) (model.StopEvent, error) {
	cl, err := b.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	if _, err := cl.send(ctx, "pause", map[string]any{"threadId": b.threadID()}, requestTimeout); err != nil {
		return model.StopEvent{}, err
	}
	ev, err := b.WaitForStop(ctx, 15*time.Second)
	if err == nil && ev.State == model.StatePaused {
		ev.Reason = model.StopManual
	}
	return ev, err
}

func (b *Backend) Step(ctx context.Context, kind model.StepKind) (model.StopEvent, error) {
	cl, err := b.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	command := map[model.StepKind]string{
		model.StepOver: "next", model.StepInto: "stepIn", model.StepOut: "stepOut",
	}[kind]
	if command == "" {
		return model.StopEvent{}, fmt.Errorf("unknown step kind %q (expected over, into or out)", kind)
	}
	if _, err := cl.send(ctx, command, map[string]any{"threadId": b.threadID()}, requestTimeout); err != nil {
		return model.StopEvent{}, err
	}
	// DAP steps are asynchronous: the response acknowledges the request, and
	// where it landed arrives as an event. The agent is handed the landing spot
	// rather than the acknowledgement.
	return b.WaitForStop(ctx, 30*time.Second)
}

func (b *Backend) RunToLine(ctx context.Context, file string, line int, timeout time.Duration) (model.StopEvent, error) {
	temp, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: model.Location{File: file, Line: line}})
	if err != nil {
		return model.StopEvent{}, err
	}
	defer func() { _ = b.RemoveBreakpoint(ctx, temp.ID) }()
	if err := b.Resume(ctx); err != nil {
		return model.StopEvent{}, err
	}
	return b.WaitForStop(ctx, timeout)
}

func (b *Backend) WaitForStop(ctx context.Context, timeout time.Duration) (model.StopEvent, error) {
	cl, err := b.rpc()
	if err != nil {
		return model.StopEvent{}, err
	}
	select {
	case ev := <-cl.stopped:
		b.mu.Lock()
		b.currentThread, b.currentFrame = ev.ThreadID, 0
		b.mu.Unlock()
		return b.describeStop(ctx, ev)
	case <-cl.terminated:
		b.mu.Lock()
		b.exited = true
		b.mu.Unlock()
		return model.StopEvent{State: model.StateExited, Reason: model.StopExited, Message: "the process exited"}, nil
	case <-time.After(timeout):
		return model.StopEvent{State: model.StateRunning, Reason: model.StopUnknown,
			Message: fmt.Sprintf("still running after %s", timeout)}, nil
	case <-ctx.Done():
		return model.StopEvent{}, ctx.Err()
	}
}

func (b *Backend) Status(ctx context.Context) (model.StopEvent, error) {
	b.mu.Lock()
	exited, thread := b.exited, b.currentThread
	b.mu.Unlock()
	if exited {
		return model.StopEvent{State: model.StateExited, Reason: model.StopExited, Message: "the process has exited"}, nil
	}
	return b.describeStop(ctx, StoppedEvent{ThreadID: thread, Reason: "pause"})
}

// describeStop assembles the whole stopped state, matching what the Delve
// backend returns so one agent behaviour works against both.
func (b *Backend) describeStop(ctx context.Context, ev StoppedEvent) (model.StopEvent, error) {
	out := model.StopEvent{State: model.StatePaused, Reason: reasonFor(ev.Reason)}
	if len(ev.HitBreakpointIDs) > 0 {
		out.BreakpointID = b.localIDFor(ev.HitBreakpointIDs[0])
	}
	if ev.Text != "" {
		out.Message = ev.Text
	} else if ev.Description != "" {
		out.Message = ev.Description
	}

	if frames, err := b.Stack(ctx, strconv.Itoa(ev.ThreadID), 32); err == nil {
		out.Frames = frames
	}
	out.Variables, _ = b.Variables(ctx, 0, model.DefaultValueBudget())
	if units, err := b.ExecUnits(ctx, 50); err == nil {
		for i := range units {
			if units[i].ID == strconv.Itoa(ev.ThreadID) {
				units[i].State = model.UnitPaused
				out.Unit = &units[i]
				break
			}
		}
	}
	if len(out.Frames) > 0 && out.Frames[0].File != "" {
		out.Source = readSourceSpan(out.Frames[0].File, out.Frames[0].Line, 4)
	}
	out.RecentOutput = b.tailOutput(10)
	return out, nil
}

func reasonFor(dapReason string) model.StopReason {
	switch dapReason {
	case "breakpoint", "function breakpoint", "data breakpoint":
		return model.StopBreakpoint
	case "step", "entry":
		return model.StopStep
	case "exception":
		return model.StopPanic
	case "pause":
		return model.StopManual
	default:
		return model.StopUnknown
	}
}

func (b *Backend) localIDFor(remoteID int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tracked := range b.breakpoints {
		for _, t := range tracked {
			if t.remoteID == remoteID {
				return strconv.Itoa(t.localID)
			}
		}
	}
	return ""
}
