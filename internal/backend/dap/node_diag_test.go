package dap_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend/conformance"
	"github.com/didenkolab/dbgmcp/internal/backend/dap"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// TestNodeDiagnostic walks one launch step by step and says WHERE it is at each
// step, so a stop landing in the wrong frame is attributed rather than guessed.
func TestNodeDiagnostic(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "buggy_js")
	cart := filepath.Join(dir, "cart.js")

	b, err := dap.New("node")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	ctx := context.Background()

	where := func(label string, ev model.StopEvent) {
		top := "(no frames)"
		if len(ev.Frames) > 0 {
			top = ev.Frames[0].Function + " at " + filepath.Base(ev.Frames[0].File) + ":" +
				itoa(ev.Frames[0].Line)
		}
		names := []string{}
		for i, v := range ev.Variables {
			if i >= 6 {
				break
			}
			names = append(names, v.Name)
		}
		t.Logf("%-14s state=%-7s reason=%-11s top=%-34s vars[%d]: %s",
			label, ev.State, ev.Reason, top, len(ev.Variables), strings.Join(names, " "))
	}

	if err := b.Launch(ctx, model.LaunchRequest{Mode: model.LaunchDebug, Target: "cart.js", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	ev, _ := b.Status(ctx)
	where("after launch", ev)

	units, _ := b.ExecUnits(ctx, 10)
	for _, u := range units {
		t.Logf("unit id=%s name=%q current=%v", u.ID, u.Name, u.Current)
	}

	line := conformance.LineContaining(t, cart, "NEVER-REACHED") - 1
	bp, err := b.SetBreakpoint(ctx, model.Breakpoint{Location: model.Location{File: cart, Line: line}})
	if err != nil {
		t.Fatalf("set breakpoint at line %d: %v", line, err)
	}
	t.Logf("breakpoint asked for line %d, bound at line %d", line, bp.Location.Line)

	for i := 1; i <= 3; i++ {
		if err := b.Resume(ctx); err != nil {
			t.Fatalf("resume %d: %v", i, err)
		}
		ev, err := b.WaitForStop(ctx, 15*time.Second)
		if err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
		where("stop "+itoa(i), ev)
		// Whether the program advanced between stops distinguishes "it really
		// paused there again" from "we are reading a state that never moved".
		if page, err := b.Output(ctx, 0, 50); err == nil {
			t.Logf("               output so far: %d line(s)", len(page.Chunks))
		}
		if ev.State == model.StateExited {
			break
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
