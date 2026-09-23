// Package tools is the agent-facing surface. Tool names match the JetBrains
// debugger plugin wherever the semantics match, so one skill and one agent work
// against both an IDE and this server.
package tools

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/backend/dap"
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
	Language string            `json:"language,omitempty" jsonschema:"Runtime of the target: 'go' (default), 'python', or 'node' (also 'javascript' or 'typescript'). Capabilities differ by runtime, so read describe_backend after starting."`
	Mode     string            `json:"mode" jsonschema:"'test' runs go test, 'debug' builds and runs a main package, 'exec' runs an already-built binary, 'attach' takes control of a process that is already running."`
	Target   string            `json:"target" jsonschema:"Package path for test/debug (for example ./internal/billing or .), or the binary path for exec."`
	WorkDir  string            `json:"work_dir" jsonschema:"Absolute path of the directory to run in. All relative paths and breakpoint files resolve against it."`
	PID      int               `json:"pid,omitempty" jsonschema:"Process id to attach to. Required for mode=attach and ignored otherwise."`
	TestRun  string            `json:"test_run,omitempty" jsonschema:"Only for mode=test: the -test.run regular expression selecting which tests to run."`
	Args     []string          `json:"args,omitempty" jsonschema:"Arguments passed to the program itself."`
	Env      map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables for the debuggee."`
	// RecordAncestry is a switch rather than an environment variable the agent
	// has to know, and it is off by default because recording a stack at every
	// goroutine creation costs real performance.
	RecordAncestry bool `json:"record_ancestry,omitempty" jsonschema:"Record where each execution unit was created from, so get_unit_ancestors can answer. Off by default because it slows the target down."`

	// BuildTags matters more than it looks. A project can keep most of its test
	// suite behind a tag, and without naming the tag that half cannot be built
	// -- so it cannot be debugged, and it is usually the half that talks to a
	// database and holds the interesting defects.
	BuildTags  []string `json:"build_tags,omitempty" jsonschema:"Build tags the target needs in order to compile, for example [\"integration\"]. Without them, a suite kept behind a tag cannot be run at all."`
	BuildFlags string   `json:"build_flags,omitempty" jsonschema:"Anything else the build needs, passed through verbatim, for example -mod=vendor."`
	// Deterministic defaults to off so nothing changes for callers who do not
	// ask, but any agent setting a breakpoint in a named test wants it on.
	Deterministic bool `json:"deterministic,omitempty" jsonschema:"Switch off test-order randomisation. Set this when a breakpoint is in a named test: a shuffled run puts a different test in its place, which looks like the breakpoint failing."`
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
	case model.LaunchAttach:
		if in.PID <= 0 {
			return fail[StartOut]("mode=attach needs a pid. Find it with `pgrep -f <name>` or `ps`.")
		}
	default:
		return fail[StartOut]("Unknown mode %q. Use 'test', 'debug', 'exec' or 'attach'.", in.Mode)
	}
	if in.WorkDir == "" {
		return fail[StartOut]("Missing required parameter: work_dir")
	}

	env := in.Env
	if in.RecordAncestry {
		if env == nil {
			env = map[string]string{}
		}
		if _, set := env["GODEBUG"]; !set {
			env["GODEBUG"] = "tracebackancestors=10"
		}
	}

	b, err := backendFor(in.Language)
	if err != nil {
		return fail[StartOut]("%s", err.Error())
	}
	req := model.LaunchRequest{
		Mode: mode, Target: in.Target, WorkDir: in.WorkDir,
		TestRun: in.TestRun, Args: in.Args, Env: env, PID: in.PID,
		BuildTags: in.BuildTags, BuildFlags: in.BuildFlags, Deterministic: in.Deterministic,
	}
	if err := b.Launch(ctx, req); err != nil {
		return fail[StartOut]("%s", err.Error())
	}

	optimisations := false
	if reporter, canReport := b.(interface {
		OptimisationsDisabled(model.LaunchMode) bool
	}); canReport {
		optimisations = reporter.OptimisationsDisabled(mode)
	}
	sess := &session.Session{
		ID: session.NewID(), Backend: b, Request: req, StartedAt: time.Now(),
		OptimisationsDisabled: optimisations,
	}
	r.store.Add(sess)

	return ok(StartOut{
		SessionID: sess.ID, Backend: b.Name(), State: string(model.StatePaused),
		OptimisationsDisabled: sess.OptimisationsDisabled,
		Capabilities:          b.Capabilities(),
		Message:               startMessage(mode),
	})
}

// backendFor picks the debugger for a runtime. Go goes through Delve's native
// API because DAP cannot express what that backend offers; everything else goes
// through DAP, where one implementation serves several runtimes.
func backendFor(language string) (backend.Backend, error) {
	switch strings.ToLower(language) {
	case "", "go", "golang":
		return delve.New(), nil
	default:
		b, err := dap.New(language)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

// startMessage says what is actually true of this session, because the two
// cases differ in ways an agent must not have to infer.
func startMessage(mode model.LaunchMode) string {
	if mode.IsAttach() {
		return "Attached, and the process is suspended. It belongs to someone else: it was not started here, " +
			"stop_debug_session will leave it running rather than kill it, and its output goes wherever it already went, " +
			"so get_session_output has nothing to show. Resume it promptly -- everything it serves is stopped meanwhile."
	}
	return "The target is loaded and stopped before its first instruction. " +
		"Set breakpoints now, then call resume_execution followed by wait_for_pause."
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

type BackendTool struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	SupportedGo string `json:"supported_go,omitempty"`
}

type DescribeBackendIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"Describe the backend of this session. Omit to describe a backend that has no session yet."`
	Backend   string `json:"backend,omitempty" jsonschema:"Runtime to describe: 'go' or 'python'. Defaults to go."`
}

type DescribeBackendOut struct {
	Backend      string               `json:"backend"`
	Platform     string               `json:"platform" jsonschema:"GOOS/GOARCH the server runs on. Capabilities differ by platform, so this is part of the answer, not trivia."`
	Tool         BackendTool          `json:"tool"`
	Capabilities backend.Capabilities `json:"capabilities"`
	Message      string               `json:"message"`
}

// describeBackend answers before any session exists, because the point of
// capabilities is to plan against them -- and a plan made after launching is a
// plan made too late.
func (r *Registry) describeBackend(_ context.Context, _ *mcp.CallToolRequest, in DescribeBackendIn) (*mcp.CallToolResult, DescribeBackendOut, error) {
	var b backend.Backend
	if in.SessionID != "" {
		sess, err := r.store.Resolve(in.SessionID)
		if err != nil {
			return fail[DescribeBackendOut]("%s", err.Error())
		}
		b = sess.Backend
	} else {
		resolved, err := backendFor(in.Backend)
		if err != nil {
			return fail[DescribeBackendOut]("%s", err.Error())
		}
		b = resolved
	}

	out := DescribeBackendOut{
		Backend:      b.Name(),
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		Capabilities: b.Capabilities(),
		Message: "Plan against these capabilities rather than discovering the limits by failing into them. " +
			"Capabilities can differ by platform, so read them per backend and per machine.",
	}
	if reporter, canReport := b.(backend.ToolReporter); canReport {
		name, path, version, supports := reporter.Tool()
		out.Tool = BackendTool{Name: name, Path: path, Version: version, SupportedGo: supports}
		if path == "" {
			out.Message = "The external debugger this backend needs was not found. " +
				"Run `dbgmcp doctor` for the exact command to install it."
		}
	}
	return ok(out)
}
