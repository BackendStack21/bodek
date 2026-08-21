package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// The e2e command bar: every registered slash command is executed through
// the real user path — one key event per rune into the composer (which
// drives the completion popup as a side effect), then enter, then the
// produced command tree runs against the in-process odek stand-in and every
// message is fed back into the model. A command that only works when its
// handler is invoked directly (the unit tests) fails here.

// drive runs a cmd tree the way the tea runtime would, flattening batches
// and feeding every produced message back into the model. Timer cmds that
// sleep wall-clock (note expiry, the runs poll re-arm) are abandoned —
// their state changes already happened inline, and chasing them would
// stall the test for their full delay.
func drive(m *Model, cmd tea.Cmd) {
	for step := 0; cmd != nil && step < 64; step++ {
		msg := callCmd(cmd)
		if msg == nil || timeReArm(msg) {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				drive(m, c)
			}
			return
		}
		_, cmd = m.Update(msg)
	}
}

// callCmd runs a cmd with a grace window. In-process stand-in fetches
// return in ~1ms; anything slower is a tea.Tick-style timer (it sleeps for
// its delay before returning its msg) and is abandoned.
func callCmd(cmd tea.Cmd) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(150 * time.Millisecond):
		return nil
	}
}

func timeReArm(msg tea.Msg) bool {
	switch msg.(type) {
	case spinner.TickMsg, heartbeatMsg, runsTickMsg, noticeExpireMsg, renderFlushMsg:
		return true
	}
	return false
}

// typeLine enters text exactly as a user does — rune by rune, then enter —
// returning the cmd the submission produced.
func typeLine(m *Model, text string) tea.Cmd {
	var cmd tea.Cmd
	for _, r := range text {
		_, cmd = m.Update(key(string(r)))
	}
	_, cmd = m.Update(key("enter"))
	return cmd
}

// pumpWS drains events the stand-in echoed back over the websocket, the
// way the production read loop would deliver them — /cancel settles the
// turn through the echoed events, not through its own cmd. Each event gets
// a short wait window; the echo is in-process and returns in well under a
// millisecond.
func pumpWS(m *Model) {
	for i := 0; i < 8; i++ {
		select {
		case ev := <-m.events:
			_, cmd := m.handleEvent(ev)
			drive(m, cmd)
		case <-time.After(30 * time.Millisecond):
			return
		}
	}
}

func lastMsg(m *Model) *message {
	if len(m.msgs) == 0 {
		return nil
	}
	return &m.msgs[len(m.msgs)-1]
}

