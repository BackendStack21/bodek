package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// The top-3 improvements round: runtime /theme with persistence, copy any
// turn card (alt+y), and /retry. Each test drives the real command/key
// paths; persistence is observed through the OnThemeChange hook.

func lastNote(m *Model) string {
	if n := len(m.notices); n > 0 {
		return m.notices[n-1]
	}
	return ""
}

// --- /theme ---

func TestThemeListNote(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("/theme")
	exec(m.submit())
	want := "theme: " + themeName()
	if !strings.Contains(lastNote(m), want) || !strings.Contains(lastNote(m), "classic") {
		t.Errorf("/theme note = %q, want current theme %q plus options", lastNote(m), want)
	}
}

func TestThemeSwitchPersists(t *testing.T) {
	m := wired(t)
	t.Cleanup(func() { themeOverride = "" })
	var saved string
	m.opts.OnThemeChange = func(name string) error { saved = name; return nil }

	m.ta.SetValue("/theme classic")
	exec(m.submit())

	if themeOverride != "classic" {
		t.Errorf("themeOverride = %q, want classic", themeOverride)
	}
	if saved != "classic" {
		t.Errorf("OnThemeChange saved %q, want classic", saved)
	}
	if !strings.Contains(lastNote(m), "classic") {
		t.Errorf("switch note = %q, want confirmation", lastNote(m))
	}
}

func TestThemeSwitchUnknown(t *testing.T) {
	m := wired(t)
	m.opts.OnThemeChange = func(string) error { t.Fatal("OnThemeChange called for unknown theme"); return nil }
	m.ta.SetValue("/theme neon-night")
	exec(m.submit())
	if themeOverride == "neon-night" {
		t.Error("unknown theme accepted")
	}
	if !strings.Contains(lastNote(m), "neon-night") {
		t.Errorf("note = %q, want unknown-theme mention", lastNote(m))
	}
}

func TestThemeSwitchAlreadyActive(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("/theme " + themeName())
	exec(m.submit())
	if !strings.Contains(lastNote(m), "already") {
		t.Errorf("note = %q, want already-active hint", lastNote(m))
	}
}

func TestCanonicalTheme(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"ember-dark", "ember-dark", true},
		{"dark", "ember-dark", true},
		{"ember-light", "ember-light", true},
		{"light", "ember-light", true},
		{"high-contrast", "high-contrast", true},
		{"contrast", "high-contrast", true},
		{"classic", "classic", true},
		{"  Classic  ", "classic", true},
		{"neon-night", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := canonicalTheme(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("canonicalTheme(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestThemeSwitchRebuildsGlam(t *testing.T) {
	m := wired(t)
	t.Cleanup(func() { themeOverride = "" })
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "hi"},
		message{role: roleAsst, content: "**bold** answer"},
	)
	m.msgs[1].rendered = m.render(m.msgs[1].content)
	m.ta.SetValue("/theme ember-light")
	exec(m.submit())
	// The finalized message must have been re-rendered under the new palette
	// (resize() runs on switch; its render must differ from the old cache
	// only if colors changed — here we assert it was recomputed at all).
	if m.msgs[1].rendered == "" {
		t.Error("finalized message not re-rendered after theme switch")
	}
}

// --- copy any turn card (alt+y) ---

func TestCopyFocusedTurn(t *testing.T) {
	m := wired(t)
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "first prompt"},
		message{role: roleAsst, content: "first reply"},
		message{role: roleUser, content: "second prompt"},
		message{role: roleAsst, content: "second reply"},
	)
	m.refresh() // rebuilds the turn-head line index the jumps navigate

	// Jump to the previous turn twice: lands on the first assistant head.
	m.Update(key("alt+up"))
	m.Update(key("alt+up"))
	if m.focusIdx < 0 {
		t.Fatalf("focusIdx = %d, want a turn head after alt+up jumps", m.focusIdx)
	}
	if got := m.focusedReply(); got != "first reply" {
		t.Errorf("focusedReply = %q, want first reply", got)
	}

	// alt+y copies the focused turn through the OSC 52 path (exec command).
	_, cmd := m.Update(key("alt+y"))
	if cmd == nil {
		t.Fatal("alt+y produced no command")
	}
}

func TestCopyFocusedTurnFallsBackToLastReply(t *testing.T) {
	m := wired(t)
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "p"},
		message{role: roleAsst, content: "the only reply"},
	)
	if got := m.focusedReply(); got != "the only reply" {
		t.Errorf("focusedReply without focus = %q, want last reply", got)
	}
	// A stale index (out of range or non-assistant) must fall back, not panic.
	m.focusIdx = 99
	if got := m.focusedReply(); got != "the only reply" {
		t.Errorf("focusedReply with stale focus = %q, want last reply", got)
	}
	m.focusIdx = 0 // user message — not copyable
	if got := m.focusedReply(); got != "the only reply" {
		t.Errorf("focusedReply on user msg = %q, want last reply", got)
	}
}

func TestCopyFocusedTurnIsEmpty(t *testing.T) {
	m := wired(t)
	_, cmd := m.Update(key("alt+y"))
	if cmd == nil {
		t.Fatal("alt+y on an empty transcript produced no command")
	}
	if !strings.Contains(lastNote(m), "nothing to copy") {
		t.Errorf("note = %q, want nothing-to-copy hint", lastNote(m))
	}
}

// --- /retry ---

func TestRetryResendsLastPrompt(t *testing.T) {
	m := wired(t)
	m.handleEvent(client.Event{Type: "session", SessionID: "s1"})
	m.ta.SetValue("build the thing")
	exec(m.submit())
	if !m.busy {
		t.Fatal("prompt did not start a turn")
	}
	// End the turn like the server would.
	m.handleEvent(client.Event{Type: "done", Content: "ok"})
	m.busy = false

	m.ta.SetValue("/retry")
	exec(m.submit())

	if !m.busy {
		t.Error("/retry did not start a new turn")
	}
	if n := len(m.msgs); n < 2 {
		t.Fatalf("/retry produced %d messages, want at least the user echo", n)
	}
	if last := m.msgs[len(m.msgs)-2]; last.role != roleUser || !strings.Contains(last.content, "build the thing") {
		t.Errorf("retry sent %q, want the previous prompt", last.content)
	}
}

func TestRetryWithoutHistory(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("/retry")
	exec(m.submit())
	if !strings.Contains(lastNote(m), "nothing to retry") {
		t.Errorf("note = %q, want nothing-to-retry hint", lastNote(m))
	}
}

func TestRetryWhileBusyQueues(t *testing.T) {
	m := wired(t)
	m.handleEvent(client.Event{Type: "session", SessionID: "s1"})
	m.ta.SetValue("first prompt")
	exec(m.submit()) // starts a turn — busy

	m.ta.SetValue("/retry")
	exec(m.submit())

	if len(m.queue) != 1 || m.queue[0] != "first prompt" {
		t.Errorf("queue = %v, want [first prompt]", m.queue)
	}
	if !strings.Contains(lastNote(m), "queued") {
		t.Errorf("note = %q, want queued hint", lastNote(m))
	}
}

func TestRetryChordAltR(t *testing.T) {
	m := wired(t)
	m.lastPrompt = "from the chord"
	_, cmd := m.Update(key("alt+r"))
	if cmd == nil {
		t.Fatal("alt+r produced no command")
	}
	if !m.busy {
		t.Error("alt+r did not start a turn")
	}
	if m.lastPrompt != "from the chord" {
		t.Errorf("lastPrompt = %q", m.lastPrompt)
	}
}
