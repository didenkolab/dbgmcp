package model

// Location is either a file/line pair or a symbol, never both resolved at once
// on the way in. Backends that declare BreakpointBySymbol resolve Symbol
// themselves; the rest reject it by capability rather than by guessing.
//
// This is why an agent can break on "billing.Charge" without first reading the
// file and counting lines.
type Location struct {
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Symbol string `json:"symbol,omitempty"`
}

func (l Location) IsZero() bool { return l.File == "" && l.Symbol == "" }

type Frame struct {
	Index    int    `json:"index"`
	Function string `json:"function,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	// Label is the backend's own rendering when it has one. The JetBrains plugin
	// learned this the hard way: a frame's toString() is not what the IDE shows.
	Label string `json:"label,omitempty"`
}
