package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// plainDecisionKey builds a bare-letter KeyMsg like a terminal sends for
// Option-as-UTF-8 on macOS (Bubble Tea reports it as plain KeyRunes).
func plainDecisionKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// TestApprovalPlainKeysDecideEmptyComposer verifies the plain-letter
// shortcuts a approve / d deny / t trust fire only while the composer
// draft is empty.
func TestApprovalPlainKeysDecideEmptyComposer(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	cases := []struct {
		name       string
		allowTrust bool
		key        rune
		want       string
	}{
		{"plain a approves", false, 'a', "approve"},
		{"plain d denies", false, 'd', "deny"},
		{"plain t trusts when offered", true, 't', "trust"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			busyTurn(m)
			m.handleEvent(client.Event{Type: "approval_request", ID: "apr", AllowTrust: tc.allowTrust})
			if m.ta.Value() != "" {
				t.Fatal("precondition: composer draft must be empty")
			}
			_, cmd := m.Update(plainDecisionKey(tc.key))
			exec(cmd)
			if m.curApproval() != nil {
				t.Fatal("plain decision letter left the approval pending")
			}
			if got := awaitAction(t, actions); got != tc.want {
				t.Errorf("action = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestApprovalPlainKeysNeedEmptyComposer verifies a non-empty draft keeps
// routing a/d/t to the composer, and that a paste never decides.
func TestApprovalPlainKeysNeedEmptyComposer(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr"})
	m.Update(plainDecisionKey('x'))
	if m.ta.Value() == "" {
		t.Fatal("precondition: draft should hold text")
	}
	for _, r := range []rune{'a', 'd', 't'} {
		m.Update(plainDecisionKey(r))
	}
	if m.curApproval() == nil {
		t.Fatal("plain letter must not decide while the draft is non-empty")
	}
	if got := m.ta.Value(); got != "xadt" {
		t.Errorf("composer draft = %q, want letters appended", got)
	}
	// Paste is never a decision even on an empty composer.
	m.ta.SetValue("")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d"), Paste: true})
	if m.curApproval() == nil {
		t.Fatal("pasted text must not decide an approval")
	}
	if got := m.ta.Value(); got != "d" {
		t.Errorf("paste should land in the composer, got %q", got)
	}
}

// TestApprovalPlainKeysInertDuringFriction verifies the friction gate still
// demands the literal word — a plain 'a' opens the editor but never approves,
// and pasted letters land in the composer instead.
func TestApprovalPlainKeysInertDuringFriction(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", Friction: true, FrictionApprovals: 3})
	m.Update(plainDecisionKey('a'))
	if m.curApproval() == nil {
		t.Fatal("plain a must not approve under friction")
	}
	if !m.apprEditing {
		t.Fatal("plain a should open the friction editor (macOS has no Alt chords)")
	}
	// Paste must never activate or decide anything.
	m.apprEditing = false
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a"), Paste: true})
	if m.apprEditing || m.curApproval() == nil {
		t.Fatal("pasted 'a' must not open the friction editor")
	}
}

// TestFilterShiftEnterDecodesCSIAltChords verifies the legacy Alt encodings
// from modifyOtherKeys=2 and kitty CSI-u terminals decode to alt+rune KeyMsgs.
func TestFilterShiftEnterDecodesCSIAltChords(t *testing.T) {
	cases := []struct {
		seq  string
		want string
	}{
		{"\x1b[27;3;97~", "alt+a"},
		{"\x1b[27;3;100~", "alt+d"},
		{"\x1b[27;3;116~", "alt+t"},
		{"\x1b[97;3u", "alt+a"},
		{"\x1b[100;3u", "alt+d"},
		{"\x1b[116;3u", "alt+t"},
	}
	for _, tc := range cases {
		msg := FilterShiftEnter(nil, []byte(tc.seq))
		km, ok := msg.(tea.KeyMsg)
		if !ok {
			t.Errorf("FilterShiftEnter(%q) = %#v, want KeyMsg", tc.seq, msg)
			continue
		}
		if got := km.String(); got != tc.want {
			t.Errorf("FilterShiftEnter(%q) = %q, want %q", tc.seq, got, tc.want)
		}
	}
}

// TestApprovalCSIDecodedAltDecidesEndToEnd pipes a decoded CSI chord through
// Update to prove the legacy path still decides.
func TestApprovalCSIDecodedAltDecidesEndToEnd(t *testing.T) {
	m, actions, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr"})
	msg := FilterShiftEnter(nil, []byte("\x1b[27;3;100~"))
	_, cmd := m.Update(msg.(tea.KeyMsg))
	exec(cmd)
	if m.curApproval() != nil {
		t.Fatal("CSI-encoded alt+d left the approval pending")
	}
	if got := awaitAction(t, actions); got != "deny" {
		t.Errorf("action = %q, want deny", got)
	}
}

// TestApprovalFooterShowsPlainHints verifies the card footer advertises the
// plain-letter shortcuts.
func TestApprovalFooterShowsPlainHints(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr", AllowTrust: true})
	foot := plain(m.footer())
	for _, want := range []string{"a approve", "d deny", "t trust"} {
		if !strings.Contains(foot, want) {
			t.Errorf("approval footer missing %q: %q", want, foot)
		}
	}
	if strings.Contains(foot, "\n") {
		t.Error("approval footer must fit one row")
	}
}
