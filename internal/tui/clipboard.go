package tui

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"

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
// to OSC 52, which the terminal emulator itself must honor — the note says
// so honestly, because a silent OSC 52 no-op leaves the user pasting their
// previous clipboard. Shared by copy-last-reply (^Y) and copy-focused-turn
// (alt+y).
func (m *Model) copyText(text string) tea.Cmd {
	if text == "" {
		return m.transientNoteCmd("nothing to copy — no assistant reply yet")
	}
	if tool := clipboardTool(); tool != "" {
		return m.copyViaExec(tool, text)
	}
	return m.copyTextOSC52(text)
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

// copyViaExec pipes the payload into a local clipboard helper and reports
// the real outcome — the note is emitted from the command's completion, so
// a failed write can never claim success.
func (m *Model) copyViaExec(tool, text string) tea.Cmd {
	c := osexec.Command(tool)
	stdin, err := c.StdinPipe()
	if err != nil {
		return m.copyTextOSC52(text)
	}
	go func() {
		_, _ = io.WriteString(stdin, text)
		_ = stdin.Close()
	}()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return copyResultMsg{n: len(text), tool: tool, err: err}
	})
}

// copyTextOSC52 is the OSC 52 fallback write (remote or helper-less sessions).
func (m *Model) copyTextOSC52(text string) tea.Cmd {
	if len(text) > osc52Cap {
		return m.transientNoteCmd(fmt.Sprintf("reply too large to copy (%d bytes) — select and copy manually", len(text)))
	}
	note := m.transientNoteCmd("copied to the system clipboard — if nothing appears, your terminal doesn't support it")
	return tea.Batch(tea.Exec(&rawSeq{seq: ansi.SetSystemClipboard(text)}, nil), note)
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

// copyLastReply puts the latest assistant reply on the system clipboard.
func (m *Model) copyLastReply() tea.Cmd {
	return m.copyText(m.lastReply())
}

// copyFocusedTurn puts the focused turn's reply on the clipboard — any
// earlier answer, not just the newest one.
func (m *Model) copyFocusedTurn() tea.Cmd {
	return m.copyText(m.focusedReply())
}
