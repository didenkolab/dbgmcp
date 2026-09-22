package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/didenkolab/dbgmcp/internal/findings"
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ProbeIn struct {
	File    string   `json:"file,omitempty" jsonschema:"Absolute path of the source file. Supply with line, or supply symbol instead."`
	Line    int      `json:"line,omitempty" jsonschema:"1-based line, used with file."`
	Symbol  string   `json:"symbol,omitempty" jsonschema:"Function to record at, for example main.lineTotal. Preferred over file and line."`
	Record  []string `json:"record" jsonschema:"Expressions the debugger evaluates at every hit, for example it.Name and it.Price*it.Qty."`
	MaxHits int      `json:"max_hits,omitempty" jsonschema:"Stop recording this probe after this many hits. Omit to record until the program ends or the trace times out."`
}

type TraceIn struct {
	SessionID  string    `json:"session_id,omitempty" jsonschema:"Session to act on. May be omitted when exactly one session is open."`
	Probes     []ProbeIn `json:"probes" jsonschema:"Where to record, and what to record there."`
	TimeoutSec int       `json:"timeout_sec,omitempty" jsonschema:"How long to let the target run before returning what was collected. Defaults to 60."`
}

type TraceOut struct {
	Status         string           `json:"status" jsonschema:"'completed' when every probe met its budget, 'finished' when the program ended first, 'stopped' when something other than a probe halted it, 'timeout' otherwise."`
	Hits           []model.TraceHit `json:"hits"`
	ProbesNeverHit []string         `json:"probes_never_hit,omitempty"`
	Findings       []model.Finding  `json:"findings,omitempty" jsonschema:"What the server noticed in the transcript. Each is an observation with the values it rests on, not a verdict about the cause."`
	Mode           string           `json:"mode" jsonschema:"How the transcript was collected: 'buffered' (the debuggee never stopped), 'auto_continue' (it stopped at each hit but the debugger resumed it with no agent round-trip), 'suspend_only'."`
	PerturbsTiming bool             `json:"perturbs_timing" jsonschema:"True when the debuggee was stopped at each hit. Decisive when chasing a race: a perturbed trace can hide or create the very timing you are investigating."`
	Message        string           `json:"message"`
}

func (r *Registry) traceExecution(ctx context.Context, _ *mcp.CallToolRequest, in TraceIn) (*mcp.CallToolResult, TraceOut, error) {
	sess, err := r.store.Resolve(in.SessionID)
	if err != nil {
		return fail[TraceOut]("%s", err.Error())
	}
	if len(in.Probes) == 0 {
		return fail[TraceOut]("trace_execution needs at least one probe.")
	}

	probes := make([]model.Probe, 0, len(in.Probes))
	for i, p := range in.Probes {
		if p.Symbol == "" && p.File == "" {
			return fail[TraceOut]("Probe %d needs either symbol, or file and line.", i)
		}
		if len(p.Record) == 0 {
			return fail[TraceOut]("Probe %d records nothing. Give it at least one expression, or use set_breakpoint instead.", i)
		}
		probes = append(probes, model.Probe{
			Location: model.Location{File: p.File, Line: p.Line, Symbol: p.Symbol},
			Record:   p.Record, MaxHits: p.MaxHits,
		})
	}

	timeout := 60 * time.Second
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	tr, err := sess.Backend.Trace(ctx, probes, timeout)
	if err != nil {
		return fail[TraceOut]("%s", err.Error())
	}
	// Computed here rather than in a backend: the rules read the transcript and
	// nothing else, so one implementation serves every runtime.
	tr.Findings = findings.Analyse(tr, probes)

	message := tr.Message
	if len(tr.Findings) > 0 {
		message += fmt.Sprintf(" %d thing(s) in this transcript looked worth pointing out; see findings, which are observations rather than conclusions.", len(tr.Findings))
	}
	return ok(TraceOut{
		Status: string(tr.Status), Hits: tr.Hits, ProbesNeverHit: tr.ProbesNeverHit,
		Findings: tr.Findings, Mode: tr.Mode, PerturbsTiming: tr.PerturbsTiming, Message: message,
	})
}
