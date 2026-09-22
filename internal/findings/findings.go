// Package findings reads a transcript and says what stands out in it.
//
// Everything here works on the neutral transcript and nothing else. That is not
// tidiness: it means a rule written once holds for Go, Python and every runtime
// added later, and that a rule can be tested without a debugger anywhere near
// it.
package findings

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// minSamples is how many hits a trend needs before a break in it means
// anything. Two points make a line through any two points.
const minSamples = 3

// Analyse returns what is notable about a transcript, most specific first.
func Analyse(t model.Transcript, probes []model.Probe) []model.Finding {
	var out []model.Finding

	for i, p := range probes {
		where := p.Location
		for _, never := range t.ProbesNeverHit {
			if matchesLocation(never, where) {
				out = append(out, model.Finding{
					Kind: model.FindingProbeNeverHit, Probe: i,
					File: where.File, Line: where.Line,
					Detail: "This probe never fired, so its absence from the transcript says nothing about the values there.",
				})
			}
		}
	}

	for _, series := range seriesOf(t) {
		out = append(out, series.analyse()...)
	}
	return out
}

func matchesLocation(text string, loc model.Location) bool {
	if loc.Symbol != "" && strings.Contains(text, loc.Symbol) {
		return true
	}
	return loc.File != "" && strings.Contains(text, loc.File) &&
		strings.Contains(text, strconv.Itoa(loc.Line))
}

// sample is one recorded value in the order it was recorded.
type sample struct {
	hit   int
	value string
	file  string
	line  int
}

// series is every value one expression took at one probe in one execution unit.
//
// All three keys matter. Two probes on the same expression are two stories.
// Two goroutines running the same loop are two more, and splicing them would
// invent trends that exist in neither.
//
// What this cannot separate is two sequential calls of the same function in the
// same unit: those do land in one series, and a fresh call looks like a sudden
// reset. The rules below guard against reading that as a reversal, but the
// limitation is real and is why a finding is an observation rather than a
// verdict.
type series struct {
	probe      int
	unit       string
	expression string
	samples    []sample
}

func seriesOf(t model.Transcript) []series {
	index := map[string]*series{}
	var order []string
	for _, hit := range t.Hits {
		for expr, value := range hit.Values {
			key := strconv.Itoa(hit.Probe) + "\x00" + hit.UnitID + "\x00" + expr
			s, seen := index[key]
			if !seen {
				s = &series{probe: hit.Probe, unit: hit.UnitID, expression: expr}
				index[key] = s
				order = append(order, key)
			}
			s.samples = append(s.samples, sample{hit: hit.Hit, value: value, file: hit.File, line: hit.Line})
		}
	}
	out := make([]series, 0, len(order))
	for _, key := range order {
		out = append(out, *index[key])
	}
	return out
}

func (s series) analyse() []model.Finding {
	var out []model.Finding
	if f, found := s.monotonicBreak(); found {
		out = append(out, f)
	}
	if f, found := s.firstEmpty(); found {
		out = append(out, f)
	}
	if f, found := s.froze(); found {
		out = append(out, f)
	}
	if f, found := s.stepOutlier(); found {
		out = append(out, f)
	}
	return out
}

func (s series) at(i int) model.Finding {
	return model.Finding{
		Probe: s.probe, Expression: s.expression,
		Hit: s.samples[i].hit, File: s.samples[i].file, Line: s.samples[i].line,
	}
}

// monotonicBreak reports a value that had been moving one way and turned round.
func (s series) monotonicBreak() (model.Finding, bool) {
	nums, ok := s.numbers()
	if !ok || len(nums) < minSamples+1 {
		return model.Finding{}, false
	}
	rising := nums[1] > nums[0]
	for i := 1; i < len(nums); i++ {
		if nums[i] == nums[i-1] {
			continue
		}
		stillRising := nums[i] > nums[i-1]
		if stillRising == rising {
			continue
		}
		if i < minSamples {
			return model.Finding{}, false
		}
		// A value dropping back to where the series began is the signature of
		// the function being called again, not of the value reversing. Calling
		// that a break would be confidently wrong on every loop traced across
		// more than one invocation.
		if (rising && nums[i] <= nums[0]) || (!rising && nums[i] >= nums[0]) {
			return model.Finding{}, false
		}
		direction := "increasing"
		if !rising {
			direction = "decreasing"
		}
		f := s.at(i)
		f.Kind = model.FindingMonotonicBreak
		f.Detail = fmt.Sprintf("%s had been %s for %d hits and then reversed.", s.expression, direction, i)
		f.Evidence = s.evidenceAround(i)
		return f, true
	}
	return model.Finding{}, false
}

