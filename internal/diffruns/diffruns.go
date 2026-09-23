// Package diffruns compares two transcripts and says where they first stopped
// agreeing.
//
// This answers the question findings structurally cannot. Findings notices an
// anomalous shape -- a trend that broke, a value that froze -- but nothing in one
// transcript says what the answer should have been. A second run does: the
// passing one is the specification, and the first place the failing one departs
// from it is the bug's neighbourhood.
package diffruns

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// Compare lines the two transcripts up and reports the disagreements.
//
// Alignment is by probe, execution unit and hit ordinal -- not by wall-clock
// order, which differs between runs for reasons that are never the bug. A
// different number of hits is reported rather than silently truncating the
// longer run, because "the loop ran four times instead of three" is frequently
// the whole answer.
func Compare(left, right model.Transcript, probes []model.Probe) model.Comparison {
	out := model.Comparison{
		Divergences:    []model.Divergence{},
		PerturbsTiming: left.PerturbsTiming || right.PerturbsTiming,
	}

	leftRun, rightRun := index(left), index(right)
	out.AmbiguousUnits = ambiguities(leftRun, rightRun)
	out.Divergences = append(out.Divergences, unitCountDivergences(leftRun, rightRun)...)

	for _, k := range unionKeys(leftRun.series, rightRun.series) {
		l, inLeft := leftRun.series[k]
		r, inRight := rightRun.series[k]

		// A probe reached in one run and not the other is a divergence in
		// control flow, and reporting it as a value mismatch would send a reader
		// looking at arithmetic.
		if !inLeft || !inRight {
			reached, missing := "the first", "the second"
			present := l
			if !inLeft {
				reached, missing = "the second", "the first"
				present = r
			}
			out.Divergences = append(out.Divergences, model.Divergence{
				Kind: model.DivergedReach, Probe: present.probe, Expression: present.expression,
				Unit: present.unit, File: present.file, Line: present.line,
				Detail: fmt.Sprintf("%s was recorded in %s run and never in %s.",
					present.expression, reached, missing),
			})
			continue
		}

		if len(l.readings) != len(r.readings) {
			out.Divergences = append(out.Divergences, model.Divergence{
				Kind: model.DivergedHitCount, Probe: l.probe, Expression: l.expression,
				Unit: l.unit, File: l.file, Line: l.line,
				Left: strconv.Itoa(len(l.readings)), Right: strconv.Itoa(len(r.readings)),
				Detail: fmt.Sprintf("%s was recorded %d time(s) in the first run and %d in the second, so the two took different paths.",
					l.expression, len(l.readings), len(r.readings)),
			})
		}

		shared := min(len(l.readings), len(r.readings))
		for i := 0; i < shared; i++ {
			lr, rr := l.readings[i], r.readings[i]
			out.Compared++
			if d, diverged := compareReading(l, r, i, lr, rr); diverged {
				out.Divergences = append(out.Divergences, d)
			}
		}
	}

	sort.SliceStable(out.Divergences, func(i, j int) bool {
		if out.Divergences[i].Probe != out.Divergences[j].Probe {
			return out.Divergences[i].Probe < out.Divergences[j].Probe
		}
		return out.Divergences[i].Hit < out.Divergences[j].Hit
	})
	if len(out.Divergences) > 0 {
		first := out.Divergences[0]
		out.First = &first
	}
	out.Message = summarise(out)
	return out
}

func compareReading(l, r *series, i int, lr, rr reading) (model.Divergence, bool) {
	at := model.Divergence{
		Probe: l.probe, Expression: l.expression, Hit: lr.hit,
		Unit: l.unit, File: lr.file, Line: lr.line,
		Evidence: evidence(l, r, i),
	}
	switch {
	case lr.absent == "" && rr.absent == "":
		if lr.value == rr.value {
			return model.Divergence{}, false
		}
		at.Kind = model.DivergedValue
		at.Left, at.Right = lr.value, rr.value
	case lr.absent == rr.absent:
		return model.Divergence{}, false
	default:
		// One run had a value and the other did not. This is not a value
		// difference: it usually means a branch was not taken.
		at.Kind = model.DivergedPresence
		at.Left, at.Right = describe(lr), describe(rr)
	}
	at.Detail = fmt.Sprintf("At hit %d, %s was %s in the first run and %s in the second.",
		lr.hit, l.expression, at.Left, at.Right)
	return at, true
}

// unitCountDivergences reports probes reached by a different number of execution
// units. That is a difference in concurrency, and folding it into the value
// comparison would surface it as one unit's readings mysteriously missing.
func unitCountDivergences(left, right *run) []model.Divergence {
	var out []model.Divergence
	for _, probe := range unionProbes(left, right) {
		l, r := len(left.units[probe]), len(right.units[probe])
		if l == r || l == 0 || r == 0 {
			// Zero on one side is a reach divergence, already reported as such.
			continue
		}
		out = append(out, model.Divergence{
			Kind: model.DivergedUnitCount, Probe: probe,
			File: left.where[probe].file, Line: left.where[probe].line,
			Left: strconv.Itoa(l), Right: strconv.Itoa(r),
			Detail: fmt.Sprintf("Probe %d was reached by %d execution unit(s) in the first run and %d in the second.",
				probe, l, r),
		})
	}
	return out
}

