package model

import "time"

type SessionState string

const (
	StateStarting SessionState = "starting"
	StateRunning  SessionState = "running"
	StatePaused   SessionState = "paused"
	StateExited   SessionState = "exited"
)

// StopReason is reported only when the backend actually knows it. The JetBrains
// plugin infers it from a file/line heuristic and can therefore never say
// "exception" even though its schema advertises it; here an Unknown is returned
// instead of a guess.
type StopReason string

const (
	StopBreakpoint StopReason = "breakpoint"
	StopStep       StopReason = "step"
	StopPanic      StopReason = "panic"
	StopManual     StopReason = "manual"
	StopExited     StopReason = "exited"
	StopUnknown    StopReason = "unknown"
)

// LaunchMode covers the ways a Go target can be put under a debugger.
type LaunchMode string

const (
	LaunchTest  LaunchMode = "test"
	LaunchDebug LaunchMode = "debug"
	LaunchExec  LaunchMode = "exec"
	// LaunchAttach takes control of a process this server did not start. It is
	// different in kind from the others, not just in degree: the process
	// belongs to somebody else, so detaching must leave it running and its
	// output stays wherever it was already going.
	LaunchAttach LaunchMode = "attach"
)

// IsAttach reports whether a session took over a process rather than starting
// one. Several safety decisions hang off this, so it is asked by name rather
// than compared inline.
func (m LaunchMode) IsAttach() bool { return m == LaunchAttach }

// LaunchRequest is what an agent asks for. WorkDir anchors every relative path
// in the session, including the ones in breakpoint locations.
type LaunchRequest struct {
	Mode    LaunchMode        `json:"mode"`
	Target  string            `json:"target"`
	Args    []string          `json:"args,omitempty"`
	WorkDir string            `json:"work_dir"`
	Env     map[string]string `json:"env,omitempty"`
	// TestRun is the -test.run filter, meaningful only for LaunchTest.
	TestRun string `json:"test_run,omitempty"`
	// PID is the process to attach to, meaningful only for LaunchAttach.
	PID int `json:"pid,omitempty"`
}

type Session struct {
	ID        string        `json:"id"`
	Backend   string        `json:"backend"`
	State     SessionState  `json:"state"`
	Request   LaunchRequest `json:"request"`
	StartedAt time.Time     `json:"started_at"`
	// OptimisationsDisabled records that the target was rebuilt with the
	// optimiser off so variables survive. It is surfaced to the agent because it
	// means the binary under the debugger is not the binary that ships.
	OptimisationsDisabled bool `json:"optimisations_disabled"`
}

// StopEvent is what wait_for_pause resolves to. It carries enough to answer the
// next question without another call, which is the single biggest lever on how
// many round trips a debugging session costs.
type StopEvent struct {
	State        SessionState `json:"state"`
	Reason       StopReason   `json:"reason"`
	BreakpointID string       `json:"breakpoint_id,omitempty"`
	Unit         *ExecUnit    `json:"unit,omitempty"`
	Frames       []Frame      `json:"frames,omitempty"`
	Variables    []Variable   `json:"variables,omitempty"`
	Source       *SourceSpan  `json:"source,omitempty"`
	ExitStatus   *int         `json:"exit_status,omitempty"`
	// RecentOutput is the last thing the debuggee printed before it stopped.
	// It rides along because "what did the program just say" is asked at almost
	// every pause, and outside an IDE there is no console to glance at.
	RecentOutput []OutputChunk `json:"recent_output,omitempty"`
	Message      string        `json:"message,omitempty"`
}

type SourceSpan struct {
	File      string   `json:"file"`
	FirstLine int      `json:"first_line"`
	Lines     []string `json:"lines"`
	// MarkLine is the line the debugger is actually stopped on.
	MarkLine int `json:"mark_line"`
}
