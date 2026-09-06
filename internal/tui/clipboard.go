package tui

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// osc52Cap is the payload size where terminal emulators start dropping
// OSC 52 writes (tmux buffers ~100KB, Windows Terminal ~150KB). Larger
// copies are refused with a note rather than silently truncated.
const osc52Cap = 100_000

// rawSeq is a tea.ExecCommand that writes a raw escape sequence to
// the terminal. tea.Println can't carry it: bodek runs on the alt-screen,
// where the renderer drops printed lines entirely. Exec briefly pauses the
// renderer and hands over the real terminal writer, so the sequence lands
// verbatim and frames can't interleave. Shared by the OSC 52 clipboard
// write and the attention layer (bell / window title / OSC 9 notify).
type rawSeq struct {
	seq string
	w   io.Writer
}

func (c *rawSeq) SetStdin(io.Reader)    {}
func (c *rawSeq) SetStderr(io.Writer)   {}
func (c *rawSeq) SetStdout(w io.Writer) { c.w = w }

func (c *rawSeq) Run() error {
	if c.w == nil {
		return nil // no terminal writer (tests, headless contexts)
	}
	_, err := io.WriteString(c.w, c.seq)
	return err
}

// restoreAfterExecMsg is sent when a tea.Exec finishes. ReleaseTerminal
// disables mouse reporting and the Shift+Enter keyboard protocol;
// RestoreTerminal does not put either back.
type restoreAfterExecMsg struct{}

func afterExec(error) tea.Msg { return restoreAfterExecMsg{} }

// restoreAfterExec re-arms modes torn down by tea.Exec. --plain has no
// mouse reporting (native scrollback owns the wheel).
func (m *Model) restoreAfterExec() tea.Cmd {
	EnableShiftEnterKeys()
	if m.plain {
		return nil
	}
	return tea.EnableMouseCellMotion
}

// lastReply returns the text of the most recent finalized assistant reply,
// or "" while none has landed (streaming turns, empty messages, and raw
// styled cards — /help, /stats — skipped; a raw card's content is ANSI,
// never copyable prose).
func (m *Model) lastReply() string {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if msg := m.msgs[i]; msg.role == roleAsst && !msg.streaming && !msg.raw && msg.content != "" {
			return msg.content
		}
	}
	return ""
}

// copyText puts text on the system clipboard. Local sessions exec a
// clipboard helper (pbcopy / wl-copy / clip) — definitive, no terminal
// support needed. Remote (SSH) sessions and helper-less machines fall back
// to OSC 52, which the terminal emulator itself must honor. Shared by
// copy-last-reply (^Y), copy-focused-turn (alt+y), and click-to-copy.
// Success is the footer ✓ Copied flash only — a transcript notice would
// duplicate it (and sit off-screen when the reader is up in history).
func (m *Model) copyText(text string) tea.Cmd {
	if text == "" {
		return m.transientNoteCmd("nothing to copy — no assistant reply yet")
	}
	if tool := clipboardTool(); tool != "" {
		return tea.Batch(m.copyViaExec(tool, text), m.copiedAck())
	}
	if len(text) > osc52Cap {
		return m.transientNoteCmd(fmt.Sprintf("reply too large to copy (%d bytes) — select and copy manually", len(text)))
	}
	return tea.Batch(m.copyTextOSC52(text), m.copiedAck())
}

// copiedAck arms the footer ✓ Copied flash for noticeTTL. A generation
// stamp drops a stale expire if the user copies again before it fades.
func (m *Model) copiedAck() tea.Cmd {
	m.copyFlashSeq++
	seq := m.copyFlashSeq
	m.copyFlashUntil = time.Now().Add(noticeTTL)
	return tea.Tick(noticeTTL, func(time.Time) tea.Msg {
		return copyFlashExpireMsg{seq: seq}
	})
}

// copyFlashing reports whether the footer should paint ✓ Copied.
func (m *Model) copyFlashing() bool {
	return !m.copyFlashUntil.IsZero() && time.Now().Before(m.copyFlashUntil)
}

// copyFlashExpireMsg clears the footer flash when its dwell elapses.
type copyFlashExpireMsg struct{ seq int }

// replyText is the copy payload for one assistant turn: the raw markdown
// blob (final or the partial stream so far). Raw styled cards are skipped.
func (m *Model) replyText(msgIdx int) string {
	if msgIdx < 0 || msgIdx >= len(m.msgs) {
		return ""
	}
	msg := m.msgs[msgIdx]
	if msg.role != roleAsst || msg.raw {
		return ""
	}
	return msg.content
}

// copyReplyAt copies the given turn's reply and parks focus on it so a
// follow-up alt+y copies the same card.
func (m *Model) copyReplyAt(msgIdx int) tea.Cmd {
	m.focusIdx = msgIdx
	return m.copyText(m.replyText(msgIdx))
}

// clipboardTool returns the local clipboard helper to exec, or "" when the
// session is remote (over SSH the user's clipboard lives on their machine —
// only OSC 52 can reach it) or no helper is installed.
func clipboardTool() string {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return ""
	}
	tool := "pbcopy"
	switch runtime.GOOS {
	case "linux":
		tool = "wl-copy"
	case "windows":
		tool = "clip"
	}
	if p, err := osexec.LookPath(tool); err == nil && p != "" {
		return tool
	}
	return ""
}

