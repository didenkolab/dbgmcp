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
type Variable struct {
	// Name is the full path from the frame's root, usable verbatim as an
	// expression.
	Name  string `json:"name"`
	Type  string `json:"type,omitempty"`
	Value string `json:"value"`
	Kind  string `json:"kind,omitempty"`
	// Truncated says a budget cut this value short, so the agent knows to ask
	// for more rather than concluding the data is not there.
	Truncated bool `json:"truncated,omitempty"`
}
