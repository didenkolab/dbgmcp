package tools

import (
	"context"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stepOut is the response shape for every control tool that moves execution.
// They all answer the same question -- "where am I now" -- so they all return
// the same thing, and an agent that has learned one has learned them all.
type stepResult = WaitOut

func toWaitOut(ev model.StopEvent) WaitOut {
	return WaitOut{
		State: string(ev.State), Reason: string(ev.Reason), BreakpointID: ev.BreakpointID,
		Unit: ev.Unit, Frames: ev.Frames, Variables: ev.Variables, Source: ev.Source,
		ExitStatus: ev.ExitStatus, Message: ev.Message,
	}
}

func (r *Registry) step(kind model.StepKind) func(context.Context, *mcp.CallToolRequest, SessionRef) (*mcp.CallToolResult, stepResult, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, stepResult, error) {
		sess, err := r.store.Resolve(in.SessionID)
		if err != nil {
			return fail[stepResult]("%s", err.Error())
		}
		ev, err := sess.Backend.Step(ctx, kind)
		if err != nil {
			return fail[stepResult]("%s", err.Error())
		}
		return ok(toWaitOut(ev))
	}
}

func (r *Registry) pauseExecution(ctx context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, stepResult, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[stepResult]("%s", err.Error())
	}
	ev, err := sess.Backend.Pause(ctx)
	if err != nil {
		return fail[stepResult]("%s", err.Error())
	}
	return ok(toWaitOut(ev))
}

type RunToLineIn struct {
	SessionID  string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	File       string `json:"file" jsonschema:"Absolute path of the source file to run to."`
	Line       int    `json:"line" jsonschema:"1-based line to stop at."`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"How long to wait before giving up. Defaults to 30."`
}

func (r *Registry) runToLine(ctx context.Context, _ *mcp.CallToolRequest, in RunToLineIn) (*mcp.CallToolResult, stepResult, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[stepResult]("%s", err.Error())
	}
	if in.File == "" || in.Line <= 0 {
		return fail[stepResult]("run_to_line needs both file and line.")
	}
	timeout := 30 * time.Second
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	ev, err := sess.Backend.RunToLine(ctx, in.File, in.Line, timeout)
	if err != nil {
		return fail[stepResult]("%s", err.Error())
	}
	return ok(toWaitOut(ev))
}

type SetVariableIn struct {
	SessionID  string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	Name       string `json:"name" jsonschema:"Variable or field path to change, for example it.Price. The same paths get_variables returns."`
	Value      string `json:"value" jsonschema:"New value, written as it would appear in source."`
	FrameIndex int    `json:"frame_index,omitempty" jsonschema:"Stack frame to act in, 0 being the innermost."`
}

type SetVariableOut struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Message string `json:"message"`
}

func (r *Registry) setVariable(ctx context.Context, _ *mcp.CallToolRequest, in SetVariableIn) (*mcp.CallToolResult, SetVariableOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[SetVariableOut]("%s", err.Error())
	}
	if in.Name == "" {
		return fail[SetVariableOut]("Missing required parameter: name")
	}
	if err := sess.Backend.SetVariable(ctx, in.FrameIndex, in.Name, in.Value); err != nil {
		return fail[SetVariableOut]("%s", err.Error())
	}
	// Read it back rather than reporting what was asked for: a write the agent
	// cannot observe is a write it should not trust.
	got, err := sess.Backend.Evaluate(ctx, in.FrameIndex, in.Name, model.ValueBudget{})
	if err != nil {
		return ok(SetVariableOut{Name: in.Name, Value: in.Value,
			Message: "The value was set, but reading it back failed: " + err.Error()})
	}
	return ok(SetVariableOut{Name: in.Name, Value: got.Value, Message: "Set and read back."})
}

type SetWatchpointIn struct {
	SessionID  string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	Expression string `json:"expression" jsonschema:"Variable to watch, for example total. It must already be in scope: stopping at a function's entry is before its locals exist."`
	Mode       string `json:"mode,omitempty" jsonschema:"'write' (default) stops when the value changes, 'read' when it is read, 'read_write' for both."`
	FrameIndex int    `json:"frame_index,omitempty" jsonschema:"Stack frame the variable lives in, 0 being the innermost."`
}

func (r *Registry) setWatchpoint(ctx context.Context, _ *mcp.CallToolRequest, in SetWatchpointIn) (*mcp.CallToolResult, BreakpointOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[BreakpointOut]("%s", err.Error())
	}
	if in.Expression == "" {
		return fail[BreakpointOut]("Missing required parameter: expression")
	}
	wp, err := sess.Backend.SetWatchpoint(ctx, in.FrameIndex, in.Expression, model.WatchMode(in.Mode))
	if err != nil {
		return fail[BreakpointOut]("%s", err.Error())
	}
	return ok(BreakpointOut{Breakpoint: wp,
		Message: "Watchpoint set. Resume and wait_for_pause to stop at the next access. " +
			"It is bound to this stack frame and disappears when the frame returns."})
}

type AncestorsIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	UnitID    string `json:"unit_id,omitempty" jsonschema:"Execution unit to trace back. Defaults to the one that is stopped."`
	Depth     int    `json:"depth,omitempty" jsonschema:"Frames per ancestor. Defaults to 16."`
}

type AncestorsOut struct {
	Ancestry model.Ancestry `json:"ancestry"`
}

func (r *Registry) getUnitAncestors(ctx context.Context, _ *mcp.CallToolRequest, in AncestorsIn) (*mcp.CallToolResult, AncestorsOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[AncestorsOut]("%s", err.Error())
	}
	anc, err := sess.Backend.Ancestors(ctx, in.UnitID, in.Depth)
	if err != nil {
		return fail[AncestorsOut]("%s", err.Error())
	}
	return ok(AncestorsOut{Ancestry: anc})
}
