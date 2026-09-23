// Package mcpserver wires the tool registry onto an MCP transport.
package mcpserver

import (
	"context"

	"github.com/didenkolab/dbgmcp/internal/session"
	"github.com/didenkolab/dbgmcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ServerName    = "dbgmcp"
	ServerVersion = "0.2.0"
)

// Instructions are read by the client before any tool call, so they carry the
// one workflow an agent most often gets wrong: breakpoints must be set while
// the target is still stopped at its entry point.
const Instructions = `Debug Go programs with breakpoints, without an IDE.

Typical flow:
  start_debug_session -> set_breakpoint -> resume_execution -> wait_for_pause -> evaluate_expression

start_debug_session leaves the target stopped before its first instruction, so set every
breakpoint before the first resume_execution. wait_for_pause returns the stack, the variables
and the surrounding source in one response, so there is usually no need to follow it with
get_variables or get_stack_trace.

Prefer breaking by symbol over file and line. To watch a value across many iterations, set one
breakpoint with record and suspend=none rather than stopping on every pass.`

func New(store *session.Store) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{Instructions: Instructions},
	)
	tools.NewRegistry(store).Register(s)
	return s
}

// RunStdio serves on stdin and stdout, which is how editors and agent CLIs
// launch an MCP server.
func RunStdio(ctx context.Context, store *session.Store) error {
	return New(store).Run(ctx, &mcp.StdioTransport{})
}
