package tools

import (
	"context"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------- breakpoints ----------

type SetBreakpointIn struct {
	SessionID    string   `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	File         string   `json:"file,omitempty" jsonschema:"Absolute path of the source file. Supply this with line, or supply symbol instead."`
	Line         int      `json:"line,omitempty" jsonschema:"1-based line number, used with file."`
	Symbol       string   `json:"symbol,omitempty" jsonschema:"Break on a function by name, for example main.lineTotal or (*Cart).Add. Preferred over file and line: it needs no line numbers and survives edits above it."`
	Condition    string   `json:"condition,omitempty" jsonschema:"Expression that must be true to stop, for example it.Price > 100. Evaluated by the debugger in the frame at the breakpoint."`
	Record       []string `json:"record,omitempty" jsonschema:"Expressions the debugger evaluates on every hit without contacting the agent. Combine with suspend=none to trace a loop in one call."`
	Suspend      string   `json:"suspend,omitempty" jsonschema:"'all' (default) stops the process. 'none' records and lets it run on, which does not perturb timing the way stopping does."`
	HitCondition string   `json:"hit_condition,omitempty" jsonschema:"Hit-count condition such as '> 5' or '100', to stop only on a chosen iteration."`
}

type BreakpointOut struct {
	Breakpoint model.Breakpoint `json:"breakpoint"`
	Message    string           `json:"message"`
}

func (r *Registry) setBreakpoint(ctx context.Context, _ *mcp.CallToolRequest, in SetBreakpointIn) (*mcp.CallToolResult, BreakpointOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[BreakpointOut]("%s", err.Error())
	}
	if in.Symbol == "" && in.File == "" {
		return fail[BreakpointOut]("A breakpoint needs either symbol, or file and line.")
	}
	if in.File != "" && in.Line <= 0 {
		return fail[BreakpointOut]("Missing required parameter: line (a file was given without one)")
	}

	suspend := model.SuspendAll
	if in.Suspend == string(model.SuspendNone) {
		suspend = model.SuspendNone
	}
	bp, err := sess.Backend.SetBreakpoint(ctx, model.Breakpoint{
		Kind:         model.BreakLine,
		Location:     model.Location{File: in.File, Line: in.Line, Symbol: in.Symbol},
		Condition:    in.Condition,
		HitCondition: in.HitCondition,
		Record:       in.Record,
		Suspend:      suspend,
	})
	if err != nil {
		return fail[BreakpointOut]("%s", err.Error())
	}
	msg := "Breakpoint set."
	if suspend == model.SuspendNone {
		msg = "Tracepoint set: it records on every hit and lets the process run on."
	}
	return ok(BreakpointOut{Breakpoint: bp, Message: msg})
}

type ListBreakpointsOut struct {
	Breakpoints []model.Breakpoint `json:"breakpoints"`
	Message     string             `json:"message"`
}

func (r *Registry) listBreakpoints(ctx context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, ListBreakpointsOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[ListBreakpointsOut]("%s", err.Error())
	}
	bps, err := sess.Backend.ListBreakpoints(ctx)
	if err != nil {
		return fail[ListBreakpointsOut]("%s", err.Error())
	}
	out := ListBreakpointsOut{Breakpoints: bps}
	if len(bps) == 0 {
		out.Message = "No breakpoints are set."
	}
	return ok(out)
}

type RemoveBreakpointIn struct {
	SessionID    string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	BreakpointID string `json:"breakpoint_id" jsonschema:"Id returned by set_breakpoint or list_breakpoints."`
}

type MessageOut struct {
	Message string `json:"message"`
}

func (r *Registry) removeBreakpoint(ctx context.Context, _ *mcp.CallToolRequest, in RemoveBreakpointIn) (*mcp.CallToolResult, MessageOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[MessageOut]("%s", err.Error())
	}
	if in.BreakpointID == "" {
		return fail[MessageOut]("Missing required parameter: breakpoint_id")
	}
	if err := sess.Backend.RemoveBreakpoint(ctx, in.BreakpointID); err != nil {
		return fail[MessageOut]("%s", err.Error())
	}
	return ok(MessageOut{Message: "Breakpoint " + in.BreakpointID + " removed successfully."})
}

// ---------- execution ----------

func (r *Registry) resumeExecution(ctx context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, MessageOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[MessageOut]("%s", err.Error())
	}
	if err := sess.Backend.Resume(ctx); err != nil {
		return fail[MessageOut]("%s", err.Error())
	}
	return ok(MessageOut{Message: "Resumed. Call wait_for_pause to block until it stops."})
}

type WaitIn struct {
	SessionID  string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"How long to wait before reporting that the target is still running. Defaults to 30."`
}

// WaitOut carries the whole stopped state. Returning it in one response is the
// single biggest lever on how many round trips a debugging session costs: the
// agent learns where it stopped, why, with what stack and what variables,
// without four more calls.
type WaitOut struct {
	State        string              `json:"state"`
	Reason       string              `json:"reason"`
	BreakpointID string              `json:"breakpoint_id,omitempty"`
	Unit         *model.ExecUnit     `json:"unit,omitempty"`
	Frames       []model.Frame       `json:"frames,omitempty"`
	Variables    []model.Variable    `json:"variables,omitempty"`
	Source       *model.SourceSpan   `json:"source,omitempty"`
	ExitStatus   *int                `json:"exit_status,omitempty"`
	RecentOutput []model.OutputChunk `json:"recent_output,omitempty" jsonschema:"The last lines the debuggee printed before it stopped. Use get_session_output for the rest."`
	Message      string              `json:"message,omitempty"`
}

func (r *Registry) waitForPause(ctx context.Context, _ *mcp.CallToolRequest, in WaitIn) (*mcp.CallToolResult, WaitOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[WaitOut]("%s", err.Error())
	}
	timeout := 30 * time.Second
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	ev, err := sess.Backend.WaitForStop(ctx, timeout)
	if err != nil {
		return fail[WaitOut]("%s", err.Error())
	}
	return ok(WaitOut{
		State: string(ev.State), Reason: string(ev.Reason), BreakpointID: ev.BreakpointID,
		Unit: ev.Unit, Frames: ev.Frames, Variables: ev.Variables, Source: ev.Source,
		ExitStatus: ev.ExitStatus, RecentOutput: ev.RecentOutput, Message: ev.Message,
	})
}

