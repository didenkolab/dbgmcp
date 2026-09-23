package tools

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/didenkolab/dbgmcp/internal/diffruns"
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunVariantIn is what differs between the two runs. Everything else -- the
// package, the probes, the build -- is shared, because a comparison is only
// worth reading when one thing changed.
type RunVariantIn struct {
	Label   string            `json:"label,omitempty" jsonschema:"Name for this run in the report, for example 'passing' or 'failing'."`
	TestRun string            `json:"test_run,omitempty" jsonschema:"Only for mode=test: the -test.run expression selecting which test this run executes."`
	Args    []string          `json:"args,omitempty" jsonschema:"Arguments for this run. Replaces args entirely rather than adding to them."`
	Env     map[string]string `json:"env,omitempty" jsonschema:"Environment variables for this run, merged over the shared env."`
}

type DiffRunsIn struct {
	Language string `json:"language,omitempty" jsonschema:"Runtime of the target: 'go' (default), 'python', or 'node'."`
	Mode     string `json:"mode" jsonschema:"'test', 'debug' or 'exec'. 'attach' is not available here: a diff has to start both runs itself."`
	Target   string `json:"target" jsonschema:"Package path for test/debug, or the binary path for exec. The same for both runs."`
	WorkDir  string `json:"work_dir" jsonschema:"Absolute path of the directory to run in."`

	RunA RunVariantIn `json:"run_a" jsonschema:"The first run. Conventionally the one that behaves correctly, so divergences read as what the second run did differently."`
	RunB RunVariantIn `json:"run_b" jsonschema:"The second run. Conventionally the failing one."`

	Probes     []ProbeIn         `json:"probes" jsonschema:"Where to record, and what to record there. Identical for both runs -- that is what makes them comparable."`
	Env        map[string]string `json:"env,omitempty" jsonschema:"Environment variables shared by both runs."`
	BuildTags  []string          `json:"build_tags,omitempty" jsonschema:"Build tags the target needs to compile, for example [\"integration\"]."`
	BuildFlags string            `json:"build_flags,omitempty" jsonschema:"Anything else the build needs, passed through verbatim."`
	TimeoutSec int               `json:"timeout_sec,omitempty" jsonschema:"How long to let each run proceed. Defaults to 60, and applies per run, so the call can take twice this."`
}

// RunSummary reports how a run went, so "they agreed" can be told apart from
// "neither run got far enough to disagree". Without it, a pair of runs that both
// timed out before the interesting code would report a clean comparison.
type RunSummary struct {
	Label          string   `json:"label"`
	Status         string   `json:"status"`
	Hits           int      `json:"hits"`
	ProbesNeverHit []string `json:"probes_never_hit,omitempty"`
}

type DiffRunsOut struct {
	First          *model.Divergence  `json:"first,omitempty" jsonschema:"The earliest point the two runs stopped agreeing. This is the answer most of the time; the divergences after it are usually consequences of it."`
	Divergences    []model.Divergence `json:"divergences"`
	Compared       int                `json:"compared" jsonschema:"How many readings were lined up and checked. Zero means nothing was compared, which is not the same as the runs agreeing."`
	RunA           RunSummary         `json:"run_a"`
	RunB           RunSummary         `json:"run_b"`
	PerturbsTiming bool               `json:"perturbs_timing" jsonschema:"True when the debuggee was stopped at each hit. A timing-dependent difference between two perturbed runs may be the observation rather than the bug."`
	Message        string             `json:"message"`
}

// diffRuns answers "this input works and that one does not -- where do they
// first differ", which findings structurally cannot: nothing in a single
// transcript says what the value should have been, and a second run does.
//
// It starts both runs itself. Handing the agent two sessions to trace and
// compare would cost six calls and put the comparison back in the model, which
// is exactly the round-trip cost this server exists to remove.
func (r *Registry) diffRuns(ctx context.Context, _ *mcp.CallToolRequest, in DiffRunsIn) (*mcp.CallToolResult, DiffRunsOut, error) {
	mode := model.LaunchMode(in.Mode)
	switch mode {
	case model.LaunchTest, model.LaunchDebug, model.LaunchExec:
	case model.LaunchAttach:
		return fail[DiffRunsOut]("mode=attach cannot be diffed: comparing two runs means starting both, and an attached process was already running. " +
			"Use trace_execution on the attached session instead.")
	default:
		return fail[DiffRunsOut]("Unknown mode %q. Use 'test', 'debug' or 'exec'.", in.Mode)
	}
	if in.WorkDir == "" {
		return fail[DiffRunsOut]("Missing required parameter: work_dir")
	}
	probes, unusable := buildProbes(in.Probes, "diff_runs")
	if unusable != "" {
		return fail[DiffRunsOut]("%s", unusable)
	}

	timeout := 60 * time.Second
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}

	// Sequential, not concurrent. Two debuggers on one target collide over the
	// build output and over anything the program binds, and a diff that fails
	// for that reason looks exactly like a diff that found something.
	left, err := r.traceOnce(ctx, in, in.RunA, probes, timeout)
	if err != nil {
		return fail[DiffRunsOut]("The first run failed before it could be compared: %s", err.Error())
	}
	right, err := r.traceOnce(ctx, in, in.RunB, probes, timeout)
	if err != nil {
		return fail[DiffRunsOut]("The first run succeeded but the second failed, so there is nothing to compare: %s", err.Error())
	}

	cmp := diffruns.Compare(left, right, probes)
	out := DiffRunsOut{
		First: cmp.First, Divergences: cmp.Divergences, Compared: cmp.Compared,
		PerturbsTiming: cmp.PerturbsTiming,
		RunA:           summarise(label(in.RunA.Label, "run_a"), left),
		RunB:           summarise(label(in.RunB.Label, "run_b"), right),
		Message:        cmp.Message,
	}
	if cmp.PerturbsTiming && cmp.First != nil {
		out.Message += " Both runs were stopped at every hit, so a difference that depends on timing may be an effect of the measurement."
	}
	return ok(out)
}

