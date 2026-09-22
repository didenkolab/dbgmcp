// Package tools is the agent-facing surface. Tool names match the JetBrains
// debugger plugin wherever the semantics match, so one skill and one agent work
// against both an IDE and this server.
package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/backend/delve"
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/didenkolab/dbgmcp/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Registry struct{ store *session.Store }

func NewRegistry(store *session.Store) *Registry { return &Registry{store: store} }

// fail returns a successful protocol response carrying an error message, rather
// than a JSON-RPC error.
//
// This is deliberate and matches the JetBrains plugin: the model can read a
// message and act on it, whereas a protocol error surfaces to the user as a
// hard transport failure and ends the agent's turn.
func fail[T any](format string, args ...any) (*mcp.CallToolResult, T, error) {
	var zero T
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}, zero, nil
}

func ok[T any](out T) (*mcp.CallToolResult, T, error) { return nil, out, nil }

// ---------- session lifecycle ----------

type StartIn struct {
	Mode    string            `json:"mode" jsonschema:"How to launch: 'test' to run go test, 'debug' to build and run a main package, 'exec' to run an already-built binary."`
	Target  string            `json:"target" jsonschema:"Package path for test/debug (for example ./internal/billing or .), or the binary path for exec."`
	WorkDir string            `json:"work_dir" jsonschema:"Absolute path of the directory to run in. All relative paths and breakpoint files resolve against it."`
	TestRun string            `json:"test_run,omitempty" jsonschema:"Only for mode=test: the -test.run regular expression selecting which tests to run."`
	Args    []string          `json:"args,omitempty" jsonschema:"Arguments passed to the program itself."`
	Env     map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables for the debuggee."`
}

type StartOut struct {
	SessionID string `json:"session_id"`
	Backend   string `json:"backend"`
	State     string `json:"state"`
	// OptimisationsDisabled tells the agent the binary it is debugging is not
	// the binary that ships, which changes what a surprising result means.
	OptimisationsDisabled bool                 `json:"optimisations_disabled"`
	Capabilities          backend.Capabilities `json:"capabilities"`
	Message               string               `json:"message"`
}

func (r *Registry) startDebugSession(ctx context.Context, _ *mcp.CallToolRequest, in StartIn) (*mcp.CallToolResult, StartOut, error) {
	mode := model.LaunchMode(in.Mode)
	switch mode {
	case model.LaunchTest, model.LaunchDebug, model.LaunchExec:
	default:
		return fail[StartOut]("Unknown mode %q. Use 'test', 'debug' or 'exec'.", in.Mode)
	}
	if in.WorkDir == "" {
		return fail[StartOut]("Missing required parameter: work_dir")
	}

	b := delve.New()
	req := model.LaunchRequest{
		Mode: mode, Target: in.Target, WorkDir: in.WorkDir,
		TestRun: in.TestRun, Args: in.Args, Env: in.Env,
	}
	if err := b.Launch(ctx, req); err != nil {
		return fail[StartOut]("%s", err.Error())
	}

	sess := &session.Session{
		ID: session.NewID(), Backend: b, Request: req, StartedAt: time.Now(),
		OptimisationsDisabled: b.OptimisationsDisabled(mode),
	}
	r.store.Add(sess)

	return ok(StartOut{
		SessionID: sess.ID, Backend: b.Name(), State: string(model.StatePaused),
		OptimisationsDisabled: sess.OptimisationsDisabled,
		Capabilities:          b.Capabilities(),
		Message: "The target is loaded and stopped before its first instruction. " +
			"Set breakpoints now, then call resume_execution followed by wait_for_pause.",
	})
}

type SessionRef struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
}

type StopOut struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

func (r *Registry) stopDebugSession(ctx context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, StopOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[StopOut]("%s", err.Error())
	}
	if err := sess.Backend.Stop(ctx); err != nil {
		return fail[StopOut]("%s", err.Error())
	}
	r.store.Remove(sess.ID)
	return ok(StopOut{SessionID: sess.ID, Message: "Session stopped and the debuggee terminated."})
}

type SessionInfo struct {
	SessionID string `json:"session_id"`
	Backend   string `json:"backend"`
	Mode      string `json:"mode"`
	Target    string `json:"target"`
	WorkDir   string `json:"work_dir"`
	StartedAt string `json:"started_at"`
}

type ListSessionsOut struct {
	Sessions []SessionInfo `json:"sessions"`
	Message  string        `json:"message"`
}

func (r *Registry) listDebugSessions(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, ListSessionsOut, error) {
	sessions := r.store.List()
	out := ListSessionsOut{Sessions: make([]SessionInfo, 0, len(sessions))}
	for _, s := range sessions {
		out.Sessions = append(out.Sessions, SessionInfo{
			SessionID: s.ID, Backend: s.Backend.Name(), Mode: string(s.Request.Mode),
			Target: s.Request.Target, WorkDir: s.Request.WorkDir,
			StartedAt: s.StartedAt.Format(time.RFC3339),
		})
	}
	if len(out.Sessions) == 0 {
		out.Message = "No debug sessions are running. Start one with start_debug_session."
	}
	return ok(out)
}

type DescribeBackendOut struct {
	Backend      string               `json:"backend"`
	Capabilities backend.Capabilities `json:"capabilities"`
	Message      string               `json:"message"`
}

func (r *Registry) describeBackend(_ context.Context, _ *mcp.CallToolRequest, in SessionRef) (*mcp.CallToolResult, DescribeBackendOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[DescribeBackendOut]("%s", err.Error())
	}
	return ok(DescribeBackendOut{
		Backend: sess.Backend.Name(), Capabilities: sess.Backend.Capabilities(),
		Message: "Plan against these capabilities rather than discovering the limits by failing into them.",
	})
}