// ambiguities names the probes where matching units between the runs is a guess.
func ambiguities(left, right *run) []string {
	var out []string
	for _, probe := range unionProbes(left, right) {
		l, r := len(left.units[probe]), len(right.units[probe])
		if l <= 1 && r <= 1 {
			continue
		}
		out = append(out, fmt.Sprintf("probe %d (%s:%d): %d unit(s) in the first run and %d in the second, matched by order of arrival",
			probe, left.where[probe].file, left.where[probe].line, l, r))
	}
	return out
}

func describe(r reading) string {
	if r.absent != "" {
		return r.absent
	}
	return r.value
}

func summarise(c model.Comparison) string {
	var msg string
	switch {
	case c.Compared == 0 && len(c.Divergences) == 0:
		return "Nothing was compared: neither run recorded anything, so this says nothing about the runs."
	case c.First == nil:
		msg = fmt.Sprintf("The two runs agreed on all %d readings. The difference between them is not visible at these probes.", c.Compared)
	default:
		msg = fmt.Sprintf("The runs first disagreed at %s. %d divergence(s) in total across %d readings; the later ones are often consequences of the first.",
			location(*c.First), len(c.Divergences), c.Compared)
	}
	if len(c.AmbiguousUnits) > 0 {
		msg += " Several execution units were recorded at the same probe, so units were matched between the runs by the order they arrived rather than by identity -- see ambiguous_units. Two runs can interleave differently, which makes a divergence there weaker evidence."
	}
	return msg
}

func location(d model.Divergence) string {
	if d.File == "" {
		return d.Expression
	}
	return fmt.Sprintf("%s:%d (%s)", d.File, d.Line, d.Expression)
}

// reading is one recorded value in one run.
type reading struct {
	hit    int
	value  string
	absent string
	file   string
	line   int
}

// series is every reading of one expression at one probe by one execution unit.
type series struct {
	probe      int
	unit       string
	expression string
	file       string
	line       int
	readings   []reading
}

type place struct {
	file string
	line int
}

// run is one transcript turned into comparable series.
type run struct {
	series map[string]*series
	// units lists, per probe, the raw unit ids in the order they first arrived.
	// Their position -- not their value -- is what aligns them with the other
	// run, because an id is assigned within a run and means nothing outside it.
	units map[int][]string
	where map[int]place
}

func key(probe, unitSlot int, expression string) string {
	return strconv.Itoa(probe) + "\x00" + strconv.Itoa(unitSlot) + "\x00" + expression
}

func index(t model.Transcript) *run {
	r := &run{series: map[string]*series{}, units: map[int][]string{}, where: map[int]place{}}

	slotOf := func(probe int, unit string) int {
		for i, known := range r.units[probe] {
			if known == unit {
				return i
			}
		}
		r.units[probe] = append(r.units[probe], unit)
		return len(r.units[probe]) - 1
	}

	add := func(h model.TraceHit, slot int, expr, value, absent string) {
		k := key(h.Probe, slot, expr)
		s, seen := r.series[k]
		if !seen {
			s = &series{
				probe: h.Probe, unit: "#" + strconv.Itoa(slot), expression: expr,
				file: h.File, line: h.Line,
			}
			r.series[k] = s
		}
		s.readings = append(s.readings, reading{
			hit: h.Hit, value: value, absent: absent, file: h.File, line: h.Line,
		})
	}

	for _, h := range t.Hits {
		slot := slotOf(h.Probe, h.UnitID)
		if _, known := r.where[h.Probe]; !known {
			r.where[h.Probe] = place{file: h.File, line: h.Line}
		}
		// Sorted so a hit recording several expressions produces its series in a
		// fixed order regardless of map iteration.
		for _, expr := range sortedKeys(h.Values) {
			add(h, slot, expr, h.Values[expr], "")
		}
		for _, expr := range sortedKeys(h.Absent) {
			add(h, slot, expr, h.Absent[expr], h.Absent[expr])
		}
	}
	return r
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unionKeys gives a stable order over both runs, so the same pair of transcripts
// always produces the same first divergence.
func unionKeys(left, right map[string]*series) []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(left)+len(right))
	for k := range left {
		seen[k] = true
		keys = append(keys, k)
	}
	for k := range right {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func unionProbes(left, right *run) []int {
	seen := map[int]bool{}
	var probes []int
	for _, r := range []*run{left, right} {
		for probe := range r.units {
			if !seen[probe] {
				seen[probe] = true
				probes = append(probes, probe)
			}
		}
	}
	sort.Ints(probes)
	return probes
}

// evidence shows the readings either side of the divergence in both runs, so a
// reader can see the run-up rather than a single pair of numbers.
func evidence(l, r *series, i int) []string {
	first := max(i-1, 0)
	var out []string
	for j := first; j <= i+1; j++ {
		if j >= len(l.readings) || j >= len(r.readings) {
			break
		}
		out = append(out, fmt.Sprintf("hit %d: %s = %s | %s",
			l.readings[j].hit, l.expression, describe(l.readings[j]), describe(r.readings[j])))
	}
	return out
}
