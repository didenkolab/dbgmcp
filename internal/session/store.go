// Package session tracks live debug sessions and resolves which one a tool call
// means.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/didenkolab/dbgmcp/internal/backend"
	"github.com/didenkolab/dbgmcp/internal/model"
)

type Session struct {
	ID        string
	Backend   backend.Backend
	Request   model.LaunchRequest
	StartedAt time.Time
	// OptimisationsDisabled is surfaced to the agent because when it is true the
	// binary under the debugger is not the binary that ships.
	OptimisationsDisabled bool
}

type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewStore() *Store { return &Store{sessions: map[string]*Session{}} }

func (s *Store) Add(sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID] = sess
}

func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *Store) List() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Resolve finds the session a call means. An empty id is allowed when exactly
// one session exists, because making an agent carry an id it could not have got
// wrong is a round trip spent on bookkeeping.
//
// With several sessions open the error lists them, so the agent can retry
// correctly instead of guessing.
func (s *Store) Resolve(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id != "" {
		sess, ok := s.sessions[id]
		if !ok {
			return nil, fmt.Errorf("Session not found: %s", id)
		}
		return sess, nil
	}
	switch len(s.sessions) {
	case 0:
		return nil, fmt.Errorf("No active debug session")
	case 1:
		for _, sess := range s.sessions {
			return sess, nil
		}
	}
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return nil, fmt.Errorf("Several debug sessions are open, so session_id is required. Open sessions: %v", ids)
}

// NewID is short on purpose: an agent copies it between calls, and a 36-byte
// UUID costs tokens on every one of them.
func NewID() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
