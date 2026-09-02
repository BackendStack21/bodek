package tui

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// W3 — clipboard & export. Regression tests for the judge-2 audit
// (see .ux-review/judge2_copy_export.md).

// F3/P1: raw cards (help/stats) carry styled ANSI — ^Y after /help must
// copy the last real reply, never the card.
func TestLastReplySkipsRawCards(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q"},
		message{role: roleAsst, content: "the real answer"},
		message{role: roleAsst, content: "\x1b[36m┌─ help card", rendered: "x", raw: true},
	)
	if got := m.lastReply(); got != "the real answer" {
		t.Errorf("lastReply = %q, want the real answer", got)
	}
	m.focusIdx = 2 // cursor parked on the raw card
	if got := m.focusedReply(); got != "the real answer" {
		t.Errorf("focusedReply = %q, want fallback to the real answer", got)
	}
}

// F1/F8: exports never silently overwrite — same base gets -1, -2… suffixes.
func TestWriteExportNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	p1, err := writeExport(dir, "sess-abc123", "md", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := writeExport(dir, "sess-abc123", "md", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Errorf("second export overwrote the first: %s", p1)
	}
	if !strings.Contains(p1, "sess-abc123") || !strings.HasSuffix(p1, ".md") {
		t.Errorf("filename %q lacks session slug and format", p1)
	}
	body, err := os.ReadFile(p2)
	if err != nil || string(body) != "two" {
		t.Errorf("suffix copy content = %q err %v", body, err)
	}
	if st, err := os.Stat(p1); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("export perm = %v, want 0600 (transcripts are sensitive)", st.Mode())
	}
}

// F1/P0: /export exists in the registry with an honest format guard.
func TestExportCommandRegistered(t *testing.T) {
	found := false
	for _, c := range slashCommands() {
		if c.name == "export" {
			found = true
		}
	}
	if !found {
		t.Fatal("/export is not in the slash registry")
	}
	m := newTestModel()
	m.sessionID = "sess-abc123"
	if cmd := runExport(m, "yaml"); cmd == nil {
		t.Fatal("unknown format must produce an explanatory note")
	}
	if cmd := runExport(m, "md"); cmd == nil {
		t.Fatal("valid format must produce an export command")
	}
}

// F2/P0: remote sessions skip exec helpers — the clipboard that matters is
// on the machine running the terminal.
func TestClipboardToolRemotePrefersOSC52(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys004")
	if got := clipboardTool(); got != "" {
		t.Errorf("clipboardTool over SSH = %q, want empty (OSC 52 path)", got)
	}
}

func TestClipboardToolLocalPrefersExec(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("exec-helper probe asserted on darwin only")
	}
	if _, err := os.Stat("/usr/bin/pbcopy"); err != nil {
		t.Skip("pbcopy not present")
	}
	if got := clipboardTool(); got != "pbcopy" {
		t.Errorf("clipboardTool = %q, want pbcopy", got)
	}
}

var _ = client.Event{} // keep the client import for parity with sibling tests
