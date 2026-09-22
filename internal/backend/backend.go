package backend

import (
	"context"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// Backend is one debugger, driven by one session. Everything above this line is
// language-neutral; everything below it knows about Delve, DAP or CDP.
//
// The interface is deliberately shaped by the richest backend rather than the
// poorest. Modelling DAP first and then trying to surface Delve's extras yields
// an abstraction that cannot express them -- the standard
// lowest-common-denominator failure. Poorer backends enter as a degraded case
// and say so through Capabilities.
type Backend interface {
	Name() string
	Capabilities() Capabilities

	// Launch starts the target under the debugger. The target is stopped at its
	// entry point on return, so breakpoints can be set before anything runs --
	// without this an agent races the program it is trying to observe.
	Launch(ctx context.Context, req model.LaunchRequest) error
	// Stop terminates the debuggee and the debugger. It must be safe to call
	// twice and must leave no orphan process behind.
	Stop(ctx context.Context) error

	SetBreakpoint(ctx context.Context, bp model.Breakpoint) (model.Breakpoint, error)
	// SetWatchpoint stops the program when a value is accessed. Backends
	// without it declare Watchpoints=none and return an UnsupportedError naming
	// the alternative.
	SetWatchpoint(ctx context.Context, frameIndex int, expr string, mode model.WatchMode) (model.Breakpoint, error)
	ListBreakpoints(ctx context.Context) ([]model.Breakpoint, error)
	RemoveBreakpoint(ctx context.Context, id string) error

	Resume(ctx context.Context) error
	// Pause interrupts a running target. It is the one control an agent has
	// over a program that is not going to hit a breakpoint on its own.
	Pause(ctx context.Context) (model.StopEvent, error)
	// Step performs one step and returns where it landed, rather than requiring
	// a separate wait. Delve's stepping is synchronous, so charging the agent a
	// second round trip for the answer would be a self-inflicted cost.
	Step(ctx context.Context, kind model.StepKind) (model.StopEvent, error)
	// RunToLine continues until a location is reached, without leaving a
	// breakpoint behind for the agent to clean up.
	RunToLine(ctx context.Context, file string, line int, timeout time.Duration) (model.StopEvent, error)
	// WaitForStop blocks until the target next stops or exits. It returns the
	// whole stopped state -- unit, frames, variables, source -- because the
	// alternative is four more calls to answer the question the agent already
	// has.
	WaitForStop(ctx context.Context, timeout time.Duration) (model.StopEvent, error)

	Variables(ctx context.Context, frameIndex int, budget model.ValueBudget) ([]model.Variable, error)
	Evaluate(ctx context.Context, frameIndex int, expr string, budget model.ValueBudget) (model.Variable, error)
	Stack(ctx context.Context, unitID string, maxFrames int) ([]model.Frame, error)
	ExecUnits(ctx context.Context, limit int) ([]model.ExecUnit, error)
	// Trace records expressions at a set of probes and returns the transcript
	// from one call, with the debugger doing the evaluating. Backends that
	// cannot record without suspending still implement it; they declare
	// NonSuspendingTrace=suspend_only and set PerturbsTiming on the result.
	Trace(ctx context.Context, probes []model.Probe, timeout time.Duration) (model.Transcript, error)

	// Status reports where the session is right now without waiting for
	// anything, for re-inspecting a pause the agent has already seen.
	Status(ctx context.Context) (model.StopEvent, error)
	// Output returns what the debuggee printed, from a cursor. It keeps working
	// after the process exits, because a program's last words are most useful
	// once it is dead.
	Output(ctx context.Context, since, limit int) (model.OutputPage, error)

	Source(ctx context.Context, file string, line, contextLines int) (*model.SourceSpan, error)
	// SetVariable changes a value in the running program.
	SetVariable(ctx context.Context, frameIndex int, name, value string) error
	// Ancestors returns the chain of units that created this one.
	Ancestors(ctx context.Context, unitID string, depth int) (model.Ancestry, error)
}
