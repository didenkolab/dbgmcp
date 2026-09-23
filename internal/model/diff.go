package model

// Divergence is one place two runs stopped agreeing.
//
// Recording the kind separately from the values matters: "the value differed" and
// "one run did not reach here at all" and "the loop ran a different number of
// times" are three different findings, and flattening them into "these do not
// match" loses the part that says where to look.
type Divergence struct {
	Kind DivergenceKind `json:"kind"`
	// Probe and Expression locate it in the request; Hit locates it in time.
	Probe      int    `json:"probe"`
	Expression string `json:"expression,omitempty"`
	Hit        int    `json:"hit,omitempty"`
	// Unit identifies which execution unit the reading came from, as the order
	// in which units first arrived at the probe: "#0" is the first. Raw ids are
	// deliberately not used -- a goroutine or thread id is assigned within one
	// run and means nothing across two.
	Unit string `json:"unit,omitempty"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
	// Left and Right are the two readings, in the order the runs were given.
	Left  string `json:"left,omitempty"`
	Right string `json:"right,omitempty"`
	// Detail states it in one sentence.
	Detail string `json:"detail"`
	// Evidence is the readings either side, so the claim can be checked rather
	// than believed.
	Evidence []string `json:"evidence,omitempty"`
}

type DivergenceKind string

const (
	// DivergedValue: both runs had a value here and they differ.
	DivergedValue DivergenceKind = "value"
	// DivergedPresence: one run had a value and the other had none. Kept apart
	// from a value difference because the cause is usually different -- a branch
	// not taken rather than arithmetic gone wrong.
	DivergedPresence DivergenceKind = "presence"
	// DivergedHitCount: the two runs passed a probe a different number of times,
	// which is a divergence in control flow rather than in data.
	DivergedHitCount DivergenceKind = "hit_count"
	// DivergedReach: a probe fired in one run and never in the other.
	DivergedReach DivergenceKind = "reach"
	// DivergedUnitCount: a probe was reached by a different number of execution
	// units. Separate from a hit-count difference because the cause is
	// concurrency rather than a loop.
	DivergedUnitCount DivergenceKind = "unit_count"
)

// Comparison is the answer to "where did these two runs first disagree".
type Comparison struct {
	// First is the earliest divergence, and is the answer most of the time. The
	// ones after it are usually consequences.
	First *Divergence `json:"first,omitempty"`
	// Divergences is every one found, in order.
	Divergences []Divergence `json:"divergences"`
	// Compared is how many readings were lined up and checked, so "no
	// divergence" can be told apart from "nothing was compared".
	Compared int    `json:"compared"`
	Message  string `json:"message"`
	// PerturbsTiming is true when either run stopped at every hit, because then
	// a timing-dependent difference may be the observation rather than the bug.
	PerturbsTiming bool `json:"perturbs_timing"`
	// AmbiguousUnits names the probes where more than one execution unit was
	// recorded. There, units are matched between the runs by the order they
	// arrived, which is not a fact about the program: two runs can interleave
	// differently. Divergences at those probes are weaker evidence, and saying
	// so is the difference between a useful answer and a confident wrong one.
	AmbiguousUnits []string `json:"ambiguous_units,omitempty"`
}
