package model

import "time"

// OutputChunk is a piece of what the debuggee printed.
//
// Outside an IDE there is no console to glance at, so a program's own output --
// often the fastest route to what went wrong -- would otherwise be invisible to
// the agent entirely.
type OutputChunk struct {
	// Seq orders chunks across both streams and doubles as the cursor for
	// reading only what is new.
	Seq    int       `json:"seq"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// OutputPage is a window onto the buffer, with enough context that an agent can
// tell "nothing new" from "I missed some".
type OutputPage struct {
	Chunks    []OutputChunk `json:"chunks"`
	NextSince int           `json:"next_since"`
	// Dropped counts chunks discarded because the buffer wrapped. A silent drop
	// would let an agent conclude a program printed nothing when it printed too
	// much.
	Dropped int `json:"dropped"`
}
