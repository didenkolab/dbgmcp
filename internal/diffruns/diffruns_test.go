package diffruns

import (
	"strings"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// hit builds one recorded hit. Absent readings are given as "expr!reason".
func hit(probe, n int, unit string, readings ...string) model.TraceHit {
	h := model.TraceHit{
		Probe: probe, Hit: n, UnitID: unit, File: "main.go", Line: 10 + probe,
		Values: map[string]string{}, Absent: map[string]string{},
	}
	for _, r := range readings {
		if expr, reason, absent := strings.Cut(r, "!"); absent {
			h.Absent[expr] = reason
			continue
		}
		expr, value, _ := strings.Cut(r, "=")
		h.Values[expr] = value
	}
	return h
}

func transcript(hits ...model.TraceHit) model.Transcript {
	return model.Transcript{Status: model.TraceFinished, Hits: hits}
}

func TestIdenticalRunsDivergeNowhere(t *testing.T) {
	a := transcript(hit(0, 1, "g1", "n=1"), hit(0, 2, "g1", "n=2"))
	got := Compare(a, a, nil)

	if got.First != nil {
		t.Fatalf("identical transcripts diverged at %+v", *got.First)
	}
	if got.Compared != 2 {
		t.Errorf("compared = %d, want 2 -- a silent zero would make any pair look equal", got.Compared)
	}
}

func TestNothingComparedIsNotTheSameAsAgreement(t *testing.T) {
	got := Compare(transcript(), transcript(), nil)

	if got.First != nil {
		t.Fatalf("empty transcripts diverged: %+v", *got.First)
	}
	// The distinction that matters: two runs that recorded nothing have not
	// been shown to agree, and saying they agreed would be a false all-clear.
	if !strings.Contains(got.Message, "Nothing was compared") {
		t.Errorf("message = %q, want it to refuse to claim agreement", got.Message)
	}
}

func TestFirstDivergenceIsTheEarliestHitNotTheFirstFound(t *testing.T) {
	left := transcript(
		hit(0, 1, "g1", "n=1"), hit(0, 2, "g1", "n=2"), hit(0, 3, "g1", "n=3"),
	)
	right := transcript(
		hit(0, 1, "g1", "n=1"), hit(0, 2, "g1", "n=99"), hit(0, 3, "g1", "n=98"),
	)
	got := Compare(left, right, nil)

	if got.First == nil {
		t.Fatal("no divergence reported for differing runs")
	}
	if got.First.Hit != 2 {
		t.Errorf("first divergence at hit %d, want 2 -- later ones are consequences", got.First.Hit)
	}
	if got.First.Left != "2" || got.First.Right != "99" {
		t.Errorf("readings = %q vs %q, want 2 vs 99", got.First.Left, got.First.Right)
	}
	if got.First.Kind != model.DivergedValue {
		t.Errorf("kind = %q, want %q", got.First.Kind, model.DivergedValue)
	}
	if len(got.Divergences) != 2 {
		t.Errorf("reported %d divergences, want 2", len(got.Divergences))
	}
	// Evidence must show the agreeing hit before the split, which is what makes
	// the claim checkable rather than a bare pair of numbers.
	joined := strings.Join(got.First.Evidence, "\n")
	if !strings.Contains(joined, "hit 1: n = 1 | 1") {
		t.Errorf("evidence lacks the run-up:\n%s", joined)
	}
}

func TestAbsenceIsNotAValueDifference(t *testing.T) {
	// In Go this is the difference between "err was nil" and "err was set".
	// Reporting it as a value mismatch sends a reader to look at arithmetic.
	left := transcript(hit(0, 1, "g1", "err!nil"))
	right := transcript(hit(0, 1, "g1", "err=EOF"))
	got := Compare(left, right, nil)

	if got.First == nil {
		t.Fatal("nil against a value did not diverge")
	}
	if got.First.Kind != model.DivergedPresence {
		t.Errorf("kind = %q, want %q", got.First.Kind, model.DivergedPresence)
	}
	if got.First.Left != "nil" || got.First.Right != "EOF" {
		t.Errorf("readings = %q vs %q, want nil vs EOF", got.First.Left, got.First.Right)
	}
}

func TestTheSameKindOfAbsenceOnBothSidesAgrees(t *testing.T) {
	// The reason the presence refactor came before this tool: with absence
	// flattened into the value string, "nil" on both sides still agrees, but
	// "nil" against "unreadable" must not.
	both := transcript(hit(0, 1, "g1", "err!nil"))
	if got := Compare(both, both, nil); got.First != nil {
		t.Fatalf("nil against nil diverged: %+v", *got.First)
	}

	unreadable := transcript(hit(0, 1, "g1", "err!unreadable"))
	got := Compare(both, unreadable, nil)
	if got.First == nil {
		t.Fatal("nil against unreadable agreed -- that conflates 'no error' with 'I could not tell'")
	}
	if got.First.Kind != model.DivergedPresence {
		t.Errorf("kind = %q, want %q", got.First.Kind, model.DivergedPresence)
	}
}

func TestADifferentNumberOfHitsIsAControlFlowDivergence(t *testing.T) {
	left := transcript(hit(0, 1, "g1", "n=1"), hit(0, 2, "g1", "n=2"))
	right := transcript(hit(0, 1, "g1", "n=1"))
	got := Compare(left, right, nil)

	if got.First == nil {
		t.Fatal("a differing hit count did not diverge")
	}
	if got.First.Kind != model.DivergedHitCount {
		t.Errorf("kind = %q, want %q", got.First.Kind, model.DivergedHitCount)
	}
	if got.First.Left != "2" || got.First.Right != "1" {
		t.Errorf("counts = %q vs %q, want 2 vs 1", got.First.Left, got.First.Right)
	}
	// The shared prefix must still be compared, not discarded with the count.
	if got.Compared != 1 {
		t.Errorf("compared = %d, want 1", got.Compared)
	}
}

func TestAProbeReachedInOnlyOneRun(t *testing.T) {
	left := transcript(hit(0, 1, "g1", "n=1"), hit(1, 1, "g1", "why=taken"))
	right := transcript(hit(0, 1, "g1", "n=1"))
	got := Compare(left, right, nil)

	if got.First == nil {
		t.Fatal("an unreached probe did not diverge")
	}
	if got.First.Kind != model.DivergedReach {
		t.Errorf("kind = %q, want %q", got.First.Kind, model.DivergedReach)
	}
	if !strings.Contains(got.First.Detail, "never in the second") {
		t.Errorf("detail = %q, want it to name which run missed it", got.First.Detail)
	}
}

func TestUnitIdsAreNotComparedAcrossRuns(t *testing.T) {
	// The bug the live test caught and the unit tests missed: a goroutine or
	// thread id is assigned within one run. The same code ran as goroutine 6 in
	// one run and 20 in the next, and keying on the id made every single series
	// read as "recorded in one run and never in the other" -- a total failure
	// dressed up as six findings.
	left := transcript(hit(0, 1, "6", "n=1"), hit(0, 2, "6", "n=2"))
	right := transcript(hit(0, 1, "20", "n=1"), hit(0, 2, "20", "n=2"))

	got := Compare(left, right, nil)
	if got.First != nil {
		t.Fatalf("differing unit ids produced a divergence: %+v", *got.First)
	}
	if got.Compared != 2 {
		t.Errorf("compared = %d, want 2", got.Compared)
	}
	if len(got.AmbiguousUnits) != 0 {
		t.Errorf("one unit per run is not ambiguous, but got %v", got.AmbiguousUnits)
	}
}

func TestTwoUnitsAtOneProbeAreNotMergedIntoOneSeries(t *testing.T) {
	// Two goroutines at one probe are two series. Merged into one, the readings
	// would be ordered by arrival -- and two runs interleave differently, so
	// every run of a concurrent program would look like it diverged.
	interleaved := transcript(
		hit(0, 1, "g1", "n=1"), hit(0, 1, "g2", "n=500"),
		hit(0, 2, "g1", "n=2"), hit(0, 2, "g2", "n=501"),
	)
	blocked := transcript(
		hit(0, 1, "g1", "n=1"), hit(0, 2, "g1", "n=2"),
		hit(0, 1, "g2", "n=500"), hit(0, 2, "g2", "n=501"),
	)

	got := Compare(interleaved, blocked, nil)
	if got.First != nil {
		t.Fatalf("a different interleaving diverged at %+v", *got.First)
	}
	if got.Compared != 4 {
		t.Errorf("compared = %d, want 4 -- both units in both runs", got.Compared)
	}
	// Agreement and the caveat must coexist: the units were matched by arrival
	// order, and that stays true whether or not anything diverged.
	if len(got.AmbiguousUnits) != 1 {
		t.Fatalf("ambiguous_units = %v, want one entry for probe 0", got.AmbiguousUnits)
	}
	if !strings.Contains(got.AmbiguousUnits[0], "order of arrival") {
		t.Errorf("the caveat does not say how units were matched: %q", got.AmbiguousUnits[0])
	}
}

func TestMatchingSeveralUnitsIsReportedAsAGuess(t *testing.T) {
	// With more than one unit at a probe the matching is not a fact about the
	// program. Presenting a divergence there as settled would be the worst
	// failure this tool can have: confidently wrong about a race.
	left := transcript(hit(0, 1, "g1", "n=1"), hit(0, 1, "g2", "n=2"))
	right := transcript(hit(0, 1, "g9", "n=2"), hit(0, 1, "g8", "n=1"))

	got := Compare(left, right, nil)
	if got.First == nil {
		t.Fatal("arrival-order matching found no divergence, so the caveat cannot be checked")
	}
	if len(got.AmbiguousUnits) == 0 {
		t.Fatal("two units at a probe were not reported as ambiguous")
	}
	if !strings.Contains(got.Message, "interleave differently") {
		t.Errorf("message states the divergence without the caveat: %q", got.Message)
	}
}

func TestADifferentNumberOfUnitsIsItsOwnFinding(t *testing.T) {
	// "One goroutine instead of two" is a divergence in concurrency. Left as a
	// value comparison it appears as one unit's readings mysteriously absent.
	left := transcript(hit(0, 1, "g1", "n=1"), hit(0, 1, "g2", "n=1"))
	right := transcript(hit(0, 1, "g1", "n=1"))

	got := Compare(left, right, nil)
	if got.First == nil {
		t.Fatal("a differing unit count did not diverge")
	}
	if got.First.Kind != model.DivergedUnitCount {
		t.Errorf("first divergence is %q, want %q", got.First.Kind, model.DivergedUnitCount)
	}
	if got.First.Left != "2" || got.First.Right != "1" {
		t.Errorf("unit counts = %q vs %q, want 2 vs 1", got.First.Left, got.First.Right)
	}
}

func TestTheAnswerIsTheSameEveryTime(t *testing.T) {
	// Series live in a map. Without a deterministic order over it, the same
	// pair of runs would report different "first" divergences between calls,
	// and no report could be trusted or reproduced.
	left := transcript(hit(0, 1, "g1", "a=1", "b=1", "c=1", "d=1", "e=1"))
	right := transcript(hit(0, 1, "g1", "a=9", "b=9", "c=9", "d=9", "e=9"))

	first := Compare(left, right, nil).First
	for i := 0; i < 50; i++ {
		got := Compare(left, right, nil).First
		if got.Expression != first.Expression {
			t.Fatalf("run %d reported %q, first run reported %q", i, got.Expression, first.Expression)
		}
	}
}

func TestTimingPerturbationCarriesIntoTheComparison(t *testing.T) {
	// A difference between two stop-the-world runs may be the observation
	// rather than the bug, and the reader has to be told.
	left := transcript(hit(0, 1, "g1", "n=1"))
	left.PerturbsTiming = true
	right := transcript(hit(0, 1, "g1", "n=2"))

	if got := Compare(left, right, nil); !got.PerturbsTiming {
		t.Error("perturbs_timing did not carry into the comparison")
	}
}

func TestEveryDetailUsesThePhrasesTheReportSubstitutes(t *testing.T) {
	// The report replaces "the first"/"the second" with the caller's own names
	// for the two runs. A detail worded any other way would silently keep the
	// neutral phrasing in an artifact whose table is labelled, leaving the reader
	// to work out which column is which.
	cases := map[string]model.Comparison{
		"value":      Compare(transcript(hit(0, 1, "g1", "n=1")), transcript(hit(0, 1, "g1", "n=2")), nil),
		"presence":   Compare(transcript(hit(0, 1, "g1", "err!nil")), transcript(hit(0, 1, "g1", "err=EOF")), nil),
		"hit_count":  Compare(transcript(hit(0, 1, "g1", "n=1"), hit(0, 2, "g1", "n=2")), transcript(hit(0, 1, "g1", "n=1")), nil),
		"reach":      Compare(transcript(hit(0, 1, "g1", "n=1"), hit(1, 1, "g1", "why=x")), transcript(hit(0, 1, "g1", "n=1")), nil),
		"unit_count": Compare(transcript(hit(0, 1, "g1", "n=1"), hit(0, 1, "g2", "n=1")), transcript(hit(0, 1, "g1", "n=1")), nil),
	}
	for kind, got := range cases {
		if len(got.Divergences) == 0 {
			t.Errorf("%s: produced no divergence, so its wording is untested", kind)
			continue
		}
		for _, d := range got.Divergences {
			if !strings.Contains(d.Detail, "the first") || !strings.Contains(d.Detail, "the second") {
				t.Errorf("%s detail does not name both runs in the substitutable form: %q", kind, d.Detail)
			}
		}
	}
}
