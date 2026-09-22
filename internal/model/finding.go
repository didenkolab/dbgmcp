package model

// Finding is something the server noticed in a transcript that a reader would
// probably want pointed out.
//
// A finding is a claim with evidence, never a verdict. "total stopped
// increasing at hit 3" is a fact about the recording; "that is the bug" is a
// conclusion only the reader can draw. Blurring the two would make the tool
// confidently wrong, which is worse than silent.
type Finding struct {
	// Kind is a stable identifier so a client can filter or rank without
	// parsing prose.
	Kind FindingKind `json:"kind"`
	// Expression is the recorded expression this is about, when it is about one.
	Expression string `json:"expression,omitempty"`
	Probe      int    `json:"probe"`
	// Hit is where it became true, so the reader can go straight there.
	Hit  int    `json:"hit,omitempty"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Detail states the observation in one sentence.
	Detail string `json:"detail"`
	// Evidence is the values the observation rests on, so it can be checked
	// rather than believed.
	Evidence []string `json:"evidence,omitempty"`
}

type FindingKind string

const (
	// FindingMonotonicBreak: a value that had been moving one way reversed.
	FindingMonotonicBreak FindingKind = "monotonic_break"
	// FindingFirstEmpty: a value that had always been present arrived empty,
	// nil or zero. In most languages that is where a chain of assumptions ends.
	FindingFirstEmpty FindingKind = "first_empty"
	// FindingTypeChanged: an expression reported a different type than before.
	// Rare and usually deliberate in Go; common and usually a mistake in
	// Python and JavaScript, which is why it is worth saying out loud.
	FindingTypeChanged FindingKind = "type_changed"
	// FindingValueFroze: a value that changed at every hit stopped changing.
	FindingValueFroze FindingKind = "value_froze"
	// FindingStepOutlier: a numeric step far out of line with the steps before
	// it.
	FindingStepOutlier FindingKind = "step_outlier"
	// FindingProbeNeverHit: an empty transcript and a misplaced probe look
	// identical without this.
	FindingProbeNeverHit FindingKind = "probe_never_hit"
)
