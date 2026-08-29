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

func TestFindKeyRouting(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	m.Update(key("alt+f"))

	// ctrl+c quits from the find bar like from anywhere else.
	m.Update(key("ctrl+c"))
	if !m.quitting {
		t.Error("ctrl+c in the find bar did not quit")
	}

	// Backspace pops the query and rescans; past the last rune it is a
	// no-op that keeps the bar open.
	m = newTestModel()
	seedConversation(m)
	m.Update(key("alt+f"))
	m.Update(key("a"))
	m.Update(key("b"))
	if string(m.find.query) != "ab" {
		t.Fatalf("query = %q, want %q", string(m.find.query), "ab")
	}
	m.Update(key("backspace"))
	if string(m.find.query) != "a" {
		t.Errorf("backspace did not pop the query: %q", string(m.find.query))
	}
	if !m.find.open {
		t.Error("backspace closed the find bar")
	}
	m.Update(key("backspace"))
	m.Update(key("backspace")) // empty-query backspace
	if len(m.find.query) != 0 || len(m.find.matches) != 0 {
		t.Errorf("stale find state after emptying the query: %q %v", string(m.find.query), m.find.matches)
	}
	if !m.find.open {
		t.Error("find bar did not survive an empty-query backspace")
	}
}

func TestFindCtrlLBusyGuard(t *testing.T) {
	m := newTestModel()
	seedConversation(m)
	m.busy = true
	m.Update(key("alt+f"))
	m.Update(key("ctrl+l"))
	if m.confirm != confirmNone {
		t.Error("ctrl+l armed the clear confirm mid-turn")
	}
	if !m.find.open {
		t.Error("busy ctrl+l closed the find bar")
	}
}

func TestCloseFindIdempotent(t *testing.T) {
	m := newTestModel()
	m.closeFind() // already closed: guard branch, must not panic
	if m.find.open {
		t.Error("closeFind opened the bar")
	}
}

func TestFindMsgMatchItemKinds(t *testing.T) {
	tests := []struct {
		name string
		msg  message
		want bool
	}{
		{"raw cards never match", message{raw: true, content: "needle"}, false},
		{"content match", message{content: "the NEEDLE here"}, true},
		{"thinking item match", message{items: []turnItem{{thinking: true, text: "hidden needle"}}}, true},
		{"reply item match", message{items: []turnItem{{reply: true, text: "spoken needle"}}}, true},
		{"non-prose item text ignored", message{items: []turnItem{{text: "needle"}}}, false},
		{"step name match", message{steps: []step{{name: "needle_tool"}}}, true},
		{"step arg match", message{steps: []step{{name: "shell", arg: "grep needle file"}}}, true},
		{"step result match", message{steps: []step{{name: "shell", result: "found the needle"}}}, true},
		{"step log match", message{steps: []step{{name: "shell", logs: []string{"spinning needle"}}}}, true},
		{"no match anywhere", message{content: "haystack", steps: []step{{name: "shell", result: "nothing", logs: []string{"quiet"}}}}, false},
	}
	for _, tt := range tests {
		if got := findMsgMatch(tt.msg, "needle"); got != tt.want {
			t.Errorf("%s: findMsgMatch = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestMsgLineUnknownIndex(t *testing.T) {
	m := newTestModel()
	if got := m.msgLine(42); got != 0 {
		t.Errorf("msgLine(unknown) = %d, want 0", got)
	}

	// A stale match (message gone from the line index) must not panic:
	// the jump parks at the top.
	seedConversation(m)
	m.refresh()
	m.find = findState{open: true, query: []rune("x"), matches: []int{999}}
	m.findGoto(1)
	if m.vp.YOffset != 0 {
		t.Errorf("stale jump moved the viewport to %d, want 0", m.vp.YOffset)
	}
}

func TestFindBarRenderStates(t *testing.T) {
	m := newTestModel()

	// Empty query: hint text.
	m.find = findState{open: true}
	if got := plain(m.findBar()); !strings.Contains(got, "type to search") {
		t.Errorf("empty-query bar: %q", got)
	}

	// Query with matches: count line.
	m.find.query = []rune("needle")
	m.find.matches = []int{0, 2}
	if got := plain(m.findBar()); !strings.Contains(got, "2 matches") {
		t.Errorf("counted bar: %q", got)
	}

	// Over-long query on a wide screen: truncated, never rendered whole.
	m.width = 100
	long := strings.Repeat("q", 80)
	m.find.query = []rune(long)
	m.find.matches = nil
	if got := plain(m.findBar()); strings.Contains(got, long) {
		t.Error("over-long query rendered untruncated")
	}
}
