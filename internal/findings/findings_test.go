package findings

import (
	"strings"
	"testing"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// transcriptOf builds a transcript of one expression at one probe, which is the
// shape every rule reads.
func transcriptOf(expr string, values ...string) model.Transcript {
	t := model.Transcript{}
	for i, v := range values {
		t.Hits = append(t.Hits, model.TraceHit{
			Probe: 0, Hit: i + 1, File: "cart.go", Line: 26,
			Values: map[string]string{expr: v},
		})
	}
	return t
}

func kinds(fs []model.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f.Kind))
	}
	return out
}

func has(fs []model.Finding, kind model.FindingKind) *model.Finding {
	for i := range fs {
		if fs[i].Kind == kind {
			return &fs[i]
		}
	}
	return nil
}

func TestReportsWhereAnIncreasingValueTurnedRound(t *testing.T) {
	got := Analyse(transcriptOf("total", "10", "25", "40", "55", "31"), nil)

	f := has(got, model.FindingMonotonicBreak)
	if f == nil {
		t.Fatalf("no monotonic break reported; got %v", kinds(got))
	}
	if f.Hit != 5 {
		t.Errorf("reported hit %d, expected the hit where it reversed (5)", f.Hit)
	}
	// A claim the reader cannot check is a claim they must take on trust.
	if len(f.Evidence) < 2 {
		t.Errorf("no evidence attached: %+v", f.Evidence)
	}
	if !strings.Contains(strings.Join(f.Evidence, " "), "55") {
		t.Errorf("evidence does not show the values around the break: %v", f.Evidence)
	}
}

func TestDoesNotCallTwoPointsATrend(t *testing.T) {
	// Any two points are monotonic, and a rule that says so on every short
	// series is noise that trains a reader to ignore findings.
	if got := Analyse(transcriptOf("n", "1", "5", "2"), nil); has(got, model.FindingMonotonicBreak) != nil {
		t.Errorf("reported a trend break from three samples: %v", kinds(got))
	}
}

func TestSaysNothingAboutAValueThatSimplyVaries(t *testing.T) {
	// The commonest false positive available: ordinary noisy data.
	got := Analyse(transcriptOf("jitter", "4", "9", "2", "7", "3", "8"), nil)
	if len(got) != 0 {
		t.Errorf("reported findings about ordinary variation: %v", kinds(got))
	}
}

// transcriptWithGap records real values and then one the debugger reported as
// absent, which is how a backend now reports nil rather than as a string.
func transcriptWithGap(expr, reason string, values ...string) model.Transcript {
	t := transcriptOf(expr, values...)
	t.Hits = append(t.Hits, model.TraceHit{
		Probe: 0, Hit: len(values) + 1, File: "cart.go", Line: 26,
		Values: map[string]string{},
		Absent: map[string]string{expr: reason},
	})
	return t
}

func TestReportsTheFirstTimeAValueWentMissing(t *testing.T) {
	got := Analyse(transcriptWithGap("user", "nil", "alice", "bob", "carol"), nil)

	f := has(got, model.FindingFirstAbsent)
	if f == nil {
		t.Fatalf("no first-absent reported; got %v", kinds(got))
	}
	if f.Hit != 4 {
		t.Errorf("reported hit %d, expected 4", f.Hit)
	}
	if !strings.Contains(f.Detail, "nil") {
		t.Errorf("the detail does not say what kind of absence it was: %s", f.Detail)
	}
}

func TestAGapIsNotTheSameAsUnreadable(t *testing.T) {
	// "the debugger could not read this" is not "there is nothing here", and a
	// reader told the wrong one draws the wrong conclusion.
	got := Analyse(transcriptWithGap("cfg", "unreadable", "a", "b", "c"), nil)
	f := has(got, model.FindingFirstAbsent)
	if f == nil || !strings.Contains(f.Detail, "unreadable") {
		t.Fatalf("the reason for the gap was lost: %+v", f)
	}
}

func TestACounterReachingZeroIsAValueNotAGap(t *testing.T) {
	// This is the false alarm the old rule manufactured: zero spelled the same
	// as nil, so an honest countdown was reported as having gone missing.
	got := Analyse(transcriptOf("remaining", "3", "2", "1", "0"), nil)

	if has(got, model.FindingFirstAbsent) != nil {
		t.Errorf("read a zero as an absent value: %v", kinds(got))
	}
	if has(got, model.FindingFirstZero) == nil {
		t.Errorf("a counter reaching zero for the first time was not reported: %v", kinds(got))
	}
}

func TestNoTrendIsInventedAcrossAGap(t *testing.T) {
	// A missing value must not be read as a zero, or the series grows a cliff
	// that never happened.
	got := Analyse(transcriptWithGap("total", "nil", "10", "20", "30", "40"), nil)
	for _, f := range got {
		if f.Kind == model.FindingStepOutlier || f.Kind == model.FindingMonotonicBreak {
			t.Errorf("invented %s across a gap: %s", f.Kind, f.Detail)
		}
	}
}

func TestAbsenceIsClassifiedByTheBackendNotGuessedHere(t *testing.T) {
	// Each runtime spells absence differently, and this package used to guess
	// from the text -- which is how "0" ended up counting as missing. The
	// backends classify it now, and every spelling arrives as one fact.
	for _, reason := range []string{"nil", "unreadable", "out_of_scope"} {
		got := Analyse(transcriptWithGap("v", reason, "7", "8"), nil)
		if has(got, model.FindingFirstAbsent) == nil {
			t.Errorf("a gap reported as %q was not noticed", reason)
		}
	}
}

