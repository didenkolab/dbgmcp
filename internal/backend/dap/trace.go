package dap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// Trace records expressions at a set of probes using logpoints.
//
// DAP has no equivalent of Delve's breakpoint-side expression list, but it does
// have a logpoint: a breakpoint that formats a message and carries on. That is
// enough to keep the agent out of the loop, which is the expensive part. It is
// not enough to keep the debuggee running undisturbed, so this backend reports
// the mode it actually achieved rather than the one it would prefer.
func (b *Backend) Trace(ctx context.Context, probes []model.Probe, timeout time.Duration) (model.Transcript, error) {
	if len(probes) == 0 {
		return model.Transcript{}, fmt.Errorf("a trace needs at least one probe")
	}
	caps := b.Capabilities()
	if caps.TraceMode == backend.TraceSuspendOnly {
		return model.Transcript{}, backend.Unsupported(b.Name(), "logpoints",
			"This adapter cannot record without stopping. Set breakpoints and inspect at each stop instead.")
	}

	created := make([]string, 0, len(probes))
	defer func() {
		for _, id := range created {
			_ = b.RemoveBreakpoint(ctx, id)
		}
	}()

	recorded := make([][]string, len(probes))
	for i, p := range probes {
		if p.Location.File == "" || p.Location.Line <= 0 {
			return model.Transcript{}, fmt.Errorf("probe %d needs a file and a line; this adapter cannot place a probe by symbol", i)
		}
		if len(p.Record) == 0 {
			return model.Transcript{}, fmt.Errorf("probe %d records nothing", i)
		}
		// A whole-frame request becomes whatever this runtime writes for its own
		// scope, or is refused by name -- never evaluated as an expression
		// literally called "*", which would report something that looks like an
		// answer.
		record := make([]string, len(p.Record))
		copy(record, p.Record)
		for j, expr := range record {
			if expr != model.RecordEverythingInScope {
				continue
			}
			if b.adapter.wholeFrameExpr == "" {
				return model.Transcript{}, fmt.Errorf(
					"probe %d asked to record everything in scope, which the %s adapter cannot do: "+
						"it records by evaluating expressions while the program runs on, so there is no "+
						"stopped frame to enumerate, and this runtime has no expression naming its own "+
						"scope. Name the expressions instead", i, b.adapter.Language)
			}
			record[j] = b.adapter.wholeFrameExpr
		}
		// Kept beside the probes rather than written back into them: the caller's
		// slice is not ours, and the output carries the translated names, so the
		// matcher has to read these.
		recorded[i] = record
		bp, err := b.SetBreakpoint(ctx, model.Breakpoint{
			Location: p.Location, Record: record, Suspend: model.SuspendNone,
		})
		if err != nil {
			return model.Transcript{}, fmt.Errorf("probe %d: %w", i, err)
		}
		created = append(created, bp.ID)
	}

	// Everything already printed belongs to the program, not to this trace.
	before, _ := b.Output(ctx, 0, 0)
	cursor := before.NextSince

	if err := b.Resume(ctx); err != nil {
		return model.Transcript{}, err
	}
	ev, err := b.WaitForStop(ctx, timeout)
	if err != nil {
		return model.Transcript{}, err
	}

	out := model.Transcript{
		Mode: string(caps.TraceMode),
		// A logpoint still interrupts the debuggee at each hit; only the agent
		// is spared the round trip.
		PerturbsTiming: caps.TraceMode != backend.TraceBuffered,
		Hits:           []model.TraceHit{},
	}
	switch ev.State {
	case model.StateExited:
		out.Status = model.TraceFinished
	case model.StatePaused:
		out.Status = model.TraceStopped
		out.Message = "The target stopped at something that is not a probe, so the trace ends here."
	default:
		out.Status = model.TraceTimeout
	}

	page, _ := b.Output(ctx, cursor, 0)
	counts := make([]int, len(probes))
	for _, chunk := range page.Chunks {
		values, found := parseTraceLine(chunk.Text)
		if !found {
			continue
		}
		index := probeFor(values, recorded)
		if index < 0 {
			continue
		}
		if p := probes[index]; p.MaxHits > 0 && counts[index] >= p.MaxHits {
			continue
		}
		counts[index]++
		present, absent := splitByPresence(values)
		out.Hits = append(out.Hits, model.TraceHit{
			Probe: index, Hit: counts[index],
			File: probes[index].Location.File, Line: probes[index].Location.Line,
			Values: present, Absent: absent,
		})
	}
	for i, n := range counts {
		if n == 0 {
			out.ProbesNeverHit = append(out.ProbesNeverHit,
				fmt.Sprintf("%s:%d", probes[i].Location.File, probes[i].Location.Line))
		}
	}
	if out.Message == "" {
		out.Message = fmt.Sprintf("Recorded %d hits across %d probes.", len(out.Hits), len(probes))
	}
	if len(out.ProbesNeverHit) > 0 {
		out.Message += " Some probes never fired, so an empty transcript there means a misplaced probe rather than no data."
	}
	return out, nil
}

// splitByPresence sorts recorded values into the ones that are there and the
// ones that are not, so a comparison across runs cannot read "None" as data.
func splitByPresence(values map[string]string) (present, absent map[string]string) {
	present = map[string]string{}
	for expr, value := range values {
		if p := presenceOfText(value); p != model.PresentValue {
			if absent == nil {
				absent = map[string]string{}
			}
			absent[expr] = string(p)
			continue
		}
		present[expr] = value
	}
	return present, absent
}

// parseTraceLine reads back a line this server's own logpoint template produced.
// Anything else the program printed is left alone.
func parseTraceLine(text string) (map[string]string, bool) {
	body, isTrace := strings.CutPrefix(text, tracePrefix)
	if !isTrace {
		return nil, false
	}
	values := map[string]string{}
	for _, part := range strings.Split(body, " | ") {
		name, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		values[name] = value
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

// probeFor matches a parsed line back to the probe that produced it by the set
// of expression names, which is what distinguishes probes in the output.
//
// It takes the names as recorded rather than as requested: a whole-frame request
// is written to the adapter as this runtime's own way of naming its scope, and
// that is the name the output comes back under.
func probeFor(values map[string]string, recorded [][]string) int {
	for i, exprs := range recorded {
		matched := 0
		for _, expr := range exprs {
			if _, present := values[expr]; present {
				matched++
			}
		}
		if matched == len(exprs) && matched > 0 {
			return i
		}
	}
	return -1
}
