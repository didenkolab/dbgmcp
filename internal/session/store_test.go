package session

import (
	"strings"
	"testing"
	"time"
)

func newSession(id string) *Session {
	return &Session{ID: id, StartedAt: time.Now()}
}

func TestResolveAllowsAnEmptyIDWhenOnlyOneSessionExists(t *testing.T) {
	// Making an agent carry an id it could not have got wrong is a round trip
	// spent on bookkeeping.
	s := NewStore()
	s.Add(newSession("abc"))

	got, err := s.Resolve("")
	if err != nil || got.ID != "abc" {
		t.Fatalf("Resolve(\"\") = %v, %v", got, err)
	}
}

func TestResolveListsTheOptionsWhenAmbiguous(t *testing.T) {
	s := NewStore()
	s.Add(newSession("aaa"))
	s.Add(newSession("bbb"))

	_, err := s.Resolve("")
	if err == nil {
		t.Fatal("expected ambiguity to be an error")
	}
	// The agent must be able to retry correctly from the message alone.
	if !strings.Contains(err.Error(), "aaa") || !strings.Contains(err.Error(), "bbb") {
		t.Fatalf("error does not list the open sessions: %v", err)
	}
}

func TestResolveReportsTheTwoEmptyCasesDifferently(t *testing.T) {
	s := NewStore()
	if _, err := s.Resolve(""); err == nil || !strings.Contains(err.Error(), "No active debug session") {
		t.Fatalf("empty store: %v", err)
	}
	s.Add(newSession("aaa"))
	if _, err := s.Resolve("zzz"); err == nil || !strings.Contains(err.Error(), "Session not found: zzz") {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if seen[id] {
			t.Fatalf("duplicate id after %d draws: %s", i, id)
		}
		seen[id] = true
	}
}