func label(given, fallback string) string {
	if given == "" {
		return fallback
	}
	return given
}

func summarise(label string, t model.Transcript) RunSummary {
	return RunSummary{
		Label: label, Status: string(t.Status), Hits: len(t.Hits),
		ProbesNeverHit: t.ProbesNeverHit,
	}
}

// traceOnce runs one side of the comparison and tears it down, so the second
// run starts from the same state the first one did rather than alongside it.
func (r *Registry) traceOnce(ctx context.Context, in DiffRunsIn, variant RunVariantIn, probes []model.Probe, timeout time.Duration) (model.Transcript, error) {
	b, err := backendFor(in.Language)
	if err != nil {
		return model.Transcript{}, err
	}
	req := model.LaunchRequest{
		Mode: model.LaunchMode(in.Mode), Target: in.Target, WorkDir: in.WorkDir,
		TestRun: variant.TestRun, Args: variant.Args, Env: mergeEnv(in.Env, variant.Env),
		BuildTags: in.BuildTags, BuildFlags: in.BuildFlags,
		// Forced on, unlike start_debug_session, where it is opt-in. Two runs of
		// a shuffled suite execute different tests in a different order, so the
		// divergence found would be the shuffle rather than the bug.
		Deterministic: true,
	}
	if err := b.Launch(ctx, req); err != nil {
		return model.Transcript{}, err
	}
	defer func() {
		// Best effort: a transcript already collected is worth more than a clean
		// teardown, and the caller cannot act on a stop failure anyway.
		_ = b.Stop(context.WithoutCancel(ctx))
	}()
	return b.Trace(ctx, probes, timeout)
}

func mergeEnv(shared, variant map[string]string) map[string]string {
	if len(shared) == 0 && len(variant) == 0 {
		return nil
	}
	merged := make(map[string]string, len(shared)+len(variant))
	for k, v := range shared {
		merged[k] = v
	}
	for k, v := range variant {
		merged[k] = v
	}
	return merged
}

// buildProbes is shared with trace_execution so the two tools accept exactly the
// same probe vocabulary. A diff whose probes meant something slightly different
// from a trace's would be worse than no diff at all.
//
// It returns the reason the probes are unusable rather than an error, because
// every one of these is a message for the model to act on, not a fault.
func buildProbes(in []ProbeIn, tool string) ([]model.Probe, string) {
	if len(in) == 0 {
		return nil, tool + " needs at least one probe."
	}
	probes := make([]model.Probe, 0, len(in))
	for i, p := range in {
		if p.Symbol == "" && p.File == "" {
			return nil, fmt.Sprintf("Probe %d needs either symbol, or file and line.", i)
		}
		if len(p.Record) == 0 {
			return nil, fmt.Sprintf("Probe %d records nothing. Give it at least one expression, or use set_breakpoint instead.", i)
		}
		// "Everything, plus these two" has no sensible reading: the expressions
		// would either duplicate what the frame already carries or silently lose
		// to it. One answer per probe.
		if slices.Contains(p.Record, model.RecordEverythingInScope) && len(p.Record) > 1 {
			return nil, fmt.Sprintf("Probe %d mixes %q with named expressions. Ask for one or the other: "+
				"%q records every argument and local in the frame, which already includes anything you would name.",
				i, model.RecordEverythingInScope, model.RecordEverythingInScope)
		}
		probes = append(probes, model.Probe{
			Location: model.Location{File: p.File, Line: p.Line, Symbol: p.Symbol},
			Record:   p.Record, MaxHits: p.MaxHits,
		})
	}
	return probes, ""
}
