package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// ---------- inspection ----------

func (b *Backend) Stack(ctx context.Context, unitID string, maxFrames int) ([]model.Frame, error) {
	cl, err := b.rpc()
	if err != nil {
		return nil, err
	}
	tid := b.threadID()
	if unitID != "" {
		if n, convErr := strconv.Atoi(unitID); convErr == nil {
			tid = n
		}
	}
	if maxFrames <= 0 {
		maxFrames = 32
	}
	body, err := cl.send(ctx, "stackTrace", map[string]any{
		"threadId": tid, "startFrame": 0, "levels": maxFrames,
	}, requestTimeout)
	if err != nil {
		return nil, err
	}
	var resp stackTraceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	b.mu.Lock()
	b.frameIDs = b.frameIDs[:0]
	for _, f := range resp.StackFrames {
		b.frameIDs = append(b.frameIDs, f.ID)
	}
	b.mu.Unlock()

	out := make([]model.Frame, 0, len(resp.StackFrames))
	for i, f := range resp.StackFrames {
		file := b.pathMap.ToAgent(f.Source.Path)
		label := f.Name
		if file != "" {
			label = f.Name + " at " + file + ":" + strconv.Itoa(f.Line)
		}
		out = append(out, model.Frame{Index: i, Function: f.Name, File: file, Line: f.Line, Label: label})
	}
	return out, nil
}

// frameRef resolves our frame index to the adapter's opaque frame id. DAP hands
// out ids that are only valid until the next resume, so they are refreshed with
// every stack read rather than cached across stops.
func (b *Backend) frameRef(ctx context.Context, index int) (int, error) {
	b.mu.Lock()
	known := len(b.frameIDs)
	b.mu.Unlock()
	if known == 0 {
		if _, err := b.Stack(ctx, "", 32); err != nil {
			return 0, err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if index < 0 || index >= len(b.frameIDs) {
		if len(b.frameIDs) == 0 {
			return 0, fmt.Errorf("Session must be paused to read frames")
		}
		return 0, fmt.Errorf("frame %d does not exist; the stack has %d frames", index, len(b.frameIDs))
	}
	return b.frameIDs[index], nil
}

func (b *Backend) Variables(ctx context.Context, frameIndex int, budget model.ValueBudget) ([]model.Variable, error) {
	cl, err := b.rpc()
	if err != nil {
		return nil, err
	}
	if frameIndex < 0 {
		frameIndex = 0
	}
	frameID, err := b.frameRef(ctx, frameIndex)
	if err != nil {
		return nil, err
	}
	body, err := cl.send(ctx, "scopes", map[string]any{"frameId": frameID}, requestTimeout)
	if err != nil {
		return nil, err
	}
	var scopes scopesResponse
	if err := json.Unmarshal(body, &scopes); err != nil {
		return nil, err
	}

	budget = budget.WithDefaults()
	var out []model.Variable
	for _, sc := range scopes.Scopes {
		// Globals and built-ins are "expensive" for a reason: they are enormous
		// and almost never what the question is about.
		if sc.Expensive {
			continue
		}
		b.collect(ctx, cl, sc.VariablesReference, "", budget, 0, &out)
	}
	return out, nil
}

// collect flattens the adapter's variable tree into the flat, path-keyed list
// the model uses, so a name read here is an expression that can be evaluated.
func (b *Backend) collect(ctx context.Context, cl *client, ref int, prefix string, budget model.ValueBudget, depth int, out *[]model.Variable) {
	if ref == 0 || depth > budget.MaxDepth {
		return
	}
	body, err := cl.send(ctx, "variables", map[string]any{"variablesReference": ref}, requestTimeout)
	if err != nil {
		return
	}
	var resp variablesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return
	}
	for i, v := range resp.Variables {
		if budget.MaxArrayValues > 0 && i >= budget.MaxArrayValues {
			break
		}
		if isPresentationGroup(v.Name) {
			// debugpy folds dunder methods and the like into pseudo-entries so
			// an IDE can render them collapsed. Walking into them fills the
			// agent's context with __delattr__ and friends, which is the exact
			// waste the value budget exists to prevent.
			continue
		}
		path := v.Name
		if prefix != "" {
			path = prefix + "." + v.Name
		}
		value := v.Value
		truncated := false
		if budget.MaxStringLen > 0 && len(value) > budget.MaxStringLen {
			value = value[:budget.MaxStringLen]
			truncated = true
		}
		*out = append(*out, model.Variable{
			Name: path, Type: v.Type, Value: value, Truncated: truncated,
			Presence: presenceOfText(value),
		})
		b.collect(ctx, cl, v.VariablesReference, path, budget, depth+1, out)
	}
}

// presenceOfText classifies a rendered value.
//
// DAP carries no presence flag: an adapter reports "None" or "null" as the text
// of the value, so the distinction has to be recovered here. Doing it once, in
// one place, is what stops every consumer from inventing its own guess.
func presenceOfText(value string) model.Presence {
	switch strings.TrimSpace(value) {
	case "None", "null", "nil", "undefined", "<nil>":
		return model.PresentNil
	case "":
		return model.PresentUnreadable
	}
	return model.PresentValue
}

func (b *Backend) Evaluate(ctx context.Context, frameIndex int, expr string, budget model.ValueBudget) (model.Variable, error) {
	cl, err := b.rpc()
	if err != nil {
		return model.Variable{}, err
	}
	if frameIndex < 0 {
		frameIndex = 0
	}
	args := map[string]any{"expression": expr, "context": "repl"}
	if frameID, err := b.frameRef(ctx, frameIndex); err == nil {
		args["frameId"] = frameID
	}
	body, err := cl.send(ctx, "evaluate", args, requestTimeout)
	if err != nil {
		return model.Variable{}, fmt.Errorf("could not evaluate %q: %w", expr, err)
	}
	var resp evaluateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return model.Variable{}, err
	}
	return model.Variable{Name: expr, Type: resp.Type, Value: resp.Result,
		Presence: presenceOfText(resp.Result)}, nil
}

