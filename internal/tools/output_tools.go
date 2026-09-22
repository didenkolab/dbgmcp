package tools

import (
	"context"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type OutputIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session to read from. May be omitted when exactly one session is open."`
	Since     int    `json:"since,omitempty" jsonschema:"Return only output newer than this sequence number. Pass back the next_since from the previous call to tail without re-reading."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum chunks to return. Defaults to 200."`
}

type OutputOut struct {
	Chunks    []model.OutputChunk `json:"chunks"`
	NextSince int                 `json:"next_since" jsonschema:"Pass this as 'since' next time to read only what is new."`
	Dropped   int                 `json:"dropped" jsonschema:"Chunks discarded because the buffer wrapped. Non-zero means output was lost, not that the program was quiet."`
	Message   string              `json:"message,omitempty"`
}

func (r *Registry) getSessionOutput(ctx context.Context, _ *mcp.CallToolRequest, in OutputIn) (*mcp.CallToolResult, OutputOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[OutputOut]("%s", err.Error())
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	page, err := sess.Backend.Output(ctx, in.Since, limit)
	if err != nil {
		return fail[OutputOut]("%s", err.Error())
	}
	out := OutputOut{Chunks: page.Chunks, NextSince: page.NextSince, Dropped: page.Dropped}
	if len(page.Chunks) == 0 {
		out.Message = "The debuggee has printed nothing new."
	}
	return ok(out)
}

func (r *Registry) getDebugSessionStatus(ctx context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, WaitOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[WaitOut]("%s", err.Error())
	}
	ev, err := sess.Backend.Status(ctx)
	if err != nil {
		return fail[WaitOut]("%s", err.Error())
	}
	return ok(toWaitOut(ev))
}
