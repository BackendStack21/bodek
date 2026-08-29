package tui

import (
	"fmt"
	"io"

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
// or "" while none has landed (streaming turns and empty messages skipped).
func (m *Model) lastReply() string {
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if msg := m.msgs[i]; msg.role == roleAsst && !msg.streaming && msg.content != "" {
			return msg.content
		}
	}
	return ""
}

// copyLastReply puts the latest assistant reply on the system clipboard via
// OSC 52: the sequence is consumed by the terminal emulator itself, so no
// external tool runs and the payload never leaves the machine. Terminals
// without OSC 52 support silently ignore the sequence — the note says so.
func (m *Model) copyLastReply() tea.Cmd {
	text := m.lastReply()
	if text == "" {
		return m.transientNoteCmd("nothing to copy — no assistant reply yet")
	}
	if len(text) > osc52Cap {
		return m.transientNoteCmd(fmt.Sprintf("reply too large for OSC 52 (%d bytes) — select it manually", len(text)))
	}
	note := m.transientNoteCmd(fmt.Sprintf("copied %d chars via OSC 52 — needs a supporting terminal", len(text)))
	return tea.Batch(tea.Exec(&rawSeq{seq: ansi.SetSystemClipboard(text)}, nil), note)
}
