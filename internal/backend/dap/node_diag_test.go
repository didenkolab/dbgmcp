package dap_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend/dap"
	"github.com/didenkolab/dbgmcp/internal/model"
)

// TestNodeDiagnostic walks one launch step by step with timings, so a hang is
// attributed to a step rather than to the suite.
func TestNodeDiagnostic(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "buggy_js")

	b, err := dap.NewUnderConstruction("node")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })

	ctx := context.Background()
	step := func(name string, fn func() error) {
		start := time.Now()
		err := fn()
		t.Logf("%-22s %6.1fs  err=%v", name, time.Since(start).Seconds(), err)
	}

	step("launch", func() error {
		return b.Launch(ctx, model.LaunchRequest{
			Mode: model.LaunchDebug, Target: "cart.js", WorkDir: dir,
		})
	})
	t.Logf("capabilities: %+v", b.Capabilities())

	step("status", func() error {
		ev, err := b.Status(ctx)
		t.Logf("  state=%s reason=%s frames=%d vars=%d", ev.State, ev.Reason, len(ev.Frames), len(ev.Variables))
		return err
	})
	step("set breakpoint", func() error {
		bp, err := b.SetBreakpoint(ctx, model.Breakpoint{
			Location: model.Location{File: filepath.Join(dir, "cart.js"), Line: 21},
		})
		t.Logf("  bp=%+v", bp.Location)
		return err
	})
	step("resume", func() error { return b.Resume(ctx) })
	step("wait", func() error {
		ev, err := b.WaitForStop(ctx, 20*time.Second)
		t.Logf("  state=%s reason=%s frames=%d vars=%d msg=%s", ev.State, ev.Reason, len(ev.Frames), len(ev.Variables), ev.Message)
		return err
	})
}
