package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// ── C2: the first-run home signposts the core interactions ────────────────

// TestWelcomeTipSignpostsCoreInteractions: the welcome tip must name the
// core composer interactions and command discovery.
func TestWelcomeTipSignpostsCoreInteractions(t *testing.T) {
	out := plain(welcome(newTheme(), 120, "/somewhere", ""))
	for _, want := range []string{"⏎ send", "⇧⏎ newline", "^K commands"} {
		if !strings.Contains(out, want) {
			t.Errorf("welcome tip missing %q:\n%s", want, out)
		}
	}
	// C11: the ^K microcopy reads as a verb, not jargon.
	if !strings.Contains(out, "^K commands") {
		t.Errorf("welcome tip should identify the command palette:\n%s", out)
	}
	if strings.Contains(out, "^K everything") {
		t.Errorf("welcome tip still carries the cryptic '^K everything':\n%s", out)
	}
}

// ── C4: one failure glyph ──────────────────────────────────────────────────

// TestFailureGlyphStandardized: ✗ is the single failure glyph; the lamp
// set must not carry the stray ✕.
func TestFailureGlyphStandardized(t *testing.T) {
	if lampError != "✗" {
		t.Errorf("lampError = %q, want ✗ (standard failure glyph)", lampError)
	}
}

// ── C5: lamp glyphs belong to the connection state alone ──────────────────

// TestLampGlyphsReserved: no other surface may reuse ● ◉ ◌ ○ — those four
// cells are the connection lamp's vocabulary.
func TestLampGlyphsReserved(t *testing.T) {
	reserved := map[string]bool{lampReady: true, lampLive: true, lampReconnect: true, lampDown: true}
	if g := toolGlyph("web_fetch"); reserved[g] {
		t.Errorf("toolGlyph(web_fetch) = %q collides with the connection lamp", g)
	}
	if g := jobStatusGlyph("running"); reserved[g] {
		t.Errorf("jobStatusGlyph(running) = %q collides with the connection lamp", g)
	}
	card := &agentCard{phase: "queued"}
	if g := card.glyph(); reserved[g] {
		t.Errorf("queued agent glyph %q collides with the connection lamp", g)
	}
	if g := agentStatusGlyph("queued", ""); reserved[g] {
		t.Errorf("agentStatusGlyph(queued) = %q collides with the connection lamp", g)
	}
}

// ── C3: header instruments get a first-turn decoder ───────────────────────

// TestCtxHintOnFirstPrompt: the first real prompt teaches what the ctx
// gauge and the connection lamp mean.
func TestCtxHintOnFirstPrompt(t *testing.T) {
	m := newTestModel()
	m.sendPrompt("hello")
	found := false
	for _, n := range m.notices {
		if strings.Contains(plain(n), "ctx") && strings.Contains(plain(n), "context") {
			found = true
		}
	}
	if !found {
		t.Errorf("first prompt did not teach the ctx gauge: %v", m.notices)
	}
	// Exactly once: only notices added after the second send may match.
	before := len(m.notices)
	m.hintsShown[hintCtx] = true
	m.sendPrompt("again")
	for _, n := range m.notices[before:] {
		if strings.Contains(plain(n), "context window") {
			t.Errorf("ctx hint re-fired on a later prompt: %v", m.notices)
		}
	}
}

// ── C11: microcopy ────────────────────────────────────────────────────────

// TestSwarmHintMicrocopy: the swarm tip speaks in plain verbs.
func TestSwarmHintMicrocopy(t *testing.T) {
	if strings.Contains(hintSwarmText, "registry") || strings.Contains(hintSwarmText, "chips") {
		t.Errorf("swarm hint still uses jargon: %q", hintSwarmText)
	}
	if !strings.Contains(hintSwarmText, "/agents lists") {
		t.Errorf("swarm hint missing the plain-verb /agents phrasing: %q", hintSwarmText)
	}
}

// TestElapsedCarriesLabel: the live head clock is labeled, not a bare count.
func TestElapsedCarriesLabel(t *testing.T) {
	m := newTestModel()
	m.runStart = time.Now().Add(-12 * time.Second)
	if got := m.elapsed(); got != "running 12s" {
		t.Errorf("elapsed() = %q, want %q", got, "running 12s")
	}
	m.runStart = time.Now().Add(-65 * time.Second)
	if got := m.elapsed(); got != "running 1m05s" {
		t.Errorf("elapsed() = %q, want %q", got, "running 1m05s")
	}
}

// ── C1: the faint contract — body text never renders in faint ────────────

// TestStepArgNotFaint: step arguments are machine-voice secondary text and
// must take muted, not the chrome-only faint token.
func TestStepArgNotFaint(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	p := paletteByName("ember-dark")
	faint := lipgloss.NewStyle().Foreground(p.faint).Render("x")
	if got := themeFrom(p).stepArg.Render("x"); got == faint {
		t.Error("stepArg renders with the faint token; body text must use muted")
	}
}

// ── C9: the queued count lives in the footer alone ────────────────────────

// TestApprovalHeadDropsQueuedChip: with a queue behind the head approval,
// the card head no longer repeats the count the footer already carries.
func TestApprovalHeadDropsQueuedChip(t *testing.T) {
	m := newTestModel()
	m.approvals = []client.Event{{Type: "approval_request", ID: "a1", Risk: "low"}, {Type: "approval_request", ID: "a2", Risk: "low"}}
	body := plain(m.approvalBody())
	if strings.Contains(body, "queued") {
		t.Errorf("approval card head still carries the queued count:\n%s", body)
	}
	foot := plain(m.footer())
	if !strings.Contains(foot, "1 queued") {
		t.Errorf("footer lost the queued count:\n%s", foot)
	}
}
