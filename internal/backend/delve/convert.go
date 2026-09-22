package delve

import (
	"bufio"
	"os"
	"strconv"

	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/service/api"
)

// goroutineStatusWaiting is Go's runtime Gwaiting. It is the status worth
// surfacing, because a deadlock is a set of goroutines sitting in it.
const goroutineStatusWaiting = 4

func loadConfig(b model.ValueBudget) api.LoadConfig {
	b = b.WithDefaults()
	return api.LoadConfig{
		// Pointers are followed because a Go agent looking at a struct almost
		// always wants what the pointer points at, not its address.
		FollowPointers:     true,
		MaxVariableRecurse: b.MaxDepth,
		MaxStringLen:       b.MaxStringLen,
		MaxArrayValues:     b.MaxArrayValues,
		MaxStructFields:    b.MaxStructAttrs,
	}
}

func toVariable(v api.Variable, budget model.ValueBudget) model.Variable {
	budget = budget.WithDefaults()
	out := model.Variable{
		Name:  v.Name,
		Type:  v.Type,
		Kind:  v.Kind.String(),
		Value: presentValue(v),
	}
	// Delve reports the real length separately from what it sent, which is the
	// only way to tell "empty" from "truncated". An agent that cannot tell those
	// apart draws the wrong conclusion from an empty slice.
	if v.Len > int64(len(v.Children)) && len(v.Children) > 0 {
		out.Truncated = true
	}
	if v.Kind.String() == "string" && v.Len > int64(len(v.Value)) {
		out.Truncated = true
	}
	for _, c := range v.Children {
		out.Children = append(out.Children, toVariable(c, budget))
	}
	return out
}

// presentValue gives composite types a readable stand-in, because Delve leaves
// Value empty for structs and slices and an empty string reads as "no value"
// rather than "look at the children".
func presentValue(v api.Variable) string {
	if v.Unreadable != "" {
		return "<unreadable: " + v.Unreadable + ">"
	}
	if v.Value != "" {
		return v.Value
	}
	switch v.Kind.String() {
	case "struct":
		return v.Type + "{...}"
	case "slice", "array":
		return v.Type + " len=" + strconv.FormatInt(v.Len, 10) + " cap=" + strconv.FormatInt(v.Cap, 10)
	case "map":
		return v.Type + " len=" + strconv.FormatInt(v.Len, 10)
	case "ptr":
		if len(v.Children) == 0 {
			return "nil"
		}
	}
	return v.Value
}

func toFrames(frames []api.Stackframe) []model.Frame {
	out := make([]model.Frame, 0, len(frames))
	for i, f := range frames {
		fn := ""
		if f.Function != nil {
			fn = f.Function.Name()
		}
		out = append(out, model.Frame{
			Index: i, Function: fn, File: f.File, Line: f.Line,
			Label: frameLabel(fn, f.File, f.Line),
		})
	}
	return out
}

func frameLabel(fn, file string, line int) string {
	if fn == "" {
		return "<unknown>"
	}
	if file == "" {
		return fn
	}
	return fn + " at " + file + ":" + strconv.Itoa(line)
}

// toExecUnit needs the target's Go version because the runtime's wait-reason
// numbering is renumbered between Go releases -- Delve keeps one table per
// version for exactly that reason. Hardcoding the constants would have produced
// confidently wrong answers on a Go upgrade.
func toExecUnit(g *api.Goroutine, current bool, goVer *goversion.GoVersion) model.ExecUnit {
	u := model.ExecUnit{
		ID:      strconv.FormatInt(g.ID, 10),
		Kind:    model.UnitGoroutine,
		Current: current,
		State:   model.UnitRunning,
	}
	if g.UserCurrentLoc.Function != nil {
		u.Name = g.UserCurrentLoc.Function.Name()
	}
	// Delve's Status maps onto Go's runtime goroutine statuses; 4 is Gwaiting,
	// which is the one worth surfacing because it is how a deadlock looks.
	if g.Status == goroutineStatusWaiting {
		u.State = model.UnitBlocked
		if goVer != nil {
			u.Detail = api.WaitReasonString(goVer, g.WaitReason)
		}
	}
	u.TopFrame = &model.Frame{
		Function: u.Name, File: g.UserCurrentLoc.File, Line: g.UserCurrentLoc.Line,
		Label: frameLabel(u.Name, g.UserCurrentLoc.File, g.UserCurrentLoc.Line),
	}
	return u
}

func toBreakpoint(bp *api.Breakpoint) model.Breakpoint {
	out := model.Breakpoint{
		ID:           strconv.Itoa(bp.ID),
		Kind:         model.BreakLine,
		Location:     model.Location{File: bp.File, Line: bp.Line, Symbol: bp.FunctionName},
		Enabled:      !bp.Disabled,
		Condition:    bp.Cond,
		HitCondition: bp.HitCond,
		Suspend:      model.SuspendAll,
		Record:       bp.Variables,
		// Real hit counts, unlike the language-agnostic IDE API which cannot see
		// them at all.
		HitCount:       bp.TotalHitCount,
		HitCountByUnit: bp.HitCount,
	}
	if bp.WatchExpr != "" {
		out.Kind = model.BreakWatch
		out.Selector = bp.WatchExpr
	}
	if bp.Tracepoint {
		out.Suspend = model.SuspendNone
	}
	return out
}

// readSourceSpan pulls a window of source around a line. It reads from disk
// rather than from the debugger because Delve does not serve source, and an
// agent that has to open the file itself spends a call it should not need to.
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
