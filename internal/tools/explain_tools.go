package tools

import (
	"context"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExplainIn struct {
	SessionID  string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	Expression string `json:"expression" jsonschema:"The value to follow, written as it appears in the scope below, for example total."`
	Symbol     string `json:"symbol,omitempty" jsonschema:"Function the value lives in, for example billing.Subtotal. Supply this or file and line."`
	File       string `json:"file,omitempty" jsonschema:"Source file the value lives in, used with line."`
	Line       int    `json:"line,omitempty" jsonschema:"Line inside the scope, used with file."`
	Condition  string `json:"condition,omitempty" jsonschema:"Which call to follow, for a function called many times, for example order.ID == 42."`
	MaxWrites  int    `json:"max_writes,omitempty" jsonschema:"Stop after this many changes. Omit to follow until the frame returns."`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"Give up after this long. Defaults to 120."`
}

type ExplainOut struct {
	Expression string             `json:"expression"`
	Initial    string             `json:"initial"`
	Final      string             `json:"final"`
	Writes     []model.ValueWrite `json:"writes"`
	Status     string             `json:"status" jsonschema:"'frame_returned' means the history is complete; 'budget_reached', 'exited' and 'timeout' mean it was cut short."`
	Message    string             `json:"message"`
}

// explainValue is the tool this whole project is for: the agent asks why a
// value is what it is, and gets the sequence of changes rather than a place to
// start guessing from.
func (r *Registry) explainValue(ctx context.Context, _ *mcp.CallToolRequest, in ExplainIn) (*mcp.CallToolResult, ExplainOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[ExplainOut]("%s", err.Error())
	}
	if in.Expression == "" {
		return fail[ExplainOut]("Missing required parameter: expression")
	}
	if in.Symbol == "" && in.File == "" {
		return fail[ExplainOut]("explain_value needs a scope: symbol, or file and line, naming where %q lives.", in.Expression)
	}

	timeout := 120 * time.Second
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	explainer, canExplain := sess.Backend.(interface {
		ExplainValue(context.Context, model.ExplainRequest, time.Duration) (model.ValueHistory, error)
	})
	if !canExplain {
		return fail[ExplainOut](
			"The %s backend cannot follow a value's history. It needs watchpoints; check describe_backend, and fall back to trace_execution at the write sites.",
			sess.Backend.Name())
	}

	h, err := explainer.ExplainValue(ctx, model.ExplainRequest{
		Scope:      model.Location{File: in.File, Line: in.Line, Symbol: in.Symbol},
		Expression: in.Expression, Condition: in.Condition, MaxWrites: in.MaxWrites,
	}, timeout)
	if err != nil {
		return fail[ExplainOut]("%s", err.Error())
	}
	return ok(ExplainOut{
		Expression: h.Expression, Initial: h.Initial, Final: h.Final,
		Writes: h.Writes, Status: string(h.Status), Message: h.Message,
	})
}
