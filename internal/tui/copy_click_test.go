package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// clickTranscript sends a left press on a viewport content line, using
// the same screen math the mouse dispatcher uses (header is 2 rows).
func clickTranscript(m *Model, line int) {
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Y: 2 + line})
}

func TestReplyCardIndexedForCopy(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "hello from the card"})
	_ = m.conversation()
	if len(m.replyLineIndex) != 1 {
		t.Fatalf("reply index = %d, want 1: %+v", len(m.replyLineIndex), m.replyLineIndex)
	}
	ref := m.replyLineIndex[0]
	if ref.msgIdx != 0 || ref.stepIdx != replyCardIdx {
		t.Fatalf("reply ref = %+v, want msg 0 card", ref)
	}
	if _, ok := m.replyAtLine(ref.line); !ok {
		t.Fatal("replyAtLine missed the card's first line")
	}
	if _, ok := m.replyAtLine(ref.end); !ok {
		t.Fatal("replyAtLine missed the card's last line")
	}
	if _, ok := m.replyAtLine(ref.line - 1); ok {
		t.Fatal("replyAtLine matched the turn head")
	}
}

func TestClickReplyCardCopiesAndAcks(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001") // force OSC 52 — no local helper
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "partial or final prose"})
	m.refresh()
	if len(m.replyLineIndex) != 1 {
		t.Fatalf("reply index = %d, want 1", len(m.replyLineIndex))
	}

	clickTranscript(m, m.replyLineIndex[0].line)
	if m.focusIdx != 0 {
		t.Errorf("focusIdx = %d, want 0 (click parks the copy target)", m.focusIdx)
	}
	if note, _ := lastNoteMatching(m, "Copied"); note != "" {
		t.Fatalf("copy must not post a transcript notice: %v", m.notices)
	}
	if !m.copyFlashing() {
		t.Fatal("footer flash was not armed")
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "Copied") {
		t.Errorf("footer missing Copied indicator: %q", foot)
	}
}

func TestClickStreamingCardCopiesPartial(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001")
	m := newTestModel()
	m.msgs = append(m.msgs, message{
		role:      roleAsst,
		content:   "half the answer",
		streaming: true,
		items:     []turnItem{{reply: true, text: "half the answer"}},
	})
	m.refresh()
	if got := m.replyText(0); got != "half the answer" {
		t.Fatalf("replyText = %q, want the partial stream", got)
	}
	if len(m.replyLineIndex) == 0 {
		t.Fatal("streaming card was not indexed")
	}
	clickTranscript(m, m.replyLineIndex[0].line)
	if !m.copyFlashing() {
		t.Fatal("streaming click did not arm the footer flash")
	}
	if note, _ := lastNoteMatching(m, "Copied"); note != "" {
		t.Fatalf("streaming copy posted a transcript notice: %v", m.notices)
	}
}

func TestClickTurnHeadStillFolds(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "fold me"})
	m.refresh()
	if len(m.turnLineIndex) != 1 {
		t.Fatalf("turn index = %d, want 1", len(m.turnLineIndex))
	}
	clickTranscript(m, m.turnLineIndex[0].line)
	if !m.msgs[0].collapsed {
		t.Fatal("click on turn head did not fold the turn")
	}
	if m.copyFlashing() {
		t.Fatal("folding the head must not copy")
	}
}

func TestClickStepHeaderStillToggles(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst,
		content: "line a\nline b\nline c\nline d\nline e",
		steps:   []step{{name: "read", done: true, result: "ok"}},
	})
	m.refresh()
	if len(m.stepLineIndex) != 1 {
		t.Fatalf("step index = %d, want 1", len(m.stepLineIndex))
	}
	clickTranscript(m, m.stepLineIndex[0].line)
	if !m.msgs[0].steps[0].expanded {
		t.Fatal("click on the step header did not toggle it")
	}
	if m.copyFlashing() {
		t.Fatal("step header click must not copy")
	}
}

func TestRawCardIsNotACopyTarget(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleAsst, content: "real reply"},
		message{role: roleAsst, content: "help ansi", raw: true},
	)
	_ = m.conversation()
	if len(m.replyLineIndex) != 1 || m.replyLineIndex[0].msgIdx != 0 {
		t.Fatalf("reply index = %+v, want only the real reply", m.replyLineIndex)
	}
	if m.replyText(1) != "" {
		t.Error("replyText must skip raw cards")
	}
}

func TestCollapsedSummaryIsCopyable(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/ttys001")
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "hidden answer", collapsed: true})
	m.refresh()
	if len(m.replyLineIndex) != 1 {
		t.Fatalf("collapsed summary not indexed: %+v", m.replyLineIndex)
	}
	clickTranscript(m, m.replyLineIndex[0].line)
	if !m.copyFlashing() {
		t.Fatal("collapsed click did not arm the footer flash")
	}
	if note, _ := lastNoteMatching(m, "Copied"); note != "" {
		t.Fatalf("collapsed copy posted a transcript notice: %v", m.notices)
	}
	if got := m.replyText(0); got != "hidden answer" {
		t.Errorf("collapsed copy payload = %q", got)
	}
}

func TestCopyFlashFades(t *testing.T) {
	m := newTestModel()
	if cmd := m.copiedAck(); cmd == nil {
		t.Fatal("copiedAck must arm the expire tick")
	}
	if !strings.Contains(plain(m.footer()), "Copied") {
		t.Fatal("footer should show Copied while the flash is live")
	}
	m.Update(copyFlashExpireMsg{seq: m.copyFlashSeq - 1})
	if !m.copyFlashing() {
		t.Fatal("stale expire cleared a live flash")
	}
	m.Update(copyFlashExpireMsg{seq: m.copyFlashSeq})
	if m.copyFlashing() {
		t.Fatal("expire did not clear the flash")
	}
	if strings.Contains(plain(m.footer()), "Copied") {
		t.Error("footer still shows Copied after expiry")
	}
}

func TestReplyTextGuards(t *testing.T) {
	m := newTestModel()
	if m.replyText(0) != "" {
		t.Error("empty transcript should yield no payload")
	}
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "prompt"},
		message{role: roleAsst, content: "answer"},
	)
	if m.replyText(0) != "" {
		t.Error("user turns are not copy targets")
	}
	if m.replyText(1) != "answer" {
		t.Errorf("replyText(1) = %q, want answer", m.replyText(1))
	}
}

func TestCopyResultFailureClearsFlash(t *testing.T) {
	m := newTestModel()
	m.copiedAck()
	_, _ = m.Update(copyResultMsg{n: 4, tool: "pbcopy", err: errTest{}})
	if m.copyFlashing() {
		t.Fatal("failed copy left the footer flash armed")
	}
	if note, _ := lastNoteMatching(m, "copy failed"); note == "" {
		t.Fatalf("failure posted no alert: %v", m.notices)
	}
}
