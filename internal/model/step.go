package model

// StepKind is the unit of a single step. Naming them in the model rather than
// giving each one its own backend method keeps a backend from having to grow
// three near-identical methods, and keeps the tool layer's mapping obvious.
type StepKind string

const (
	StepOver StepKind = "over"
	StepInto StepKind = "into"
	StepOut  StepKind = "out"
)

// Ancestry is the creation chain of an execution unit: who started it, and who
// started them.
//
// Most debuggers cannot answer this at all. In Go it requires the debuggee to
// run with GODEBUG=tracebackancestors set, which costs performance -- so it is
// opt-in, and a backend that has it declares Ancestry.
type Ancestry struct {
	UnitID string     `json:"unit_id"`
	Chain  []Ancestor `json:"chain"`
	Note   string     `json:"note,omitempty"`
}

type Ancestor struct {
	UnitID string  `json:"unit_id"`
	Frames []Frame `json:"frames"`
	// Unreadable explains why a link in the chain could not be recovered,
	// instead of silently shortening the chain.
	Unreadable string `json:"unreadable,omitempty"`
}

// WatchMode says which accesses to a value should stop the program.
type WatchMode string

const (
	WatchWrite     WatchMode = "write"
	WatchRead      WatchMode = "read"
	WatchReadWrite WatchMode = "read_write"
)
