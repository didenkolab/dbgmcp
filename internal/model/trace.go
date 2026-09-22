package model

// Probe is one place to record from, and how many hits are wanted there.
//
// Declaring the expressions up front is what buys the whole saving: the
// debugger evaluates them itself on every hit, so a ten-thousand-iteration
// trace costs one call rather than ten thousand.
type Probe struct {
	Location Location `json:"location"`
	Record   []string `json:"record"`
	// MaxHits caps what this probe contributes. Zero means "until the program
	// ends or the trace times out".
	MaxHits int `json:"max_hits,omitempty"`
}

type TraceHit struct {
	// Probe indexes back into the request, so a transcript from several probes
	// can be separated again without matching on file and line.
	Probe    int    `json:"probe"`
	Hit      int    `json:"hit"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function,omitempty"`
	UnitID   string `json:"unit_id,omitempty"`
	// Values keys each recorded expression by the expression itself, so the
	// transcript reads without having to consult the request.
	Values map[string]string `json:"values"`
}

type TraceStatus string

const (
	// TraceCompleted means every probe reached its MaxHits.
	TraceCompleted TraceStatus = "completed"
	// TraceFinished means the program ended first, which is a normal outcome
	// rather than a failure.
	TraceFinished TraceStatus = "finished"
	TraceStopped  TraceStatus = "stopped"
	TraceTimeout  TraceStatus = "timeout"
)

type Transcript struct {
	Status TraceStatus `json:"status"`
	Hits   []TraceHit  `json:"hits"`
	// ProbesNeverHit distinguishes an empty transcript from a misplaced probe,
	// which are the same output but completely different problems.
	ProbesNeverHit []string `json:"probes_never_hit,omitempty"`
	// Mode is how this transcript was actually collected, copied from the
	// backend's capability so the agent reads it off the result rather than
	// from a docstring it may never have seen.
	Mode string `json:"mode"`
	// PerturbsTiming is false only where the backend records without stopping
	// the debuggee. An agent chasing a race needs to know which it got.
	PerturbsTiming bool   `json:"perturbs_timing"`
	Message        string `json:"message"`
}
