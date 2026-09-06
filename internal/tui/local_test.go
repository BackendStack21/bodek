package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/workspace"
)

func testWorkspace(t *testing.T) (*workspace.Store, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("BODEK_WORKSPACE", filepath.Join(dir, "ws.json"))
	cwd := filepath.Join(dir, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return workspace.Open(), cwd
}

func modelWithWS(t *testing.T) *Model {
	t.Helper()
	ws, cwd := testWorkspace(t)
	m := newTestModel()
	m.ws = ws
	m.opts.CWD = cwd
	m.opts.Workspace = ws
	return m
}

func TestRecordHistoryPersists(t *testing.T) {
	m := modelWithWS(t)
	m.recordHistory("run the tests")
	got := m.ws.Load(m.opts.CWD)
	if len(got.History) != 1 || got.History[0] != "run the tests" {
		t.Fatalf("history not persisted: %+v", got.History)
	}
	m2 := newTestModel()
	m2.ws, m2.opts.CWD = m.ws, m.opts.CWD
	m2.restoreWorkspace()
	if len(m2.history) != 1 || m2.history[0] != "run the tests" {
		t.Fatalf("history not restored: %v", m2.history)
	}
}

func TestDraftQueueAttachmentsSurviveRelaunch(t *testing.T) {
	m := modelWithWS(t)
	file := filepath.Join(m.opts.CWD, "notes.md")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.ta.SetValue("half written")
	m.queue = []string{"next turn"}
	if cmd := m.attachFile(file); cmd == nil {
		t.Fatal("attach produced no cmd")
	}
	m.persistLocal()

	m2 := newTestModel()
	m2.ws, m2.opts.CWD = m.ws, m.opts.CWD
	m2.restoreWorkspace()
	if m2.ta.Value() != "half written" {
		t.Errorf("draft = %q", m2.ta.Value())
	}
	if len(m2.queue) != 1 || m2.queue[0] != "next turn" {
		t.Errorf("queue = %v", m2.queue)
	}
	if len(m2.attachments) != 1 || m2.attachments[0].Name != "notes.md" {
		t.Errorf("attachments = %+v", m2.attachments)
	}
	if m2.attachments[0].Content != "hello" {
		t.Errorf("restaged content = %q", m2.attachments[0].Content)
	}
}

func TestPersistDoesNotAutoSendQueue(t *testing.T) {
	m := modelWithWS(t)
	m.queue = []string{"held"}
	m.persistLocal()
	m2 := newTestModel()
	m2.ws, m2.opts.CWD = m.ws, m.opts.CWD
	m2.restoreWorkspace()
	if m2.busy {
		t.Fatal("restore must not start a turn")
	}
	if len(m2.queue) != 1 {
		t.Fatalf("queue drained on restore: %v", m2.queue)
	}
}

func TestNewClearsResumeKeepsHistory(t *testing.T) {
	m := modelWithWS(t)
	m.sessionID = "sess-old"
	m.history = []string{"yesterday"}
	m.ta.SetValue("draft")
	m.queue = []string{"q"}
	m.rememberSession("old title")
	m.persistLocal()
	m.startFreshSession()
	got := m.ws.Load(m.opts.CWD)
	if got.SessionID != "" || got.Draft != "" || len(got.Queue) != 0 {
		t.Errorf("/new must drop resume+draft: %+v", got)
	}
	if len(got.History) != 1 || got.History[0] != "yesterday" {
		t.Errorf("history must survive /new: %v", got.History)
	}
}

func TestRestoreMarksPendingResume(t *testing.T) {
	ws, cwd := testWorkspace(t)
	_ = ws.Save(cwd, workspace.State{SessionID: "sess-9", SessionTitle: "fix login"})
	m := newTestModel()
	m.ws, m.opts.CWD = ws, cwd
	m.restoreWorkspace()
	if m.pendingResume != "sess-9" {
		t.Errorf("pendingResume = %q", m.pendingResume)
	}
	if m.resumeTitle != "fix login" {
		t.Errorf("resumeTitle = %q", m.resumeTitle)
	}
}

func TestFreshSkipsResume(t *testing.T) {
	ws, cwd := testWorkspace(t)
	_ = ws.Save(cwd, workspace.State{SessionID: "sess-9", SessionTitle: "fix login"})
	m := newTestModel()
	m.ws, m.opts.CWD, m.opts.Fresh = ws, cwd, true
	m.restoreWorkspace()
	if m.pendingResume != "" {
		t.Errorf("--new must not pending-resume: %q", m.pendingResume)
	}
	if m.resumeTitle != "fix login" {
		t.Errorf("title still orients the home card: %q", m.resumeTitle)
	}
}

func TestWelcomeShowsLastSession(t *testing.T) {
	m := newTestModel()
	m.resumeTitle = "fix the login bug"
	out := plain(welcome(m.th, 80, "/tmp/proj", m.resumeTitle))
	if !strings.Contains(out, "fix the login bug") {
		t.Errorf("welcome missing last session:\n%s", out)
	}
	if !strings.Contains(out, "/new") {
		t.Errorf("welcome must teach /new as the escape:\n%s", out)
	}
}

func TestQuitPersistsDraft(t *testing.T) {
	m := modelWithWS(t)
	m.ta.SetValue("do not lose this")
	m.quitting = true
	m.persistLocal()
	got := m.ws.Load(m.opts.CWD)
	if got.Draft != "do not lose this" {
		t.Errorf("quit draft = %q", got.Draft)
	}
}
