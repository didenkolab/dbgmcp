// Command dbgmcp is an MCP server that debugs Go programs with breakpoints,
// without an IDE.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/didenkolab/dbgmcp/internal/backend/delve"
	"github.com/didenkolab/dbgmcp/internal/mcpserver"
	"github.com/didenkolab/dbgmcp/internal/session"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("%s %s (requires delve %s)\n", mcpserver.ServerName, mcpserver.ServerVersion, delve.RequiredVersion)
			return
		case "doctor":
			doctor()
			return
		}
	}

	store := session.NewStore()

	// Every session is stopped on the way out. Without this an interrupted
	// server leaves a dlv and a debuggee behind, which is how orphaned processes
	// pinned at 450% CPU happen.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer stopAll(store)

	if err := mcpserver.RunStdio(ctx, store); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "dbgmcp:", err)
		stopAll(store)
		os.Exit(1)
	}
}

func stopAll(store *session.Store) {
	for _, s := range store.List() {
		_ = s.Backend.Stop(context.Background())
	}
}

// doctor answers "why does this not work" without needing an agent to
// interrogate the server through the protocol.
func doctor() {
	path, err := delve.FindDelve()
	if err != nil {
		fmt.Println("delve:  NOT FOUND")
		fmt.Println("       ", err)
		os.Exit(1)
	}
	fmt.Println("delve:  ", path)
	fmt.Println("status:  ready")
}
