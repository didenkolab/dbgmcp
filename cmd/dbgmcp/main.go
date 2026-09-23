// Command dbgmcp is an MCP server that debugs Go programs with breakpoints,
// without an IDE.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
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
		case "trace":
			os.Exit(traceCommand(os.Args[2:]))
		case "diff":
			os.Exit(diffCommand(os.Args[2:]))
		case "help", "--help", "-h":
			usage()
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

func usage() {
	fmt.Print(`dbgmcp - debug programs with breakpoints, from an AI agent or a pipeline.

  dbgmcp              serve MCP over stdio (how an agent uses it)
  dbgmcp trace ...    record expressions while a target runs, and write a report
  dbgmcp diff ...     run the same probes twice and report the first divergence
  dbgmcp doctor       report the platform and the debugger in use
  dbgmcp version

Run "dbgmcp trace -h" or "dbgmcp diff -h" for their options.
`)
}

func stopAll(store *session.Store) {
	for _, s := range store.List() {
		_ = s.Backend.Stop(context.Background())
	}
}

// doctor answers "why does this not work" without needing an agent to
// interrogate the server through the protocol.
func doctor() {
	fmt.Printf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	info, err := delve.Resolve()
	if err != nil {
		fmt.Println("delve:    UNUSABLE")
		fmt.Println("         ", err)
		os.Exit(1)
	}
	fmt.Printf("delve:    %s (version %s)\n", info.Path, info.Version)
	fmt.Printf("supports: %s in the target binary\n", info.SupportedGo)
	// Attaching is the one capability whose availability is a property of the
	// machine rather than of the debugger, so it is reported here rather than
	// discovered when an attach fails.
	if runtime.GOOS == "linux" {
		if raw, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope"); err == nil {
			scope := strings.TrimSpace(string(raw))
			note := "attach to any process this user owns"
			if scope != "0" {
				note = "attach is limited to descendants of this process"
			}
			fmt.Printf("attach:   yama ptrace_scope=%s - %s\n", scope, note)
		}
	}
	fmt.Println("status:   ready")
}
