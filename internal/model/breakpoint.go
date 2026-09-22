package model

// BreakpointKind is an open string, not a closed enum, and that is the whole
// point. A browser can break on a DOM mutation, on an event listener firing, or
// on a request matching a URL -- kinds with no backend-language analogue. Had
// this been sealed at line|watch, adding them later would be a breaking change
// for every client, so the door is held open from the first commit.
//
// A backend declares the kinds it supports through Capabilities; unsupported
// kinds are refused by name rather than silently ignored.
type BreakpointKind string

const (
	BreakLine  BreakpointKind = "line"
	BreakWatch BreakpointKind = "watch"
	// Declared, not implemented. Listed here so the vocabulary is one place.
	BreakDOM     BreakpointKind = "dom"
	BreakEvent   BreakpointKind = "event"
	BreakNetwork BreakpointKind = "network"
)

// SuspendPolicy says what happens when the breakpoint is reached. "none" is what
// makes tracing possible: the backend records and lets the process run on.
type SuspendPolicy string

const (
	SuspendAll  SuspendPolicy = "all"
	SuspendNone SuspendPolicy = "none"
)

type Breakpoint struct {
	ID   string         `json:"id"`
	Kind BreakpointKind `json:"kind"`
	// Selector carries whatever a non-line kind needs (a CSS selector, an event
	// name, a URL fragment). Empty for line breakpoints.
	Selector string   `json:"selector,omitempty"`
	Location Location `json:"location"`
	Enabled  bool     `json:"enabled"`

	Condition string `json:"condition,omitempty"`
	// HitCondition is the backend's own hit-count expression ("> 5", "100").
	HitCondition string        `json:"hit_condition,omitempty"`
	Suspend      SuspendPolicy `json:"suspend"`

	// Record is the list of expressions the backend evaluates on every hit
	// without a round trip to the agent. Backends that cannot do this declare
	// NonSuspendingTrace=suspend_only and the tool layer falls back to stopping.
	Record []string `json:"record,omitempty"`

	// HitCount is total; HitCountByUnit is per goroutine/thread where the
	// backend can tell them apart. The JetBrains plugin documents hitCount as
	// permanently 0 because the language-agnostic IDE API cannot see it; Delve
	// can, so the field is real here.
	HitCount       uint64            `json:"hit_count"`
	HitCountByUnit map[string]uint64 `json:"hit_count_by_unit,omitempty"`
}
