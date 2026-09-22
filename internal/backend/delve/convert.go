package delve

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

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

// flattenVariable walks Delve's value tree into the flat, path-keyed list the
// model uses. The path it builds is a real Go expression, so anything the agent
// reads here it can immediately evaluate.
func flattenVariable(v api.Variable, path string, budget model.ValueBudget, out *[]model.Variable) {
	budget = budget.WithDefaults()
	*out = append(*out, model.Variable{
		Name:      path,
		Type:      v.Type,
		Kind:      v.Kind.String(),
		Value:     presentValue(v),
		Truncated: isTruncated(v),
	})

	switch v.Kind.String() {
	case "struct":
		for _, c := range v.Children {
			flattenVariable(c, path+"."+c.Name, budget, out)
		}
	case "slice", "array":
		for i, c := range v.Children {
			flattenVariable(c, fmt.Sprintf("%s[%d]", path, i), budget, out)
		}
	case "map":
		// Delve returns map entries as alternating key and value children.
		for i := 0; i+1 < len(v.Children); i += 2 {
			key, val := v.Children[i], v.Children[i+1]
			flattenVariable(val, fmt.Sprintf("%s[%s]", path, key.Value), budget, out)
		}
	case "ptr", "interface":
		// FollowPointers means the single child is the pointee. It keeps the
		// same path because that is what the agent would write to reach it.
		for _, c := range v.Children {
			if c.Kind.String() == "struct" || c.Kind.String() == "slice" || c.Kind.String() == "map" {
				for _, gc := range c.Children {
					flattenVariable(gc, path+"."+gc.Name, budget, out)
				}
			}
		}
	}
}

func isTruncated(v api.Variable) bool {
	// Delve reports the real length separately from what it sent, which is the
	// only way to tell "empty" from "truncated". An agent that cannot tell those
	// apart draws the wrong conclusion from an empty slice.
	if v.Len > int64(len(v.Children)) && len(v.Children) > 0 {
		return true
	}
	return v.Kind.String() == "string" && v.Len > int64(len(v.Value))
}

// presentValue gives a value a readable one-line form. Delve leaves Value empty
// for composites, and an empty string reads as "no value" rather than "look at
// the fields", so composites are rendered rather than left blank.
func presentValue(v api.Variable) string {
	if v.Unreadable != "" {
		return "<unreadable: " + v.Unreadable + ">"
	}
	// Pointers are handled before Value is trusted. Delve sets Value to the
	// address, and passing that through reads as data: a *big.Int inside a
	// decimal came back as "87588325026848", which is a plausible-looking
	// amount and is in fact a pointer. Showing the type instead is worse to
	// look at and impossible to misread.
	if v.Kind.String() == "ptr" {
		if len(v.Children) == 0 || (len(v.Children) == 1 && v.Children[0].Addr == 0) {
			return "nil"
		}
		if inner := presentValue(v.Children[0]); inner != "" {
			return "*" + inner
		}
		return "*" + v.Type
	}
	if v.Value != "" {
		return v.Value
	}
	// An empty rendering is ambiguous in the worst way: for an error or any
	// other nilable kind it reads as "I could not tell you", when what it
	// actually means is "there is nothing here". In Go that is the difference
	// between "no error" and "I do not know", so it is spelled out.
	switch v.Kind.String() {
	case "interface", "chan", "func", "unsafe.Pointer":
		if len(v.Children) == 0 || (len(v.Children) == 1 && v.Children[0].Kind.String() == "invalid") {
			return "nil"
		}
		if inner := presentValue(v.Children[0]); inner != "" {
			return inner
		}
		return v.Type
	}
	switch v.Kind.String() {
	case "struct":
		// Inline fields: one readable line is cheaper for an agent than the same
		// data spread over a dozen entries, and the fields are still listed
		// separately for drilling down.
		var sb strings.Builder
		sb.WriteString(v.Type + "{")
		for i, c := range v.Children {
			if i > 0 {
				sb.WriteString(", ")
			}
			if i >= 8 {
				sb.WriteString("...")
				break
			}
			sb.WriteString(c.Name + ": " + shortValue(c))
		}
		sb.WriteString("}")
		return sb.String()
	case "slice", "array":
		return v.Type + " len=" + strconv.FormatInt(v.Len, 10) + " cap=" + strconv.FormatInt(v.Cap, 10)
	case "map":
		return v.Type + " len=" + strconv.FormatInt(v.Len, 10)
	}
	return v.Value
}

// shortValue is the one-line form used inside a struct rendering, where nesting
// further would defeat the point of having a single readable line.
func shortValue(v api.Variable) string {
	if v.Kind.String() == "ptr" {
		if len(v.Children) == 0 || (len(v.Children) == 1 && v.Children[0].Addr == 0) {
			return "nil"
		}
		return "*" + v.Children[0].Type
	}
	if v.Value != "" {
		return v.Value
	}
	// An empty rendering is ambiguous in the worst way: for an error or any
	// other nilable kind it reads as "I could not tell you", when what it
	// actually means is "there is nothing here". In Go that is the difference
	// between "no error" and "I do not know", so it is spelled out.
	switch v.Kind.String() {
	case "interface", "chan", "func", "unsafe.Pointer":
		if len(v.Children) == 0 || (len(v.Children) == 1 && v.Children[0].Kind.String() == "invalid") {
			return "nil"
		}
		if inner := presentValue(v.Children[0]); inner != "" {
			return inner
		}
		return v.Type
	}
	switch v.Kind.String() {
	case "struct":
		return v.Type + "{...}"
	case "slice", "array", "map":
		return v.Type + " len=" + strconv.FormatInt(v.Len, 10)
	}
	return "?"
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
