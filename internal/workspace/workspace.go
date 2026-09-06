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
		return s
	}
	s.all = f.Workspaces
	return s
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
	st := cloneState(s.all[cwd])
	fn(&st)
	s.all[cwd] = st
	path := s.path
	snap := cloneAll(s.all)
	s.mu.Unlock()
	_ = persist(path, snap)
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write workspace: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
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
