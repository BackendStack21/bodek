package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// W2 — visible state & the escape stack. Regression tests for the
// judge-3 interaction audit (see .ux-review/judge3_interaction.md).

// F1/P0: an armed stop-agent gate must be visible when armed from the
// composer (panel == none) — footer carries the headline and the y confirm.
func TestStopAgentGateVisibleFromComposer(t *testing.T) {
	m := newTestModel()
	m.panel = panelNone
	m.armStopAgent("t1", "SA1")
	foot := m.footer()
	if !strings.Contains(foot, "stop sub-agent SA1?") || !strings.Contains(foot, "y") {
		t.Errorf("armed stop-agent gate invisible in composer footer:\n%s", foot)
	}
}

// F2/P1: a disarmed printable rune falls through to the composer — the gate
// eats exactly one decision key, never the user's first keystroke.
func TestDisarmedRuneFallsThroughToComposer(t *testing.T) {
	m := newTestModel()
	m.panel = panelNone
	m.busy = true
	m.Update(key("esc")) // arms the cancel gate
	if m.confirm != confirmCancel {
		t.Fatalf("gate not armed: %v", m.confirm)
	}
	m.Update(key("x")) // any non-y key disarms — and must reach the composer
	if m.confirm != confirmNone {
		t.Errorf("gate still armed after non-y key")
	}
	if !strings.Contains(m.ta.Value(), "x") {
		t.Errorf("disarmed rune lost, composer = %q", m.ta.Value())
	}
}

// F4/P1: lowercase n is next (vim/less reflex), never query corruption.
func TestFindNextBinding(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "alpha one"},
		message{role: roleAsst, content: "alpha two alpha three"},
	)
	m.openFind()
	for _, r := range "alpha" {
		m.Update(key(string(r)))
	}
	if len(m.find.matches) == 0 {
		t.Fatal("no matches indexed")
	}
	start := m.find.sel
	m.Update(key("n"))
	if m.find.sel != start+1 {
		t.Errorf("n did not advance the match: sel %d -> %d", start, m.find.sel)
	}
	if string(m.find.query) != "alpha" {
		t.Errorf("n corrupted the query: %q", string(m.find.query))
	}
}

// F10/P2: queue deletes are two-step — first d arms, second d deletes.
func TestQueueDeleteTwoStep(t *testing.T) {
	m := newTestModel()
	m.queue = []string{"alpha", "beta"}
	m.qfocus = true
	m.qsel = 0
	m.Update(key("d"))
	if len(m.queue) != 2 {
		t.Fatalf("first d deleted immediately — queue: %v", m.queue)
	}
	m.Update(key("d"))
	if len(m.queue) != 1 || m.queue[0] != "beta" {
		t.Errorf("second d did not delete: %v", m.queue)
	}
	// Rearming on a new selection.
	m.Update(key("d"))
	if len(m.queue) != 1 {
		t.Errorf("rearm deleted without confirm: %v", m.queue)
	}
}

// Judge-1 P0: the events tab selection must be rendered — panelSel draws a
// cursor on the selected row.
func TestEventsSelectionRendered(t *testing.T) {
	m := newTestModel()
	m.panel = panelEvents
	m.feed = []client.RuntimeEvent{
		{Type: "token"},
		{Type: "tool_call", Tool: "read_file"},
	}
	m.panelSel = 1
	rows := m.eventRows(40)
	if len(rows) != 2 {
		t.Fatalf("eventRows = %d rows, want 2", len(rows))
	}
	if !strings.Contains(rows[1], "›") {
		t.Errorf("selected row has no cursor: %q", rows[1])
	}
	if strings.Contains(rows[0], "›") {
		t.Errorf("unselected row shows a cursor: %q", rows[0])
	}
}

// F3/P1: /help teaches only real keys — no phantom r, and the marquee
// features (^S stop sub-agent, alt+f find, ^K palette) are listed.
func TestHelpTeachesRealKeys(t *testing.T) {
	m := newTestModel()
	m.showHelp()
	card := m.msgs[len(m.msgs)-1].content
	if strings.Contains(card, "retry a lost connection") {
		t.Errorf("help still advertises the phantom r key")
	}
	for _, want := range []string{"^S", "alt+f", "^K"} {
		if !strings.Contains(card, want) {
			t.Errorf("help card omits %s", want)
		}
	}
}

// F7/P2: turn jumps name their landing spot — the copy target (alt+y) is
// verifiable on screen.
func TestJumpTurnReportsLanding(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.vp.Width, m.vp.Height = 80, 10
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q1"},
		message{role: roleAsst, content: strings.Repeat("answer ", 200)},
		message{role: roleUser, content: "q2"},
		message{role: roleAsst, content: strings.Repeat("reply ", 200)},
	)
	m.conversation()
	m.vp.GotoBottom()
	before := len(m.notices)
	m.jumpTurn(false)
	if len(m.notices) != before+1 {
		t.Errorf("jump gave no landing feedback: notices %d -> %d", before, len(m.notices))
	}
	if n := m.notices[len(m.notices)-1]; !strings.Contains(n, "turn") {
		t.Errorf("landing note %q does not name the turn", n)
	}
}
