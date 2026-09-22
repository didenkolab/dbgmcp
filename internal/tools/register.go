package tools

import (
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ptr[T any](v T) *T { return &v }

// readOnly marks a tool that only observes. Agents and clients use these hints
// to decide what may run without asking, so an inaccurate hint is a real defect.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
}

func mutating(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: false, DestructiveHint: ptr(false)}
}

// Register adds every tool to the server. Names match the JetBrains debugger
// plugin wherever the semantics match, so a single companion skill can drive an
// IDE session and a headless one without branching.
func (r *Registry) Register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "start_debug_session",
		Annotations: mutating("Start Debug Session"),
		Description: "Build and launch a Go target under the debugger, stopped before its first instruction so breakpoints can be set first. " +
			"Use mode=test for a go test run, mode=debug for a main package, mode=exec for an already-built binary. " +
			"Returns the backend's capabilities: plan against those rather than discovering limits by failing.",
	}, r.startDebugSession)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "stop_debug_session",
		Annotations: mutating("Stop Debug Session"),
		Description: "Terminate the debuggee and the debugger. Safe to call more than once.",
	}, r.stopDebugSession)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_debug_sessions",
		Annotations: readOnly("List Debug Sessions"),
		Description: "List the debug sessions this server is running.",
	}, r.listDebugSessions)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "describe_backend",
		Annotations: readOnly("Describe Backend"),
		Description: "Report what this debugger backend can and cannot do: watchpoints, non-suspending tracing, per-unit hit counts, calling functions during evaluation, breakpoints by symbol. " +
			"Read this before planning a debugging strategy.",
	}, r.describeBackend)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_breakpoint",
		Annotations: mutating("Set Breakpoint"),
		Description: "Set a breakpoint, by symbol (preferred: no line numbers needed) or by file and line. " +
			"Add condition to stop only when it holds, hit_condition to stop on a chosen iteration, " +
			"or record plus suspend=none to log expressions on every hit while the process keeps running.",
	}, r.setBreakpoint)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_breakpoints",
		Annotations: readOnly("List Breakpoints"),
		Description: "List the breakpoints set in this session, with real hit counts, total and per execution unit.",
	}, r.listBreakpoints)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_breakpoint",
		Annotations: mutating("Remove Breakpoint"),
		Description: "Remove a breakpoint by id.",
	}, r.removeBreakpoint)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "resume_execution",
		Annotations: mutating("Resume Execution"),
		Description: "Let the target run on. Follow with wait_for_pause to block until it stops.",
	}, r.resumeExecution)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "wait_for_pause",
		Annotations: readOnly("Wait For Pause"),
		Description: "Block until the target stops, then return the whole stopped state at once: location, reason, execution unit, stack, variables in scope and surrounding source. " +
			"Prefer this over calling get_variables, get_stack_trace and get_source_context separately.",
	}, r.waitForPause)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "step_over",
		Annotations: mutating("Step Over"),
		Description: "Run the current line and stop on the next one, without descending into calls. Returns where it landed, so no follow-up call is needed.",
	}, r.step(model.StepOver))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "step_into",
		Annotations: mutating("Step Into"),
		Description: "Step into the call on the current line. Returns where it landed.",
	}, r.step(model.StepInto))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "step_out",
		Annotations: mutating("Step Out"),
		Description: "Run until the current function returns, and stop in its caller. Returns where it landed.",
	}, r.step(model.StepOut))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "run_to_line",
		Annotations: mutating("Run To Line"),
		Description: "Continue until a specific line is reached. The temporary breakpoint used is always removed, including when the run fails.",
	}, r.runToLine)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pause_execution",
		Annotations: mutating("Pause Execution"),
		Description: "Interrupt a running target and report where it was. Use this on a program that is not going to hit a breakpoint by itself, for example one that is stuck.",
	}, r.pauseExecution)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_variable",
		Annotations: mutating("Set Variable"),
		Description: "Change a value in the running program, then read it back and report what it now holds. Paths are the same ones get_variables returns.",
	}, r.setVariable)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_watchpoint",
		Annotations: mutating("Set Watchpoint"),
		Description: "Stop the program when a variable is written to (or read). This answers 'what changed this value' directly, instead of guessing where to put a breakpoint. " +
			"The variable must already be in scope, and the watchpoint disappears when its stack frame returns.",
	}, r.setWatchpoint)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_unit_ancestors",
		Annotations: readOnly("Get Unit Ancestors"),
		Description: "Return the chain of execution units that created this one, with their stacks -- the answer to 'where did this goroutine come from'. " +
			"Requires the session to have been started with ancestry recording enabled.",
	}, r.getUnitAncestors)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "trace_execution",
		Annotations: mutating("Trace Execution"),
		Description: "Record expressions at one or more places while the program runs, and return the whole transcript in ONE call. " +
			"The debugger evaluates the expressions itself at every hit and resumes on its own, so watching a thousand iterations costs one call, not a thousand. " +
			"Prefer this over set_breakpoint plus resume plus wait loops whenever you already know what you want to watch. " +
			"Probes that never fired are listed separately, so an empty transcript is distinguishable from a misplaced probe.",
	}, r.traceExecution)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_variables",
		Annotations: readOnly("Get Variables"),
		Description: "Read the arguments and locals of a stack frame. Arguments come first, because when a function returns the wrong answer, what went in is usually the more useful half.",
	}, r.getVariables)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "evaluate_expression",
		Annotations: readOnly("Evaluate Expression"),
		Description: "Evaluate a Go expression in a stopped frame. Useful for testing a hypothesis against the live state, for example comparing what the code computes with what it should.",
	}, r.evaluateExpression)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_stack_trace",
		Annotations: readOnly("Get Stack Trace"),
		Description: "Return the call stack of a stopped execution unit.",
	}, r.getStackTrace)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_execution_units",
		Annotations: readOnly("List Execution Units"),
		Description: "List the target's units of execution -- goroutines for Go, threads or tasks for other runtimes -- with their state and top frame. " +
			"Units reported as blocked, with the reason they are waiting, are how a deadlock looks from here.",
	}, r.listExecutionUnits)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_source_context",
		Annotations: readOnly("Get Source Context"),
		Description: "Return a window of source around a line, so a location can be read without a separate file read.",
	}, r.getSourceContext)
}
