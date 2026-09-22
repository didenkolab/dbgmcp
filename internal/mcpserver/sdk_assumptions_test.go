package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/mcpserver"
	"github.com/didenkolab/dbgmcp/internal/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file pins what the server relies on the MCP SDK doing, asserted over the
// wire rather than against the SDK's documentation.
//
// It plays the role McpSdkAssumptionsTest plays in the JetBrains plugin: when
// an SDK bump changes one of these, the failure should name the assumption that
// moved, not appear as a puzzling behaviour change in an unrelated tool.

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	server := mcpserver.New(session.NewStore())
	go func() { _ = server.Run(ctx, serverT) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "assumptions", Version: "1"}, nil).
		Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestSdkInfersInputAndOutputSchemasFromGoTypes(t *testing.T) {
	// The whole tool layer is written as typed Go structs with jsonschema tags.
	// If inference stopped, every tool would silently lose its schema.
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools were advertised")
	}
	for _, tool := range res.Tools {
		if tool.InputSchema == nil {
			t.Errorf("%s has no input schema", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no output schema", tool.Name)
		}
	}
}

func TestSdkCarriesPropertyDescriptionsFromStructTags(t *testing.T) {
	// Tool arguments are documented only in `jsonschema` tags. If those stopped
	// reaching the wire the tools would still work and become much harder to
	// use correctly.
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "set_breakpoint" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "Break on a function by name") {
			t.Errorf("the description from the struct tag did not reach the wire: %s", raw)
		}
		return
	}
	t.Fatal("set_breakpoint was not advertised")
}

func TestSdkReturnsToolFailuresAsResultsNotProtocolErrors(t *testing.T) {
	// This is the load-bearing one. A failing tool must come back as a
	// successful response carrying IsError, so the model can read the message
	// and act on it. A JSON-RPC error surfaces as a hard transport failure and
	// ends the agent's turn.
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_breakpoints", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("a tool failure arrived as a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("calling a session tool with no session should be an error result")
	}
	var text string
	for _, c := range res.Content {
		if tc, isText := c.(*mcp.TextContent); isText {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "No active debug session") {
		t.Errorf("the message a model would read is missing: %q", text)
	}
}

func TestSdkPopulatesStructuredContentFromTypedResults(t *testing.T) {
	// Tools return Go structs and never build content by hand. If the SDK
	// stopped filling structuredContent, every tool would be advertising an
	// output schema it does not satisfy.
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_debug_sessions", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}
	if res.StructuredContent == nil {
		t.Fatal("no structuredContent, though the tool declares an output schema")
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), "sessions") {
		t.Errorf("structured result does not match the declared shape: %s", raw)
	}
}

func TestSdkReportsAnUnknownToolWithoutKillingTheSession(t *testing.T) {
	// An agent that guesses a tool name should get a message back and be able
	// to carry on, not lose the connection.
	cs := connect(t)
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "no_such_tool", Arguments: map[string]any{},
	})
	if err == nil {
		t.Log("unknown tools come back as error results")
	}
	// Whatever form the refusal takes, the session must still work afterwards.
	if _, listErr := cs.ListTools(context.Background(), nil); listErr != nil {
		t.Fatalf("the session did not survive an unknown tool: %v", listErr)
	}
}

func TestSdkDeliversServerInstructions(t *testing.T) {
	// The workflow agents most often get wrong -- set breakpoints before the
	// first resume -- is carried in the server instructions.
	cs := connect(t)
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "before the first resume_execution") {
		t.Errorf("server instructions did not reach the client: %q", got)
	}
}

func TestSdkPreservesToolAnnotations(t *testing.T) {
	// Read-only hints decide what a client may run without asking, so an
	// annotation that silently stopped being transmitted would be a safety
	// regression, not a cosmetic one.
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]*mcp.ToolAnnotations{}
	for _, tool := range res.Tools {
		seen[tool.Name] = tool.Annotations
	}
	if a := seen["get_variables"]; a == nil || !a.ReadOnlyHint {
		t.Errorf("get_variables lost its read-only hint: %+v", a)
	}
	if a := seen["set_breakpoint"]; a == nil || a.ReadOnlyHint {
		t.Errorf("set_breakpoint is marked read-only, which it is not: %+v", a)
	}
}
