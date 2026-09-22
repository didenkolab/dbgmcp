// Package backend defines what every debugger backend must provide and, just as
// importantly, what it must admit it cannot.
package backend

// Capabilities is the machine-readable answer to "what does this backend
// actually support".
//
// The JetBrains plugin solves the same problem with a prose table in its
// CLAUDE.md ("Limited Support: Rust, C++ ..."). That is a documentation answer
// to a machine problem: the agent learns the limit from text it may never have
// read, and cannot check it before acting. Here the agent reads capabilities and
// plans against them, instead of discovering limits by failing into them.
//
// A capability that is declared but not covered by the conformance suite is
// worse than no capability model at all, because the agent will trust it.
type Capabilities struct {
	Watchpoints        Support     `json:"watchpoints"`
	TraceMode          TraceMode   `json:"trace_mode"`
	HitCounts          HitCountsBy `json:"hit_counts"`
	EvalCallsFunctions Support     `json:"eval_calls_functions"`
	SetVariable        Support     `json:"set_variable"`
	BreakpointBySymbol bool        `json:"breakpoint_by_symbol"`
	Ancestry           bool        `json:"ancestry"`
	// BreakpointKinds lists the kinds this backend accepts, by model name.
	BreakpointKinds []string `json:"breakpoint_kinds"`
}

type Support string

const (
	SupportFull Support = "full"
	// SupportPrimitives is the honest answer for native debuggers, which can set
	// an int but not a String or a Vec.
	SupportPrimitives Support = "primitives"
	// SupportGuarded means the capability exists but is gated behind an explicit
	// opt-in because using it can change program state.
	SupportGuarded Support = "guarded"
	SupportNone    Support = "none"
)

type TraceMode string

// The three modes are three different deals for the agent, and the difference
// matters most to the one question tracing is worst at: a race.
const (
	// TraceBuffered means hits accumulate inside the debugger and the process
	// never stops. For Delve this needs eBPF uprobes, which are Linux-only and
	// privileged; on macOS the gdbserial backend reports SupportsBPF() == false
	// and GetBufferedTracepoints() returns nil.
	TraceBuffered TraceMode = "buffered"
	// TraceAutoContinue means the debugger resumes after each hit by itself, so
	// the agent is never in the loop -- but the debuggee genuinely stops on
	// every hit, so timing is perturbed exactly as a breakpoint perturbs it.
	// This is what Delve does everywhere without eBPF.
	TraceAutoContinue TraceMode = "auto_continue"
	// TraceSuspendOnly means every hit needs the agent to resume it.
	TraceSuspendOnly TraceMode = "suspend_only"
)

type HitCountsBy string

const (
	HitCountsPerUnit HitCountsBy = "per_unit"
	HitCountsTotal   HitCountsBy = "total"
	HitCountsNone    HitCountsBy = "none"
)
