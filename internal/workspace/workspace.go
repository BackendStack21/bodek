// Package workspace persists per-cwd bodek-owned state so a relaunch in
// the same directory can resume the last session, restore an unsent draft
// and queue, and recall prompt history. It never stores auth tokens or
// API keys — those stay in the tokens package / the environment.
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// State is the bodek-owned snapshot for one working directory.
type State struct {
	SessionID    string   `json:"session_id,omitempty"`
	SessionTitle string   `json:"session_title,omitempty"`
	History      []string `json:"history,omitempty"`
	Draft        string   `json:"draft,omitempty"`
	Queue        []string `json:"queue,omitempty"`
	Attachments  []string `json:"attachments,omitempty"` // paths only; re-read on restore
}

// Store is a concurrency-safe cwd → State map with best-effort persistence.
type Store struct {
	mu   sync.Mutex
	all  map[string]State
	path string
}

type fileFormat struct {
	Workspaces map[string]State `json:"workspaces"`
}

// Path returns the workspace file location: $BODEK_WORKSPACE if set, else
// ~/.bodek/workspaces.json.
func Path() string {
	if p := os.Getenv("BODEK_WORKSPACE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".bodek", "workspaces.json")
}

// Open loads the store. It never fails: a missing or corrupt file yields
// an empty in-memory map. Persistence is skipped when Path is empty.
func Open() *Store {
	s := &Store{all: map[string]State{}, path: Path()}
	if s.path == "" {
		return s
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var f fileFormat
	if json.Unmarshal(data, &f) != nil || f.Workspaces == nil {
		// Corrupt on disk: quarantine instead of silently resetting, so a
		// torn write never destroys every draft/queue/session undiagnosably
		// (mirrors tokens.go, including backup rotation).
		if qerr := quarantine(s.path); qerr == nil {
			fmt.Fprintf(os.Stderr, "bodek: warning: corrupt %s quarantined as %s.corrupt\n", s.path, s.path)
		} else {
			// Quarantine failed: never overwrite bytes we could not parse.
			fmt.Fprintf(os.Stderr, "bodek: warning: corrupt %s kept in place: %v\n", s.path, qerr)
			s.path = ""
		}
		return s
	}
	s.all = f.Workspaces
	return s
}

// quarantine sets a corrupt store aside as <path>.corrupt, rotating any
// earlier backup to .corrupt.1 so repeat corruption never destroys the
// previous quarantined evidence (POSIX rename replaces its destination).
func quarantine(path string) error {
	dst := path + ".corrupt"
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, dst+".1"); err != nil {
			return err
		}
	}
	return os.Rename(path, dst)
}

// reloadLocked re-reads the on-disk store and adopts the on-disk state for
// every cwd EXCEPT `except` (the caller is about to overwrite that one —
// its in-memory value is the newest). Another bodek instance may have
// persisted since Open: merging only foreign additions let our stale
// copies of other directories republish dead drafts, queues, and session
// ids over the fresher disk state (e.g. a /new in another instance
// resurrected here).
func (s *Store) reloadLocked(except string) {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // missing or unreadable: keep what we have
	}
	var f fileFormat
	if json.Unmarshal(data, &f) != nil || f.Workspaces == nil {
		return
	}
	for cwd, st := range f.Workspaces {
		if cwd != except {
			s.all[cwd] = st
		}
	}
}

// Load returns the snapshot for cwd, or a zero State.
func (s *Store) Load(cwd string) State {
	if s == nil || cwd == "" {
		return State{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.all[cwd])
}

// Save replaces the snapshot for cwd and persists.
func (s *Store) Save(cwd string, st State) error {
	if s == nil || cwd == "" {
		return nil
	}
	s.mu.Lock()
	s.reloadLocked(cwd)
	s.all[cwd] = cloneState(st)
	path := s.path
	snap := cloneAll(s.all)
	s.mu.Unlock()
	return persist(path, snap)
}

// Patch applies fn to the cwd snapshot and persists.
func (s *Store) Patch(cwd string, fn func(*State)) {
	if s == nil || cwd == "" || fn == nil {
		return
	}
	s.mu.Lock()
	s.reloadLocked(cwd)
	st := cloneState(s.all[cwd])
	fn(&st)
	s.all[cwd] = st
	path := s.path
	snap := cloneAll(s.all)
	s.mu.Unlock()
	// A failed persist is not silent: draft/queue loss on a full disk or a
	// read-only directory must be diagnosable, like tokens' warnPersist.
	if err := persist(path, snap); err != nil {
		fmt.Fprintf(os.Stderr, "bodek: warning: workspace not saved: %v\n", err)
	}
}

// ClearSession drops the resume mapping and any unsent draft/queue for
// cwd (/new). Prompt history is kept so ^P still recalls this directory.
func (s *Store) ClearSession(cwd string) {
	if s == nil || cwd == "" {
		return
	}
	s.Patch(cwd, func(st *State) {
		st.SessionID = ""
		st.SessionTitle = ""
		st.Draft = ""
		st.Queue = nil
		st.Attachments = nil
	})
}

func persist(path string, all map[string]State) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}
	data, err := json.MarshalIndent(fileFormat{Workspaces: all}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace: %w", err)
	}
	// A per-call staged name: callers persist after releasing the store
	// mutex, so a shared path+'.tmp' let two interleaved writes tear the
	// file (A.Write → B.Write truncates → A.Rename) — the same tear
	// tokens.go already fixed.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("stage workspace: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write workspace: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write workspace: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("protect workspace: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace workspace: %w", err)
	}
	return nil
}

func cloneState(s State) State {
	out := s
	if s.History != nil {
		out.History = append([]string(nil), s.History...)
	}
	if s.Queue != nil {
		out.Queue = append([]string(nil), s.Queue...)
	}
	if s.Attachments != nil {
		out.Attachments = append([]string(nil), s.Attachments...)
	}
	return out
}

func cloneAll(m map[string]State) map[string]State {
	out := make(map[string]State, len(m))
	for k, v := range m {
		out[k] = cloneState(v)
	}
	return out
}
