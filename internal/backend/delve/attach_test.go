package delve

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/model"
)

// startLongRunning builds and starts a process the debugger did not create,
// which is the whole point of the attach path.
func startLongRunning(t *testing.T) (pid int, dir string) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "longrunning")
	bin := filepath.Join(t.TempDir(), "longrunning")

	build := exec.Command("go", "build", "-gcflags=all=-N -l", "-o", bin, ".")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}

	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	time.Sleep(300 * time.Millisecond) // let it get past its own startup
	return cmd.Process.Pid, src
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestLiveAttachLeavesTheProcessRunning is the safety property that makes attach
// usable at all. Detaching from a service somebody else is using must not kill
// it, and the difference is one boolean deep inside teardown -- exactly the kind
// of thing that is correct until the day it is catastrophic.
func TestLiveAttachLeavesTheProcessRunning(t *testing.T) {
	if _, err := FindDelve(); err != nil {
		t.Skipf("delve is not installed: %v", err)
	}
	pid, dir := startLongRunning(t)
	if !alive(pid) {
		t.Fatalf("fixture died before the test began")
	}

	b := New()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := b.Launch(ctx, model.LaunchRequest{
		Mode: model.LaunchAttach, PID: pid, WorkDir: dir,
	}); err != nil {
		t.Skipf("attach is unavailable in this environment: %v", err)
	}

	// It must be a real debug session, not merely a connection.
	if _, err := b.SetBreakpoint(ctx, model.Breakpoint{
		Location: model.Location{Symbol: "main.tick"},
	}); err != nil {
		t.Fatalf("set breakpoint on the attached process: %v", err)
	}
	if err := b.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	ev, err := b.WaitForStop(ctx, 60*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if ev.State != model.StatePaused {
		t.Fatalf("never stopped in the attached process: %s %s", ev.State, ev.Message)
	}
	n, err := b.Evaluate(ctx, 0, "n", model.ValueBudget{})
	if err != nil {
		t.Errorf("evaluate in the attached process: %v", err)
	} else {
		t.Logf("attached and stopped in main.tick with n=%s", n.Value)
	}

	// The assertion this whole test exists for.
	if err := b.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if !alive(pid) {
		t.Fatal("stopping the session killed a process it did not start")
	}
	t.Logf("process %d still running after the debugger detached", pid)
}

// TestLiveAttachRefusesAPidThatIsNotThere keeps the failure legible rather than
// letting it surface as a timeout.
func TestLiveAttachRefusesAPidThatIsNotThere(t *testing.T) {
	if _, err := FindDelve(); err != nil {
		t.Skipf("delve is not installed: %v", err)
	}
	b := New()
	t.Cleanup(func() { _ = b.Stop(context.Background()) })

	err := b.Launch(context.Background(), model.LaunchRequest{
		Mode: model.LaunchAttach, PID: 999999, WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("attaching to a pid that does not exist must fail")
	}
	t.Logf("refused as expected: %v", err)
}