// ---------- inspection ----------

type BudgetIn struct {
	MaxDepth       int `json:"max_depth,omitempty" jsonschema:"How deep to follow nested values. Default 2."`
	MaxStringLen   int `json:"max_string_len,omitempty" jsonschema:"Bytes read from each string. Default 256."`
	MaxArrayValues int `json:"max_array_values,omitempty" jsonschema:"Elements read from each slice, array or map. Default 64."`
}

func (b BudgetIn) toModel() model.ValueBudget {
	return model.ValueBudget{
		MaxDepth: b.MaxDepth, MaxStringLen: b.MaxStringLen, MaxArrayValues: b.MaxArrayValues,
	}
}

type GetVariablesIn struct {
	SessionID  string   `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	FrameIndex int      `json:"frame_index,omitempty" jsonschema:"Stack frame to read, 0 being the innermost. Defaults to 0."`
	Budget     BudgetIn `json:"budget,omitempty" jsonschema:"Caps on how much of each value is returned, so one call cannot flood the context."`
}

type VariablesOut struct {
	Variables []model.Variable `json:"variables"`
	Message   string           `json:"message,omitempty"`
}

func (r *Registry) getVariables(ctx context.Context, _ *mcp.CallToolRequest, in GetVariablesIn) (*mcp.CallToolResult, VariablesOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[VariablesOut]("%s", err.Error())
	}
	vars, err := sess.Backend.Variables(ctx, in.FrameIndex, in.Budget.toModel())
	if err != nil {
		return fail[VariablesOut]("%s", err.Error())
	}
	out := VariablesOut{Variables: vars}
	if len(vars) == 0 {
		out.Message = "No variables in this frame. If the target was built without the debugger's own build flags, the optimiser may have removed them."
	}
	return ok(out)
}

type EvaluateIn struct {
	SessionID  string   `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	Expression string   `json:"expression" jsonschema:"Go expression evaluated in the selected frame, for example it.Price * it.Qty."`
	FrameIndex int      `json:"frame_index,omitempty" jsonschema:"Stack frame to evaluate in, 0 being the innermost."`
	Budget     BudgetIn `json:"budget,omitempty" jsonschema:"Caps on how much of the result is returned."`
}

