package model

// ValueBudget caps how much of a value graph crosses into the agent's context.
// Without it a single get_variables on a pointer-rich struct eats the window.
//
// Every backend maps this onto its own loading knobs (Delve: api.LoadConfig).
type ValueBudget struct {
	MaxDepth       int `json:"max_depth,omitempty"`
	MaxStringLen   int `json:"max_string_len,omitempty"`
	MaxArrayValues int `json:"max_array_values,omitempty"`
	MaxStructAttrs int `json:"max_struct_attrs,omitempty"`
}

// DefaultValueBudget is tuned for reading, not for completeness: deep enough to
// see through a pointer or two, shallow enough that one call is not a page.
func DefaultValueBudget() ValueBudget {
	return ValueBudget{MaxDepth: 2, MaxStringLen: 256, MaxArrayValues: 64, MaxStructAttrs: 32}
}

// WithDefaults fills only the fields the caller left at zero, so an agent can
// raise one limit without having to restate the others.
func (b ValueBudget) WithDefaults() ValueBudget {
	d := DefaultValueBudget()
	if b.MaxDepth == 0 {
		b.MaxDepth = d.MaxDepth
	}
	if b.MaxStringLen == 0 {
		b.MaxStringLen = d.MaxStringLen
	}
	if b.MaxArrayValues == 0 {
		b.MaxArrayValues = d.MaxArrayValues
	}
	if b.MaxStructAttrs == 0 {
		b.MaxStructAttrs = d.MaxStructAttrs
	}
	return b
}

// Variable is one value at one path, and the list of them is flat rather than
// a tree.
//
// Two reasons, both about the agent rather than about elegance. A nested JSON
// tree costs far more tokens than the same data as lines. And the flat form
// makes Name a dotted path -- "it.Price", "items[2].Name" -- which is exactly
// the expression to send to evaluate_expression next, so reading a value and
// acting on it stop being separate steps.
//
// The shape also happens to be expressible as a JSON Schema, which a recursive
// type is not.
// Presence says whether a value is there at all, separately from what it reads
// as.
//
// This exists because a bare string cannot carry the difference. "nil", "" and
// "the debugger could not read this" all rendered identically, which produced
// two real defects: a pointer's address was passed through as if it were data,
// and a nil error displayed as nothing at all -- in Go, the difference between
// "no error" and "I do not know". A comparison across runs would inherit the
// same confusion and report divergences that are not there.
type Presence string

const (
	// PresentValue means Value holds a real rendering of a real value.
	PresentValue Presence = "value"
	// PresentNil means the value is genuinely absent: a nil pointer, a None, an
	// undefined. Value says which spelling.
	PresentNil Presence = "nil"
	// PresentUnreadable means the debugger could not read it, and Value says
	// why. This is not the same as absent, and treating it as absent is how a
	// reader concludes something false.
	PresentUnreadable Presence = "unreadable"
	// PresentOutOfScope means the expression named nothing here.
	PresentOutOfScope Presence = "out_of_scope"
)

// Comparable reports whether two readings of this value can be meaningfully
// compared. An unreadable value is not equal or unequal to anything.
func (p Presence) Comparable() bool {
	return p == PresentValue || p == PresentNil
}

type Variable struct {
	// Name is the full path from the frame's root, usable verbatim as an
	// expression.
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	// Value is the rendering. Read Presence before drawing a conclusion from it.
	Value string `json:"value"`
	// Presence distinguishes a real value from an absent one and from one the
	// debugger could not read. Empty means PresentValue, so existing readers
	// are not broken by its arrival.
	Presence Presence `json:"presence,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	// Truncated says a budget cut this value short, so the agent knows to ask
	// for more rather than concluding the data is not there.
	Truncated bool `json:"truncated,omitempty"`
}
