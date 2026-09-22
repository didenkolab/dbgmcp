package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend/delve"
	"github.com/didenkolab/dbgmcp/internal/mcpserver"
	"github.com/didenkolab/dbgmcp/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxCallsToFindTheBug is the acceptance criterion, not a detail. A debugger an
// agent needs thirty round trips to use has failed in spirit even when every
// individual call works, so the budget is asserted and must not grow.
const maxCallsToFindTheBug = 6

type agent struct {
	t     *testing.T
	sess  *mcp.ClientSession
	calls int
}

func (a *agent) call(name string, args map[string]any) map[string]any {
	a.t.Helper()
	a.calls++

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := a.sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		a.t.Fatalf("%s: transport error: %v", name, err)
	}
	if res.IsError {
		a.t.Fatalf("%s reported an error: %s", name, textOf(res))
	}
	var out map[string]any
	if err := json.Unmarshal(mustJSON(a.t, res.StructuredContent), &out); err != nil {
		a.t.Fatalf("%s: could not read the structured result: %v", name, err)
	}
	return out
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "buggy")
}

// TestAgentFindsTheBugThroughMCP is the whole project's success criterion,
// expressed as a test: an agent that can only speak MCP must locate a wrong
// value at runtime and name where it comes from.
func TestAgentFindsTheBugThroughMCP(t *testing.T) {
	if _, err := delve.FindDelve(); err != nil {
		t.Skipf("delve is not installed: %v", err)
	}
	ctx := context.Background()

	serverT, clientT := mcp.NewInMemoryTransports()
	store := session.NewStore()
	server := mcpserver.New(store)

	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverT) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance-agent", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	a := &agent{t: t, sess: cs}

	// 1. Launch the failing program.
	start := a.call("start_debug_session", map[string]any{
		"mode": "debug", "target": ".", "work_dir": fixtureDir(t),
	})
	if start["session_id"] == "" {
		t.Fatal("no session id")
	}
	// The agent is told up front that the binary is not the shipping one.
	if start["optimisations_disabled"] != true {
		t.Error("the agent was not told optimisations are disabled")
	}

	// 2. Break on the suspect function, by name, only for the expensive item.
	a.call("set_breakpoint", map[string]any{
		"symbol": "main.lineTotal", "condition": "it.Price > 100",
	})

	// 3 and 4. Run to it and read everything in one response.
	a.call("resume_execution", nil)
	stop := a.call("wait_for_pause", map[string]any{"timeout_sec": 60})

	if stop["reason"] != "breakpoint" {
		t.Fatalf("expected a breakpoint stop, got %v (%v)", stop["reason"], stop["message"])
	}
	// One response must carry the whole situation; otherwise the agent spends
	// three more calls learning where it is.
	for _, key := range []string{"frames", "variables", "source", "unit"} {
		if stop[key] == nil {
			t.Errorf("wait_for_pause did not include %q", key)
		}
	}
	src, _ := stop["source"].(map[string]any)
	if src == nil || src["file"] == nil {
		t.Fatal("no source context in the stop event")
	}
	buggyFile, _ := src["file"].(string)
	buggyLine := src["mark_line"]

	// 5. Test the hypothesis against live state: the code returns Price, but the
	// answer should be Price * Qty.
	got := a.call("evaluate_expression", map[string]any{"expression": "it.Price"})
	want := a.call("evaluate_expression", map[string]any{"expression": "it.Price * it.Qty"})

	gotVal := got["result"].(map[string]any)["value"]
	wantVal := want["result"].(map[string]any)["value"]
	if gotVal == wantVal {
		t.Fatalf("the fixture no longer demonstrates the bug: %v == %v", gotVal, wantVal)
	}

	if !strings.HasSuffix(buggyFile, "main.go") {
		t.Errorf("located the wrong file: %s", buggyFile)
	}
	t.Logf("bug located at %s:%v -- returns %v, should be %v (%d MCP calls)",
		buggyFile, buggyLine, gotVal, wantVal, a.calls)

	if a.calls > maxCallsToFindTheBug {
		t.Errorf("took %d MCP calls to find the bug, budget is %d", a.calls, maxCallsToFindTheBug)
	}

	a.call("stop_debug_session", nil)
	cs.Close()
	select {
	case <-serverDone:
	case <-time.After(10 * time.Second):
	}
}
