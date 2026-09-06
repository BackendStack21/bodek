package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("BODEK_WORKSPACE", "")
	return dir
}

func TestLoadMissingIsEmpty(t *testing.T) {
	isolate(t)
	s := Open()
	got := s.Load("/tmp/proj")
	if got.SessionID != "" || len(got.History) != 0 || got.Draft != "" {
		t.Fatalf("missing file must load empty, got %+v", got)
	}
	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Fatal("Load must not create the file")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolate(t)
	s := Open()
	in := State{
		SessionID:    "sess-1",
		SessionTitle: "fix the login",
		History:      []string{"a", "b"},
		Draft:        "half written",
		Queue:        []string{"next"},
		Attachments:  []string{"/tmp/notes.md"},
	}
	if err := s.Save("/work/proj", in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Open().Load("/work/proj")
	if got.SessionID != in.SessionID || got.SessionTitle != in.SessionTitle {
		t.Errorf("session = %+v", got)
	}
	if len(got.History) != 2 || got.History[1] != "b" {
		t.Errorf("history = %v", got.History)
	}
	if got.Draft != "half written" || got.Queue[0] != "next" {
		t.Errorf("draft/queue = %+v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0] != "/tmp/notes.md" {
		t.Errorf("attachments = %v", got.Attachments)
	}
}

func TestCWDsAreIsolated(t *testing.T) {
	isolate(t)
	s := Open()
	if err := s.Save("/a", State{SessionID: "sa", Draft: "da"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("/b", State{SessionID: "sb"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Load("/a"); got.SessionID != "sa" || got.Draft != "da" {
		t.Errorf("/a = %+v", got)
	}
	if got := s.Load("/b"); got.SessionID != "sb" || got.Draft != "" {
		t.Errorf("/b = %+v", got)
	}
}

func TestClearSessionKeepsHistory(t *testing.T) {
	isolate(t)
	s := Open()
	_ = s.Save("/proj", State{
		SessionID: "old", SessionTitle: "t", History: []string{"p"},
		Draft: "d", Queue: []string{"q"},
	})
	s.ClearSession("/proj")
	got := Open().Load("/proj")
	if got.SessionID != "" || got.SessionTitle != "" {
		t.Errorf("session mapping survived /new: %+v", got)
	}
	if got.Draft != "" || len(got.Queue) != 0 {
		t.Errorf("draft/queue must wipe on /new: %+v", got)
	}
	if len(got.History) != 1 || got.History[0] != "p" {
		t.Errorf("history must survive /new: %v", got.History)
	}
}

func TestPatchMerges(t *testing.T) {
	isolate(t)
	s := Open()
	_ = s.Save("/p", State{SessionID: "s1", History: []string{"old"}})
	s.Patch("/p", func(st *State) {
		st.Draft = "typed"
		st.History = append(st.History, "new")
	})
	got := s.Load("/p")
	if got.SessionID != "s1" || got.Draft != "typed" || len(got.History) != 2 {
		t.Errorf("patch = %+v", got)
	}
}

func TestFileModeIsPrivate(t *testing.T) {
	isolate(t)
	s := Open()
	if err := s.Save("/p", State{Draft: "secret"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("workspaces.json perm = %o, want 0600", fi.Mode().Perm())
	}
	dir := filepath.Dir(Path())
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %o, want 0700", di.Mode().Perm())
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if got := s.Load("/x"); got.SessionID != "" {
		t.Errorf("nil Load = %+v", got)
	}
	if err := s.Save("/x", State{Draft: "n"}); err != nil {
		t.Errorf("nil Save = %v", err)
	}
	s.ClearSession("/x")
	s.Patch("/x", func(*State) {})
}

func TestBODEKWorkspaceOverride(t *testing.T) {
	dir := t.TempDir()
	alt := filepath.Join(dir, "ws.json")
	t.Setenv("HOME", dir)
	t.Setenv("BODEK_WORKSPACE", alt)
	s := Open()
	if err := s.Save("/p", State{SessionID: "x"}); err != nil {
		t.Fatal(err)
	}
	if Path() != alt {
		t.Errorf("Path = %q, want %q", Path(), alt)
	}
	if _, err := os.Stat(filepath.Join(dir, ".bodek")); !os.IsNotExist(err) {
		t.Error("override must not create ~/.bodek")
	}
}

func TestCorruptFileLoadsEmpty(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Open().Load("/p")
	if got.SessionID != "" {
		t.Errorf("corrupt file leaked state: %+v", got)
	}
}

func TestNoSecretsInFile(t *testing.T) {
	isolate(t)
	s := Open()
	_ = s.Save("/p", State{SessionID: "s1", Draft: "hello"})
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, banned := range []string{`"token"`, `"auth_token"`, `"api_key"`} {
		if strings.Contains(body, banned) {
			t.Errorf("secret field %s in workspace file: %s", banned, body)
		}
	}
}
