package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// home_dashboard_test.go — F4: the session home earns its screen. Recent
// sessions give one-glance orientation, wire-borne text never renders raw,
// and the card stays a bounded dashboard, not a scrolling page.

func TestHomeShowsRecentSessions(t *testing.T) {
	m := newTestModel()
	m.homePrompt = "previous task"
	m.homeSess = []client.Session{
		{ID: "s1", Task: "fix the login bug", Turns: 4, UpdatedAt: time.Now().Add(-4 * time.Minute)},
		{ID: "s2", Task: "refactor the parser", Turns: 9, UpdatedAt: time.Now().Add(-2 * time.Hour)},
	}
	out := plain(m.sessionHome())
	for _, want := range []string{"fix the login bug", "refactor the parser", "4 turns", "^K"} {
		if !strings.Contains(out, want) {
			t.Errorf("session home missing %q:\n%s", want, out)
		}
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines > 9 {
		t.Errorf("session home must stay a bounded dashboard (≤9 rows), got %d:\n%s", lines, out)
	}
}

func TestHomeSessionsCappedAtThree(t *testing.T) {
	m := newTestModel()
	m.homePrompt = "t"
	for i := 0; i < 6; i++ {
		m.homeSess = append(m.homeSess, client.Session{ID: "s", Task: "task", Turns: 1, UpdatedAt: time.Now()})
	}
	out := plain(m.sessionHome())
	if n := strings.Count(out, "↩"); n != 3 {
		t.Errorf("home must show at most 3 recent sessions, got %d:\n%s", n, out)
	}
}

func TestHomeSessionsAreSanitized(t *testing.T) {
	m := newTestModel()
	m.homePrompt = "t"
	m.homeSess = []client.Session{{ID: "s", Task: "evil\x1b]0;pwned", Turns: 1, UpdatedAt: time.Now()}}
	out := m.sessionHome() // raw render — escape sequences must not survive
	if strings.Contains(out, "\x1b]") {
		t.Fatalf("wire-borne escape sequence leaked into the home card: %q", out)
	}
}

func TestClearKicksHomeSessionsFetch(t *testing.T) {
	m := newTestModel()
	cmd := m.fetchHomeSessions()
	if cmd == nil {
		t.Fatal("fetchHomeSessions returned no cmd")
	}
	if again := m.fetchHomeSessions(); again != nil {
		t.Fatal("fetch must be once-per-clear, not re-armed on every call")
	}
	m.Update(exec(cmd)) // empty/failed fetch still settles the once-guard
	if !m.homeSessDone {
		t.Fatal("home sessions fetch never settled")
	}
	if m.homeSess != nil {
		t.Fatalf("nil client must not fabricate sessions: %+v", m.homeSess)
	}
}

func TestClearConversationReturnsFetchCmd(t *testing.T) {
	m := newTestModel()
	m.homePrompt = "old work"
	cmd := m.clearConversation()
	if cmd == nil {
		t.Fatal("/clear must arm the home-sessions fetch for the dashboard")
	}
	m.Update(exec(cmd))
}

func TestHomeSessionsGenerationGuard(t *testing.T) {
	m := newTestModel()
	first := m.clearConversation() // gen 0 fetch armed
	m.clearConversation()          // gen 1 fetch armed; gen 0 reply is now stale
	m.Update(exec(first))          // stale reply lands
	if m.homeSess != nil {
		t.Fatalf("a superseded clear's fetch must be dropped: %+v", m.homeSess)
	}
}

func TestSwarmHintSkipsTerminalFrames(t *testing.T) {
	m := manifestFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "ok"})
	if countNotices(m, "tip: tab cycles sub-agent") != 0 {
		t.Fatalf("terminal frames must not teach the swarm hint: %q", m.notices)
	}
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t2", TaskIdx: 1, Phase: "active", Status: "running", Tool: "go test"})
	if countNotices(m, "tip: tab cycles sub-agent") != 1 {
		t.Fatalf("the first live swarm frame must teach the hint once: %q", m.notices)
	}
}