type EvaluateOut struct {
	Result model.Variable `json:"result"`
}

func (r *Registry) evaluateExpression(ctx context.Context, _ *mcp.CallToolRequest, in EvaluateIn) (*mcp.CallToolResult, EvaluateOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[EvaluateOut]("%s", err.Error())
	}
	if in.Expression == "" {
		return fail[EvaluateOut]("Missing required parameter: expression")
	}
	v, err := sess.Backend.Evaluate(ctx, in.FrameIndex, in.Expression, in.Budget.toModel())
	if err != nil {
		return fail[EvaluateOut]("%s", err.Error())
	}
	return ok(EvaluateOut{Result: v})
}

type StackIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	UnitID    string `json:"unit_id,omitempty" jsonschema:"Execution unit (goroutine) to read. Defaults to the one that is stopped."`
	MaxFrames int    `json:"max_frames,omitempty" jsonschema:"Cap on frames returned. Defaults to 32."`
}

type StackOut struct {
	Frames []model.Frame `json:"frames"`
}

func (r *Registry) getStackTrace(ctx context.Context, _ *mcp.CallToolRequest, in StackIn) (*mcp.CallToolResult, StackOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[StackOut]("%s", err.Error())
	}
	frames, err := sess.Backend.Stack(ctx, in.UnitID, in.MaxFrames)
	if err != nil {
		return fail[StackOut]("%s", err.Error())
	}
	return ok(StackOut{Frames: frames})
}

type UnitsIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Cap on units returned. Defaults to 50, because a Go program can have thousands of goroutines."`
}

type UnitsOut struct {
	Units   []model.ExecUnit `json:"units"`
	Message string           `json:"message,omitempty"`
}

func (r *Registry) listExecutionUnits(ctx context.Context, _ *mcp.CallToolRequest, in UnitsIn) (*mcp.CallToolResult, UnitsOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[UnitsOut]("%s", err.Error())
	}
	units, err := sess.Backend.ExecUnits(ctx, in.Limit)
	if err != nil {
		return fail[UnitsOut]("%s", err.Error())
	}
	return ok(UnitsOut{Units: units})
}

type SourceIn struct {
	SessionID    string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	File         string `json:"file" jsonschema:"Absolute path of the source file."`
	Line         int    `json:"line" jsonschema:"1-based line to centre the window on."`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Lines shown either side. Defaults to 5."`
}

type SourceOut struct {
	Source *model.SourceSpan `json:"source"`
}

func (r *Registry) getSourceContext(ctx context.Context, _ *mcp.CallToolRequest, in SourceIn) (*mcp.CallToolResult, SourceOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[SourceOut]("%s", err.Error())
	}
	if in.File == "" {
		return fail[SourceOut]("Missing required parameter: file")
	}
	span, err := sess.Backend.Source(ctx, in.File, in.Line, in.ContextLines)
	if err != nil {
		return fail[SourceOut]("%s", err.Error())
	}
	return ok(SourceOut{Source: span})
}

type SelectFrameIn struct {
	SessionID  string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	FrameIndex int    `json:"frame_index" jsonschema:"Frame to make current, 0 being the innermost."`
}

type SelectFrameOut struct {
	Frame   model.Frame `json:"frame"`
	Message string      `json:"message"`
}

func (r *Registry) selectStackFrame(ctx context.Context, _ *mcp.CallToolRequest, in SelectFrameIn) (*mcp.CallToolResult, SelectFrameOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[SelectFrameOut]("%s", err.Error())
	}
	frame, err := sess.Backend.SelectFrame(ctx, in.FrameIndex)
	if err != nil {
		return fail[SelectFrameOut]("%s", err.Error())
	}
	return ok(SelectFrameOut{Frame: frame,
		Message: "Later calls that do not name a frame now act in this one."})
}
