// Package tokens persists per-session auth tokens locally so bodek can resume,
// cancel, and delete sessions across runs.
//
// odek serve issues a session-scoped secret (the WS `session` event's
// auth_token) and requires it on the cancel/detail/delete endpoints. The Web
// UI keeps these in localStorage; bodek keeps them in ~/.bodek/sessions.json.
// Persistence is best-effort — a Store with no writable path still works as an
// in-memory cache for the current run.
package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store is a concurrency-safe session-id → token map with best-effort
// persistence.
type Store struct {
	mu   sync.Mutex
	m    map[string]string
	path string
}

// Open loads the token store from ~/.bodek/sessions.json. It never fails: on
// any error it returns an in-memory-only store.
func Open() *Store {
	s := &Store{m: map[string]string{}}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	s.path = filepath.Join(home, ".bodek", "sessions.json")
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &s.m)
	}
	return s
}

// Get returns the stored token for a session, or "" if unknown.
func (s *Store) Get(id string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[id]
}

// Set records a session's token and persists the store (best-effort).
func (s *Store) Set(id, token string) {
	if s == nil || id == "" || token == "" {
		return
	}
	s.mu.Lock()
	if s.m[id] == token {
		s.mu.Unlock()
		return // no change; skip the disk write
	}
	s.m[id] = token
	snapshot := make(map[string]string, len(s.m))
	for k, v := range s.m {
		snapshot[k] = v
	}
	path := s.path
	s.mu.Unlock()
	persist(path, snapshot)
}

// Delete removes a session's token and persists the store (best-effort).
func (s *Store) Delete(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	if _, ok := s.m[id]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.m, id)
	snapshot := make(map[string]string, len(s.m))
	for k, v := range s.m {
		snapshot[k] = v
	}
	path := s.path
	s.mu.Unlock()
	persist(path, snapshot)
}

func persist(path string, m map[string]string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
