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

	for i, p := range probes {
		if p.Location.File == "" || p.Location.Line <= 0 {
			return model.Transcript{}, fmt.Errorf("probe %d needs a file and a line; this adapter cannot place a probe by symbol", i)
		}
		if len(p.Record) == 0 {
			return model.Transcript{}, fmt.Errorf("probe %d records nothing", i)
		}
		bp, err := b.SetBreakpoint(ctx, model.Breakpoint{
			Location: p.Location, Record: p.Record, Suspend: model.SuspendNone,
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
		index := probeFor(values, probes)
		if index < 0 {
			continue
		}
		if p := probes[index]; p.MaxHits > 0 && counts[index] >= p.MaxHits {
			continue
		}
		counts[index]++
		out.Hits = append(out.Hits, model.TraceHit{
			Probe: index, Hit: counts[index],
			File: probes[index].Location.File, Line: probes[index].Location.Line,
			Values: values,
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
func probeFor(values map[string]string, probes []model.Probe) int {
	for i, p := range probes {
		matched := 0
		for _, expr := range p.Record {
			if _, present := values[expr]; present {
				matched++
			}
		}
		if matched == len(p.Record) && matched > 0 {
			return i
		}
	}
	return -1
}
