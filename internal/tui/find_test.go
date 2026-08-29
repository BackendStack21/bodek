package tui

import (
	"strings"
	"testing"
)

// alt+f opens the transcript find bar: typing filters matches live, ⏎ jumps
// to the next match, N to the previous, esc closes. While the bar is open it
// captures printable keys — the composer must not type.

func TestFindOpenClose(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	h0 := m.inputAreaHeight()

	m.Update(key("alt+f"))
	if !m.find.open {
		t.Fatal("alt+f did not open the find bar")
	}
	if got := m.inputAreaHeight(); got != h0+1 {
		t.Errorf("open find bar did not claim a row: %d -> %d", h0, got)
	}
	if got := plain(m.View()); !strings.Contains(got, "find") {
		t.Error("find bar not rendered")
	}

	m.Update(key("esc"))
	if m.find.open {
		t.Error("esc did not close the find bar")
	}
	if got := m.inputAreaHeight(); got != h0 {
		t.Errorf("closed find bar still claims a row: %d", got)
	}
}

func TestFindMatchesAndJump(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "needle one"},
		message{role: roleAsst, content: "about needles"},
		message{role: roleAsst, content: "unrelated"},
		message{role: roleAsst, content: "NEEDLE three"},
	)
	m.refresh()

	m.Update(key("alt+f"))
	for _, r := range "needle" {
		m.Update(key(string(r)))
	}
	if len(m.find.matches) != 3 {
		t.Fatalf("matches = %v, want 3 messages", m.find.matches)
	}
	want := []int{0, 1, 3}
	for i, w := range want {
		if m.find.matches[i] != w {
			t.Fatalf("matches = %v, want %v", m.find.matches, want)
		}
	}
}

func TestFindEnterJumpsSequentially(t *testing.T) {
	m := newTestModel()
	filler := strings.Repeat("line\n", 45) // overflow the 30-row viewport
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "alpha " + filler},
		message{role: roleAsst, content: "target here " + filler},
		message{role: roleAsst, content: "filler " + filler},
		message{role: roleAsst, content: "second target"},
	)
	m.refresh()
	m.Update(key("alt+f"))
	for _, r := range "target" {
		m.Update(key(string(r)))
	}

	m.Update(key("enter"))
	if m.vp.AtBottom() {
		t.Error("enter did not scroll to the first match")
	}
	first := m.vp.YOffset
	m.Update(key("enter"))
	second := m.vp.YOffset
	if second <= first {
		t.Errorf("second enter did not advance: %d then %d", first, second)
	}
	m.Update(key("N"))
	if m.vp.YOffset != first {
		t.Errorf("N did not go back: %d want %d", m.vp.YOffset, first)
	}
}

func TestFindCapturesKeys(t *testing.T) {
	m := newTestModel()
	seedConversation(m)

	m.Update(key("alt+f"))
	m.Update(key("x"))
	if m.ta.Value() != "" {
		t.Errorf("find leaked %q into the composer", m.ta.Value())
	}
	if m.find.query == nil || len(m.find.query) != 1 {
		t.Errorf("rune did not reach the find query: %q", string(m.find.query))
	}
}

func TestFindNoMatches(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	m.Update(key("alt+f"))
	m.Update(key("z"))
	m.Update(key("z"))
	m.Update(key("z"))
	if len(m.find.matches) != 0 {
		t.Fatalf("unexpected matches: %v", m.find.matches)
	}
	if got := plain(m.View()); !strings.Contains(got, "no matches") {
		t.Errorf("no-match state not rendered:\n%s", got)
	}
	m.Update(key("enter")) // must not panic or move
}

func TestClearClosesFind(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	m.Update(key("alt+f"))
	if !m.find.open {
		t.Fatal("find bar did not open")
	}
	m.Update(key("ctrl+l")) // arms the confirm
	m.Update(key("y"))      // fires the clear
	if m.find.open {
		t.Error("clearing the conversation left the find bar open")
	}
	if len(m.find.matches) != 0 || len(m.find.query) != 0 {
		t.Errorf("find state not reset: %q %v", string(m.find.query), m.find.matches)
	}
}