func TestOneReadingBeforeAZeroIsNotAPattern(t *testing.T) {
	// "never been zero in 1 hit" is a pair, not a trend, and reporting it is the
	// noise that teaches a reader to skip findings entirely.
	got := Analyse(transcriptOf("msd", "1", "0", "31", "20"), nil)
	if f := has(got, model.FindingFirstZero); f != nil {
		t.Errorf("reported a zero with one reading behind it: %s", f.Detail)
	}
}

func TestIgnoresAValueThatStartedAtZero(t *testing.T) {
	// An accumulator starting at zero is not news.
	got := Analyse(transcriptOf("total", "0", "0", "30", "110"), nil)
	if has(got, model.FindingFirstZero) != nil {
		t.Errorf("reported a value that was zero from the start: %v", kinds(got))
	}
}

func TestReportsAValueThatStoppedMoving(t *testing.T) {
	got := Analyse(transcriptOf("cursor", "1", "2", "3", "9", "9", "9", "9"), nil)

	f := has(got, model.FindingValueFroze)
	if f == nil {
		t.Fatalf("no freeze reported; got %v", kinds(got))
	}
	if !strings.Contains(f.Detail, "9") {
		t.Errorf("the detail does not name the value it froze at: %s", f.Detail)
	}
}

func TestReportsAStepFarOutOfLineWithTheRest(t *testing.T) {
	got := Analyse(transcriptOf("balance", "100", "110", "120", "130", "9000"), nil)

	if has(got, model.FindingStepOutlier) == nil {
		t.Fatalf("no step outlier reported; got %v", kinds(got))
	}
}

func TestSteadyGrowthIsNotAnOutlier(t *testing.T) {
	got := Analyse(transcriptOf("n", "10", "20", "30", "40", "50", "60"), nil)
	if has(got, model.FindingStepOutlier) != nil {
		t.Errorf("reported an outlier in an even series: %v", kinds(got))
	}
}

func TestProbesThatNeverFiredAreSaidOutLoud(t *testing.T) {
	// An empty transcript and a misplaced probe are the same output and
	// completely different problems.
	tr := model.Transcript{ProbesNeverHit: []string{"billing.Charge"}}
	got := Analyse(tr, []model.Probe{{Location: model.Location{Symbol: "billing.Charge"}}})

	f := has(got, model.FindingProbeNeverHit)
	if f == nil {
		t.Fatalf("a probe that never fired was not reported; got %v", kinds(got))
	}
	if !strings.Contains(f.Detail, "says nothing") {
		t.Errorf("the detail does not warn against reading absence as evidence: %s", f.Detail)
	}
}

func TestKeepsProbesApart(t *testing.T) {
	// The same expression traced in two places is two stories, and splicing
	// them would invent trends that exist in neither.
	tr := model.Transcript{Hits: []model.TraceHit{
		{Probe: 0, Hit: 1, Values: map[string]string{"n": "1"}},
		{Probe: 1, Hit: 1, Values: map[string]string{"n": "100"}},
		{Probe: 0, Hit: 2, Values: map[string]string{"n": "2"}},
		{Probe: 1, Hit: 2, Values: map[string]string{"n": "99"}},
		{Probe: 0, Hit: 3, Values: map[string]string{"n": "3"}},
		{Probe: 1, Hit: 3, Values: map[string]string{"n": "98"}},
		{Probe: 0, Hit: 4, Values: map[string]string{"n": "4"}},
		{Probe: 1, Hit: 4, Values: map[string]string{"n": "97"}},
	}}
	if got := Analyse(tr, nil); len(got) != 0 {
		t.Errorf("two clean opposite trends produced findings: %v", kinds(got))
	}
}

func TestAnEmptyTranscriptProducesNothing(t *testing.T) {
	if got := Analyse(model.Transcript{}, nil); len(got) != 0 {
		t.Errorf("findings from nothing: %v", kinds(got))
	}
}

func TestASecondCallIsNotAReversal(t *testing.T) {
	// A loop traced across two invocations produces 0,30,110 then 0,30,110.
	// Reading the reset as a trend reversal would be confidently wrong on
	// almost every real trace, which is worse than saying nothing.
	got := Analyse(transcriptOf("total", "0", "30", "110", "0", "30", "110"), nil)
	if f := has(got, model.FindingMonotonicBreak); f != nil {
		t.Errorf("read a fresh invocation as a reversal: %s", f.Detail)
	}
}

func TestConcurrentUnitsAreSeparateStories(t *testing.T) {
	// Two goroutines running the same loop must not be spliced into one series.
	tr := model.Transcript{Hits: []model.TraceHit{
		{Probe: 0, Hit: 1, UnitID: "1", Values: map[string]string{"n": "1"}},
		{Probe: 0, Hit: 2, UnitID: "2", Values: map[string]string{"n": "50"}},
		{Probe: 0, Hit: 3, UnitID: "1", Values: map[string]string{"n": "2"}},
		{Probe: 0, Hit: 4, UnitID: "2", Values: map[string]string{"n": "51"}},
		{Probe: 0, Hit: 5, UnitID: "1", Values: map[string]string{"n": "3"}},
		{Probe: 0, Hit: 6, UnitID: "2", Values: map[string]string{"n": "52"}},
		{Probe: 0, Hit: 7, UnitID: "1", Values: map[string]string{"n": "4"}},
		{Probe: 0, Hit: 8, UnitID: "2", Values: map[string]string{"n": "53"}},
	}}
	if got := Analyse(tr, nil); len(got) != 0 {
		t.Errorf("two clean interleaved trends produced findings: %v", kinds(got))
	}
}
