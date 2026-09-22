package model

// ExplainRequest asks for the history of one value.
//
// A local cannot be watched before the frame that holds it exists, so the
// history is bounded by that frame's lifetime rather than by the program's.
// That is not a compromise: "why did this end up wrong" is a question about one
// call, and the answer is the sequence of writes inside it.
type ExplainRequest struct {
	// Scope is where the value lives: a function symbol, or a file and line.
	Scope Location `json:"scope"`
	// Expression is the value to follow, as it is written in that scope.
	Expression string `json:"expression"`
	// Condition selects which call to follow, for a function called many times.
	Condition string `json:"condition,omitempty"`
	// MaxWrites bounds the history. Zero means until the frame returns.
	MaxWrites int `json:"max_writes,omitempty"`
}

// ValueWrite is one change, with what it was and what it became. Recording both
// is the point: a list of values without their predecessors makes the agent
// reconstruct the deltas, which is the part it is worst at.
type ValueWrite struct {
	Seq      int     `json:"seq"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	File     string  `json:"file,omitempty"`
	Line     int     `json:"line,omitempty"`
	Function string  `json:"function,omitempty"`
	UnitID   string  `json:"unit_id,omitempty"`
	Frames   []Frame `json:"frames,omitempty"`
}

type ExplainStatus string

const (
	// ExplainFrameReturned is the clean ending: the frame holding the value
	// went out of scope, so the history is complete rather than merely stopped.
	ExplainFrameReturned ExplainStatus = "frame_returned"
	ExplainBudgetReached ExplainStatus = "budget_reached"
	ExplainExited        ExplainStatus = "exited"
	ExplainTimeout       ExplainStatus = "timeout"
)

type ValueHistory struct {
	Expression string        `json:"expression"`
	Scope      Location      `json:"scope"`
	Initial    string        `json:"initial"`
	Final      string        `json:"final"`
	Writes     []ValueWrite  `json:"writes"`
	Status     ExplainStatus `json:"status"`
	Message    string        `json:"message"`
}