func (b *Backend) SetVariable(ctx context.Context, frameIndex int, name, value string) error {
	cl, err := b.rpc()
	if err != nil {
		return err
	}
	if b.Capabilities().SetVariable == backend.SupportNone {
		return backend.Unsupported(b.Name(), "set_variable",
			"This adapter cannot change values in the running program; change the source and re-run instead.")
	}
	if frameIndex < 0 {
		frameIndex = 0
	}
	frameID, err := b.frameRef(ctx, frameIndex)
	if err != nil {
		return err
	}
	body, err := cl.send(ctx, "scopes", map[string]any{"frameId": frameID}, requestTimeout)
	if err != nil {
		return err
	}
	var scopes scopesResponse
	if err := json.Unmarshal(body, &scopes); err != nil {
		return err
	}

	// DAP sets a variable by name within a container, so a dotted path has to be
	// walked: the leading segments locate the container, and the last names the
	// member. Without this, every path the agent reads back from get_variables
	// would be unusable for writing -- which would make the two halves of the
	// tool disagree about what a name means.
	container, member := splitPath(name)
	refs := make([]int, 0, len(scopes.Scopes))
	for _, sc := range scopes.Scopes {
		if !sc.Expensive {
			refs = append(refs, sc.VariablesReference)
		}
	}
	if container != "" {
		parent, err := cl.send(ctx, "evaluate", map[string]any{
			"expression": container, "frameId": frameID, "context": "repl",
		}, requestTimeout)
		if err != nil {
			return fmt.Errorf("could not reach %s: %w", container, err)
		}
		var resolved evaluateResponse
		if err := json.Unmarshal(parent, &resolved); err != nil || resolved.VariablesReference == 0 {
			return fmt.Errorf("%s has no members that can be set individually", container)
		}
		refs = []int{resolved.VariablesReference}
	}

	var lastErr error
	for _, ref := range refs {
		_, err := cl.send(ctx, "setVariable", map[string]any{
			"variablesReference": ref, "name": member, "value": value,
		}, requestTimeout)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("could not set %s: %w", name, lastErr)
}

// splitPath separates a dotted path into the container to resolve and the member
// to set. "total" has no container; "it.price" resolves "it" and sets "price".
func splitPath(path string) (container, member string) {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return "", path
	}
	return path[:i], path[i+1:]
}

