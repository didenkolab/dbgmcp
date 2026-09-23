package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
	"github.com/didenkolab/dbgmcp/internal/session"
)

// stubBackend is the whole debugger this test needs: stopping is the only thing
// under examination. The interface is embedded rather than implemented, so any
// method this test starts to depend on panics instead of quietly returning a
// zero value.
type stubBackend struct{ backend.Backend }

func (stubBackend) Stop(context.Context) error { return nil }
func (stubBackend) Name() string               { return "stub" }

// A session that attached did not start the process, and stopping it leaves that
// process running. Reporting a termination there tells an agent it has killed a
// live service -- and an agent told that will act on it.
func TestStoppingAnAttachedSessionDoesNotClaimToHaveKilledAnything(t *testing.T) {
	store := session.NewStore()
	store.Add(&session.Session{
		ID: "s1", Backend: stubBackend{}, StartedAt: time.Now(),
		Request: model.LaunchRequest{Mode: model.LaunchAttach, PID: 4242, WorkDir: "/tmp"},
	})
	r := NewRegistry(store)

	_, out, err := r.stopDebugSession(context.Background(), nil, SessionRef{SessionID: "s1"})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if strings.Contains(out.Message, "terminated") {
		t.Errorf("an attached process was reported as terminated: %q", out.Message)
	}
	for _, want := range []string{"4242", "still running"} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("the message does not say %q: %q", want, out.Message)
		}
	}
}

// The counterpart, so the test above cannot pass by saying the same thing about
// every session.
func TestStoppingASessionItStartedSaysTheDebuggeeIsGone(t *testing.T) {
	store := session.NewStore()
	store.Add(&session.Session{
		ID: "s2", Backend: stubBackend{}, StartedAt: time.Now(),
		Request: model.LaunchRequest{Mode: model.LaunchDebug, Target: ".", WorkDir: "/tmp"},
	})
	r := NewRegistry(store)

	_, out, err := r.stopDebugSession(context.Background(), nil, SessionRef{SessionID: "s2"})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(out.Message, "terminated") {
		t.Errorf("a debuggee this server started was not reported as terminated: %q", out.Message)
	}
}