// copyViaExec pipes the payload into a local clipboard helper without
// tea.Exec: pbcopy / wl-copy / clip do not need the TTY, and ReleaseTerminal
// flickers the alt-screen (a click-to-copy would blank the transcript).
// Success was already acknowledged by copyText (✓ Copied); a failed write
// clears the flash and posts an alert from copyResultMsg.
func (m *Model) copyViaExec(tool, text string) tea.Cmd {
	return func() tea.Msg {
		c := osexec.Command(tool)
		c.Stdin = strings.NewReader(text)
		err := c.Run()
		return copyResultMsg{n: len(text), tool: tool, err: err}
	}
}

// copyTextOSC52 is the OSC 52 fallback write (remote or helper-less sessions).
func (m *Model) copyTextOSC52(text string) tea.Cmd {
	if len(text) > osc52Cap {
		return m.transientNoteCmd(fmt.Sprintf("reply too large to copy (%d bytes) — select and copy manually", len(text)))
	}
	return tea.Exec(&rawSeq{seq: ansi.SetSystemClipboard(text)}, afterExec)
}

// copyResultMsg reports the outcome of an exec-clipboard write.
type copyResultMsg struct {
	n    int
	tool string
	err  error
}

// focusedReply returns the reply of the turn the cursor last jumped to
// (alt+↑/alt+↓, find jumps), falling back to the latest reply when there
// is no focus or it went stale (messages trimmed, index now a user turn).
func (m *Model) focusedReply() string {
	if m.focusIdx >= 0 && m.focusIdx < len(m.msgs) {
		if msg := m.msgs[m.focusIdx]; msg.role == roleAsst && !msg.raw && msg.content != "" {
			return msg.content
		}
	}
	return m.lastReply()
}

// copyKind names the focused copy surface.
type copyKind int

const (
	copyReply copyKind = iota
	copyStep
	copyThink
)

// copySpan is the alt+m mark for a two-point yank.
type copySpan struct {
	msgIdx int
	kind   copyKind
	idx    int
	set    bool
}

// copyLastReply puts the latest assistant reply on the system clipboard.
func (m *Model) copyLastReply() tea.Cmd {
	return m.copyText(m.lastReply())
}

// copyFocusedTurn puts the focused surface on the clipboard: an open
// reasoning block, an expanded step, or the turn reply. After alt+m it
// yanks the sanitized range between the mark and the current focus.
func (m *Model) copyFocusedTurn() tea.Cmd {
	if m.copyMark.set {
		text := m.spanCopyText()
		m.copyMark = copySpan{}
		return m.copyText(text)
	}
	return m.copyText(m.focusedCopyText())
}

// focusedCopyText is the sanitized payload for the current inspect surface.
func (m *Model) focusedCopyText() string {
	idx := m.focusIdx
	if idx < 0 || idx >= len(m.msgs) || m.msgs[idx].role != roleAsst || m.msgs[idx].raw {
		return m.focusedReply()
	}
	msg := m.msgs[idx]
	for i := len(msg.items) - 1; i >= 0; i-- {
		if msg.items[i].thinking && msg.items[i].open && msg.items[i].text != "" {
			return sanitize(msg.items[i].text)
		}
	}
	for i := len(msg.steps) - 1; i >= 0; i-- {
		if msg.steps[i].expanded && msg.steps[i].result != "" {
			return sanitize(msg.steps[i].result)
		}
	}
	if msg.content != "" {
		return msg.content
	}
	return m.focusedReply()
}

func (m *Model) currentCopySpan() copySpan {
	idx := m.focusIdx
	if idx < 0 || idx >= len(m.msgs) {
		return copySpan{}
	}
	msg := m.msgs[idx]
	if msg.role != roleAsst || msg.raw {
		return copySpan{}
	}
	for i := len(msg.items) - 1; i >= 0; i-- {
		if msg.items[i].thinking && msg.items[i].open {
			return copySpan{msgIdx: idx, kind: copyThink, idx: i, set: true}
		}
	}
	for i := len(msg.steps) - 1; i >= 0; i-- {
		if msg.steps[i].expanded {
			return copySpan{msgIdx: idx, kind: copyStep, idx: i, set: true}
		}
	}
	return copySpan{msgIdx: idx, kind: copyReply, set: true}
}

func (m *Model) markCopySpan() tea.Cmd {
	sp := m.currentCopySpan()
	if !sp.set {
		return m.transientNoteCmd("nothing to mark — jump to a turn first")
	}
	m.copyMark = sp
	return m.transientNoteCmd("mark set — alt+y copies from here")
}

func (m *Model) spanCopyText() string {
	cur := m.currentCopySpan()
	if !m.copyMark.set || !cur.set {
		return m.focusedCopyText()
	}
	a, b := m.copyMark.msgIdx, cur.msgIdx
	if a > b {
		a, b = b, a
	}
	var parts []string
	for i := a; i <= b; i++ {
		if i < 0 || i >= len(m.msgs) {
			continue
		}
		msg := m.msgs[i]
		if msg.role != roleAsst || msg.raw || msg.content == "" {
			continue
		}
		parts = append(parts, msg.content)
	}
	if len(parts) == 0 {
		return m.focusedCopyText()
	}
	return strings.Join(parts, "\n\n")
}