func (b *Backend) ExecUnits(ctx context.Context, limit int) ([]model.ExecUnit, error) {
	cl, err := b.rpc()
	if err != nil {
		return nil, err
	}
	body, err := cl.send(ctx, "threads", map[string]any{}, requestTimeout)
	if err != nil {
		return nil, err
	}
	var resp threadsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	current := b.threadID()
	out := make([]model.ExecUnit, 0, len(resp.Threads))
	for i, t := range resp.Threads {
		if i >= limit {
			break
		}
		out = append(out, model.ExecUnit{
			ID: strconv.Itoa(t.ID), Kind: model.UnitThread, Name: t.Name,
			// DAP reports no per-thread state, so "running" is the honest
			// default rather than a guess dressed up as knowledge.
			State: model.UnitRunning, Current: t.ID == current,
		})
	}
	return out, nil
}

func (b *Backend) Source(_ context.Context, file string, line, contextLines int) (*model.SourceSpan, error) {
	if contextLines <= 0 {
		contextLines = 5
	}
	span := readSourceSpan(b.pathMap.ToRuntime(file), line, contextLines)
	if span == nil {
		return nil, fmt.Errorf("could not read source at %s:%d", file, line)
	}
	span.File = file
	return span, nil
}

// ---------- output ----------

func (b *Backend) Output(_ context.Context, since, limit int) (model.OutputPage, error) {
	b.absorbOutput()
	b.mu.Lock()
	defer b.mu.Unlock()
	page := model.OutputPage{NextSince: since, Chunks: []model.OutputChunk{}}
	for _, c := range b.outputBuf {
		if c.Seq <= since {
			continue
		}
		page.Chunks = append(page.Chunks, c)
		page.NextSince = c.Seq
		if limit > 0 && len(page.Chunks) >= limit {
			break
		}
	}
	return page, nil
}

func (b *Backend) tailOutput(n int) []model.OutputChunk {
	b.absorbOutput()
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || len(b.outputBuf) == 0 {
		return nil
	}
	start := len(b.outputBuf) - n
	if start < 0 {
		start = 0
	}
	out := make([]model.OutputChunk, len(b.outputBuf)-start)
	copy(out, b.outputBuf[start:])
	return out
}

// absorbOutput moves what the adapter has reported into the session buffer.
// DAP delivers the debuggee's output as events rather than a pipe, so this is
// the only place it exists.
func (b *Backend) absorbOutput() {
	b.mu.Lock()
	cl := b.client
	b.mu.Unlock()
	if cl == nil {
		return
	}
	events := cl.drainOutput()
	if len(events) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ev := range events {
		stream := model.StreamStdout
		if ev.Category == "stderr" {
			stream = model.StreamStderr
		}
		for _, line := range strings.Split(strings.TrimRight(ev.Output, "\n"), "\n") {
			b.outputSeq++
			b.outputBuf = append(b.outputBuf, model.OutputChunk{
				Seq: b.outputSeq, Stream: stream, Text: line, At: time.Now(),
			})
		}
	}
	if over := len(b.outputBuf) - 4000; over > 0 {
		b.outputBuf = b.outputBuf[over:]
	}
}

// readSourceSpan pulls a window of source around a line, read from disk because
// no adapter is obliged to serve source and an agent that has to open the file
// itself spends a call it should not need to.
func readSourceSpan(file string, line, contextLines int) *model.SourceSpan {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	first := line - contextLines
	if first < 1 {
		first = 1
	}
	last := line + contextLines
	span := &model.SourceSpan{File: file, FirstLine: first, MarkLine: line}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; scanner.Scan() && n <= last; n++ {
		if n >= first {
			span.Lines = append(span.Lines, scanner.Text())
		}
	}
	if len(span.Lines) == 0 {
		return nil
	}
	return span
}

// presentationGroups are entries an adapter invents for a tree view rather than
// members the program actually has.
var presentationGroups = map[string]bool{
	"special variables":   true,
	"function variables":  true,
	"class variables":     true,
	"protected variables": true,
	"private variables":   true,
	"len()":               true,
}

func isPresentationGroup(name string) bool { return presentationGroups[name] }
