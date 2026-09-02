package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// attentionKind selects the terminal state the attention layer announces.
type attentionKind int

const (
	attentionDone      attentionKind = iota // a turn finished (done event)
	attentionApproval                       // an approval is waiting (approval_request)
	attentionJobDone                        // a background job exited cleanly (jobs watcher)
	attentionJobFailed                      // a background job failed / timed out / was killed
)

// attention is the plan of terminal-attention effects for one state change.
// The window title always updates — it is silent and keeps tmux/terminal
// window lists truthful. The bell (--bel=false mutes) and the desktop
// notification (--notify enables, OSC 9) are user-gated. Fires only on
// terminal states, never on streamed tokens.
type attention struct {
	title  string // OSC 0 window title ("" = leave the title alone)
	bell   bool   // ring the terminal bell
	notify string // OSC 9 desktop notification text ("" = none)
}

func (a attention) empty() bool {
	return a.title == "" && !a.bell && a.notify == ""
}

// sequence renders the plan as one raw terminal write: title, notification,
// bell — in that order. Every embedded string is pre-sanitized by the
// planner, so control bytes from the wire cannot inject escapes here.
func (a attention) sequence() string {
	var b strings.Builder
	if a.title != "" {
		b.WriteString(ansi.SetWindowTitle(a.title))
	}
	if a.notify != "" {
		b.WriteString("\x1b]9;" + a.notify + "\x07")
	}
	if a.bell {
		b.WriteString("\a")
	}
	return b.String()
}

// attentionFor decides the attention plan for a terminal state. Wire-borne
// pieces (the model name) go through collapse() — sanitize + whitespace
// flatten — because both consumers are raw escape-sequence payloads.
func (m *Model) attentionFor(kind attentionKind) attention {
	model := collapse(m.model)
	var prefix, note string
	switch kind {
	case attentionApproval:
		prefix, note = "⚠ approval needed", "bodek: approval needed"
	case attentionDone:
		prefix, note = "✓ done", "bodek: turn complete"
	case attentionJobDone:
		prefix, note = "✓ bg job done", "bodek: background job finished"
	case attentionJobFailed:
		prefix, note = "✗ bg job failed", "bodek: background job failed"
	default:
		return attention{}
	}
	a := attention{bell: m.bell}
	if model != "" {
		a.title = prefix + " — " + model
		a.notify = note + " — " + model
	} else {
		a.title = prefix
		a.notify = note
	}
	if !m.notify {
		a.notify = ""
	}
	return a
}

// attentionCmd materializes the plan as a single raw write via tea.Exec —
// the same escape hatch as the OSC 52 clipboard write. Nil when all-muted.
func (m *Model) attentionCmd(a attention) tea.Cmd {
	if a.empty() {
		return nil
	}
	return tea.Exec(&rawSeq{seq: a.sequence()}, nil)
}
