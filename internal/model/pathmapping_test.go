package model

import "testing"

func TestPathMappingRoundTripsALocation(t *testing.T) {
	// The property that makes "set a breakpoint, then recognise where we
	// stopped" work: a path must survive agent -> runtime -> agent unchanged.
	m := PathMapping{Rules: []PathRule{{From: "/Users/me/app", To: "/srv/app"}}}

	runtime := m.ToRuntime("/Users/me/app/internal/billing/charge.go")
	if runtime != "/srv/app/internal/billing/charge.go" {
		t.Fatalf("ToRuntime = %q", runtime)
	}
	if back := m.ToAgent(runtime); back != "/Users/me/app/internal/billing/charge.go" {
		t.Fatalf("round trip lost the path: %q", back)
	}
}

func TestPathMappingLeavesUnmatchedPathsAlone(t *testing.T) {
	m := PathMapping{Rules: []PathRule{{From: "/a", To: "/b"}}}
	if got := m.ToRuntime("/elsewhere/x.go"); got != "/elsewhere/x.go" {
		t.Fatalf("rewrote an unmatched path: %q", got)
	}
}

func TestPathMappingFirstRuleWins(t *testing.T) {
	m := PathMapping{Rules: []PathRule{
		{From: "/a/b", To: "/specific"},
		{From: "/a", To: "/general"},
	}}
	if got := m.ToRuntime("/a/b/c.go"); got != "/specific/c.go" {
		t.Fatalf("expected the first matching rule to win, got %q", got)
	}
}

func TestEmptyMappingIsIdentity(t *testing.T) {
	var m PathMapping
	if got := m.ToRuntime("/x/y.go"); got != "/x/y.go" {
		t.Fatalf("empty mapping changed the path: %q", got)
	}
}
