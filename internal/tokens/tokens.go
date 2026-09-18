// Package tokens persists per-session auth tokens locally so bodek can resume,
// cancel, and delete sessions across runs.
//
// odek serve issues a session-scoped secret (the WS `session` event's
// auth_token) and requires it on the cancel/detail/delete endpoints. The Web
// UI keeps these in localStorage; bodek keeps them in ~/.bodek/sessions.json.
// Persistence is best-effort — a Store with no writable path still works as an
// in-memory cache for the current run; failures are reported on stderr.
package tokens

import (
	"encoding/json"
	"fmt"
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

// Open loads the token store from ~/.bodek/sessions.json. It never fails:
// any problem yields an in-memory-only store.
func Open() *Store {
	home, err := os.UserHomeDir()
	if err != nil {
		return &Store{m: map[string]string{}}
	}
	return openAt(filepath.Join(home, ".bodek", "sessions.json"))
}

// openAt loads a store from an explicit path (test seam). A corrupt store is
// quarantined as <path>.corrupt instead of being silently overwritten —
// silently dropping every saved token on one bad write made resume loss
// undiagnosable.
func openAt(path string) *Store {
	s := &Store{m: map[string]string{}, path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s // missing store: fresh start
	}
	if err := json.Unmarshal(data, &s.m); err != nil {
		if qErr := os.Rename(path, path+".corrupt"); qErr == nil {
			warnPersist(fmt.Errorf("corrupt store quarantined as %s.corrupt: %w", path, err))
		} else {
			warnPersist(fmt.Errorf("corrupt store kept in place: %w", err))
			s.path = "" // never overwrite bytes we could not parse
		}
		s.m = map[string]string{}
		return s
	}
	if s.m == nil { // JSON null unmarshals into a nil map
		s.m = map[string]string{}
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
	defer s.mu.Unlock()
	if s.m[id] == token {
		return // no change; skip the disk write
	}
	s.m[id] = token
	s.persistLocked()
}

// Delete removes a session's token and persists the store (best-effort).
func (s *Store) Delete(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return
	}
	delete(s.m, id)
	s.persistLocked()
}

// persistLocked writes the store while the mutex is held. Snapshot-then-
// persist-outside-the-lock let interleaved Set/Delete writes reorder on
// disk: an older snapshot landing last resurrected deleted tokens and
// dropped minted ones, silently breaking session resume.
func (s *Store) persistLocked() {
	snapshot := make(map[string]string, len(s.m))
	for k, v := range s.m {
		snapshot[k] = v
	}
	if err := persist(s.path, snapshot); err != nil {
		warnPersist(err)
	}
}

// persist atomically writes the store (staged .tmp + rename) so a crash
// mid-write never corrupts the previous snapshot.
func persist(path string, m map[string]string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}
	// A per-call staged name: callers persist after releasing the store
	// mutex, so a shared path+'.tmp' let two interleaved writes tear the
	// file (A.Write → B.Write truncates → A.Rename) and silently wipe
	// the token store on next Open.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("stage store: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write store: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("protect store: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName) // don't leave the staged copy behind
		return fmt.Errorf("replace store: %w", err)
	}
	return nil
}

// warnPersist reports a failed best-effort save without aborting the
// operation: the store stays a working in-memory cache, but a silent failure
// would break session resume with no diagnostic.
func warnPersist(err error) {
	fmt.Fprintf(os.Stderr, "bodek: warning: session token store not saved: %v\n", err)
}