func notePresent(m *Model, sub string) bool {
	for _, n := range m.notices {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// TestE2EAllCommands drives every command in the registry through the real
// typing path and asserts its observable outcome — panel opened, data
// fetched, state changed, note surfaced.
func TestE2EAllCommands(t *testing.T) {
	cases := map[string]func(t *testing.T, m *Model){
		"/help": func(t *testing.T, m *Model) {
			card := lastMsg(m)
			if card == nil || !card.raw || !strings.Contains(plain(card.content), "commands") {
				t.Fatalf("/help card = %+v", card)
			}
		},
		"/clear": func(t *testing.T, m *Model) {
			if len(m.msgs) != 0 {
				t.Fatalf("/clear left %d messages", len(m.msgs))
			}
		},
		"/stats": func(t *testing.T, m *Model) {
			card := lastMsg(m)
			if card == nil || !card.raw || !strings.Contains(plain(card.content), "⬡ session") {
				t.Fatalf("/stats card = %+v", card)
			}
		},
		"/server": func(t *testing.T, m *Model) {
			if !m.popover {
				t.Fatal("/server did not open the cockpit popover")
			}
			if out := plain(m.View()); !strings.Contains(out, "cockpit") {
				t.Errorf("cockpit card not rendered:\n%s", out)
			}
		},
		"/sessions": func(t *testing.T, m *Model) {
			if m.panel != panelSessions {
				t.Fatalf("panel = %d, want sessions", m.panel)
			}
			if len(m.sessions) != 1 {
				t.Errorf("/sessions fetched %d sessions, want 1", len(m.sessions))
			}
		},
		"/runs": func(t *testing.T, m *Model) {
			if m.panel != panelRuns {
				t.Fatalf("panel = %d, want runs", m.panel)
			}
			if len(m.runs) != 2 {
				t.Errorf("/runs fetched %d runs, want 2", len(m.runs))
			}
		},
		"/events": func(t *testing.T, m *Model) {
			if m.panel != panelEvents {
				t.Fatalf("panel = %d, want events", m.panel)
			}
			if len(m.feed) != 3 {
				t.Errorf("/events fetched %d events, want 3", len(m.feed))
			}
		},
		"/memory": func(t *testing.T, m *Model) {
			if m.panel != panelMemory {
				t.Fatalf("panel = %d, want memory", m.panel)
			}
			if len(m.memRows) != 3 {
				t.Errorf("/memory rows = %d, want 3 (2 facts + 1 episode)", len(m.memRows))
			}
		},
		"/skills": func(t *testing.T, m *Model) {
			if m.panel != panelSkills {
				t.Fatalf("panel = %d, want skills", m.panel)
			}
			if len(m.skills) != 2 {
				t.Errorf("/skills fetched %d skills, want 2", len(m.skills))
			}
		},
		"/tools": func(t *testing.T, m *Model) {
			if m.panel != panelTools {
				t.Fatalf("panel = %d, want tools", m.panel)
			}
			if len(m.toolRows) == 0 {
				t.Error("/tools fetched no rows")
			}
		},
		"/config": func(t *testing.T, m *Model) {
			if m.panel != panelConfig {
				t.Fatalf("panel = %d, want config", m.panel)
			}
			if len(m.cfgRows) == 0 {
				t.Error("/config fetched no rows")
			}
		},
		"/run fix the login bug": func(t *testing.T, m *Model) {
			if standInSaw.prompts != 1 {
				t.Fatalf("/run hit /api/prompt %d times, want 1", standInSaw.prompts)
			}
			if m.panel != panelRuns {
				t.Fatalf("panel = %d, want runs (a started run opens its tab)", m.panel)
			}
			if len(m.runs) != 2 {
				t.Errorf("/run refresh fetched %d runs, want 2", len(m.runs))
			}
		},
		"/model glm-5.3": func(t *testing.T, m *Model) {
			if m.model != "glm-5.3" || m.pendModel != "glm-5.3" {
				t.Fatalf("/model arg: model=%q pend=%q", m.model, m.pendModel)
			}
			if !notePresent(m, "model set to glm-5.3") {
				t.Errorf("/model note missing: %v", m.notices)
			}
		},
		"/model": func(t *testing.T, m *Model) {
			if m.panel != panelModels {
				t.Fatalf("panel = %d, want the model picker", m.panel)
			}
		},
		"/thinking on": func(t *testing.T, m *Model) {
			if !m.thinkOn {
				t.Fatal("/thinking on did not enable thinking")
			}
		},
		"/thinking off": func(t *testing.T, m *Model) {
			if m.thinkOn {
				t.Fatal("/thinking off did not disable thinking")
			}
		},
		"/cancel": func(t *testing.T, m *Model) {
			if m.busy {
				t.Fatal("/cancel did not clear the busy state")
			}
		},
		"/attach": func(t *testing.T, m *Model) {
			if len(m.attachments) != 1 || m.attachments[0].Name != "notes.txt" {
				t.Fatalf("/attach staged = %+v", m.attachments)
			}
			if !notePresent(m, "attached notes.txt") {
				t.Errorf("/attach note missing: %v", m.notices)
			}
		},
		"/unattach": func(t *testing.T, m *Model) {
			if len(m.attachments) != 0 {
				t.Fatalf("/unattach left %+v staged", m.attachments)
			}
		},
		"/quit": func(t *testing.T, m *Model) {
			if !m.quitting {
				t.Fatal("/quit did not set quitting")
			}
		},
	}

	// Command → the line a user types for it (some need pre-seeded state).
	lines := map[string]string{
		"/attach": "", // filled per-run with a temp file path
	}
	for name, check := range cases {
		t.Run(name, func(t *testing.T) {
			m := wired(t)
			switch name {
			case "/clear":
				m.msgs = append(m.msgs, message{role: roleUser, content: "x"})
			case "/cancel":
				m.busy = true
				m.sessionID, m.authToken = "s1", "a1"
			case "/attach":
				dir := t.TempDir()
				path := filepath.Join(dir, "notes.txt")
				if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
					t.Fatal(err)
				}
				lines[name] = "/attach " + path
			case "/unattach":
				m.attachments = append(m.attachments, client.Attachment{Name: "notes.txt", Content: "hello"})
			}
			line := lines[name]
			if line == "" {
				line = name
			}
			drive(m, typeLine(m, line))
			pumpWS(m)
			if m.ta.Value() != "" {
				t.Errorf("input not reset after %s: %q", name, m.ta.Value())
			}
			check(t, m)
		})
	}

	// The registry sweep: adding a command without an e2e case fails here.
	have := map[string]bool{}
	for name := range cases {
		have[strings.Fields(name)[0]] = true
	}
	for _, c := range slashCommands() {
		if !have["/"+c.name] {
			t.Errorf("command /%s has no e2e case — add one to TestE2EAllCommands", c.name)
		}
	}
}

// TestE2EUnknownCommandAndPopupEnter covers the two dispatch edges: an
// unregistered command surfaces a notice instead of prompting the agent,
// and enter with the popup open executes the HIGHLIGHTED command (the
// first registry match of the typed prefix).
func TestE2EUnknownCommandAndPopupEnter(t *testing.T) {
	m := wired(t)
	drive(m, typeLine(m, "/nope"))
	if !notePresent(m, "unknown command") {
		t.Errorf("unknown command produced no notice: %v", m.notices)
	}
	if m.busy {
		t.Error("an unknown command must not start an agent turn")
	}

	// "/s" matches stats, sessions, server (registry order) — enter runs
	// the highlighted first match: /stats.
	m2 := wired(t)
	drive(m2, typeLine(m2, "/s"))
	card := lastMsg(m2)
	if card == nil || !strings.Contains(plain(card.content), "⬡ session") {
		t.Errorf("popup enter should run the highlighted /stats, got %+v", card)
	}
}
