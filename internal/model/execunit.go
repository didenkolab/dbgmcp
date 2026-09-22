package model

// ExecUnitKind labels what a backend's unit of execution actually is, so the
// neutral model never has to pick a side between "thread" and "goroutine".
//
// Go has goroutines, Python has OS threads plus asyncio tasks, a browser has one
// thread and an async stack. Collapsing those into "thread" would make every
// non-JVM backend lie; splitting the tool per runtime would break the shared
// contract at the first new backend. The label is the third option.
type ExecUnitKind string

const (
	UnitThread    ExecUnitKind = "thread"
	UnitGoroutine ExecUnitKind = "goroutine"
	UnitTask      ExecUnitKind = "task"
)

// ExecUnitState is deliberately coarse. Backends report wildly different detail
// (Delve gives a wait reason, DAP gives almost nothing), so the neutral states
// are the ones every backend can honestly answer, with Detail carrying whatever
// extra the backend happens to know.
type ExecUnitState string

const (
	UnitRunning ExecUnitState = "running"
	UnitPaused  ExecUnitState = "paused"
	UnitBlocked ExecUnitState = "blocked"
	UnitExited  ExecUnitState = "exited"
)

type ExecUnit struct {
	ID      string        `json:"id"`
	Kind    ExecUnitKind  `json:"kind"`
	Name    string        `json:"name,omitempty"`
	State   ExecUnitState `json:"state"`
	Detail  string        `json:"detail,omitempty"`
	Current bool          `json:"current"`
	// TopFrame is the one frame worth showing in a listing. Full stacks are a
	// separate call precisely because there can be thousands of units.
	TopFrame *Frame `json:"topFrame,omitempty"`
}