// firstEmpty reports the first time a value that had always been present was
// not.
func (s series) firstEmpty() (model.Finding, bool) {
	if len(s.samples) < 2 || isEmptyValue(s.samples[0].value) {
		return model.Finding{}, false
	}
	for i := 1; i < len(s.samples); i++ {
		if !isEmptyValue(s.samples[i].value) {
			continue
		}
		f := s.at(i)
		f.Kind = model.FindingFirstEmpty
		f.Detail = fmt.Sprintf("%s was %q for the first %d hits and then became %q.",
			s.expression, s.samples[i-1].value, i, s.samples[i].value)
		f.Evidence = s.evidenceAround(i)
		return f, true
	}
	return model.Finding{}, false
}

// isEmptyValue covers the several ways runtimes spell absence. A rule that
// understood only Go's would be useless the moment a second backend arrived,
// which is the mistake this package exists to avoid.
func isEmptyValue(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "nil", "none", "null", "undefined", "0", "\"\"", "''", "[]", "{}", "false":
		return true
	}
	return false
}

// froze reports a value that changed at every hit and then stopped.
func (s series) froze() (model.Finding, bool) {
	if len(s.samples) < minSamples*2 {
		return model.Finding{}, false
	}
	// Find where the last run of identical values begins.
	last := len(s.samples) - 1
	start := last
	for start > 0 && s.samples[start-1].value == s.samples[last].value {
		start--
	}
	frozen := last - start + 1
	if frozen < minSamples || start == 0 {
		return model.Finding{}, false
	}
	// Only interesting if it was genuinely changing before.
	for i := 1; i < start; i++ {
		if s.samples[i].value == s.samples[i-1].value {
			return model.Finding{}, false
		}
	}
	f := s.at(start)
	f.Kind = model.FindingValueFroze
	f.Detail = fmt.Sprintf("%s changed at every hit until %q, and then stayed the same for %d hits.",
		s.expression, s.samples[start].value, frozen)
	f.Evidence = s.evidenceAround(start)
	return f, true
}

// stepOutlier reports a numeric jump far out of line with the jumps before it.
func (s series) stepOutlier() (model.Finding, bool) {
	nums, ok := s.numbers()
	if !ok || len(nums) < minSamples+1 {
		return model.Finding{}, false
	}
	var steps []float64
	for i := 1; i < len(nums); i++ {
		steps = append(steps, abs(nums[i]-nums[i-1]))
	}
	// Compare each step with the median of the ones before it, which is not
	// thrown by the outlier itself the way a mean would be.
	for i := minSamples; i < len(steps); i++ {
		previous := median(append([]float64(nil), steps[:i]...))
		if previous == 0 || steps[i] < previous*8 {
			continue
		}
		f := s.at(i + 1)
		f.Kind = model.FindingStepOutlier
		f.Detail = fmt.Sprintf("%s changed by %s at this hit, against a typical change of %s before it.",
			s.expression, trim(steps[i]), trim(previous))
		f.Evidence = s.evidenceAround(i + 1)
		return f, true
	}
	return model.Finding{}, false
}

func (s series) numbers() ([]float64, bool) {
	out := make([]float64, 0, len(s.samples))
	for _, sm := range s.samples {
		n, err := strconv.ParseFloat(strings.TrimSpace(sm.value), 64)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// evidenceAround shows the values either side of the moment, because a claim a
// reader cannot check is a claim they have to take on trust.
func (s series) evidenceAround(i int) []string {
	first, last := i-2, i+1
	if first < 0 {
		first = 0
	}
	if last >= len(s.samples) {
		last = len(s.samples) - 1
	}
	out := make([]string, 0, last-first+1)
	for j := first; j <= last; j++ {
		out = append(out, fmt.Sprintf("hit %d: %s = %s", s.samples[j].hit, s.expression, s.samples[j].value))
	}
	return out
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values[len(values)/2]
}

func trim(f float64) string { return strconv.FormatFloat(f, 'g', 4, 64) }
