package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// ^C is the one key that used to kill the app from anywhere — a stray
// terminal-focused chord could end a session mid-run. It now rides the same
// two-step confirm gate as every other destructive action: the first ^C arms
// the gate, y (or a second ^C) fires, any other key disarms.

func TestCtrlCArmsQuitConfirm(t *testing.T) {
	m := newTestModel()

	m.Update(key("ctrl+c"))

	if m.confirm != confirmQuit {
		t.Fatalf("ctrl+c did not arm confirmQuit: %v", m.confirm)
	}
	if m.quitting {
		t.Fatal("first ctrl+c quit outright — the gate never armed")
	}
	if got := plain(m.View()); !strings.Contains(got, "quit bodek?") {
		t.Errorf("footer does not show the quit confirm gate:\n%s", got)
	}
}

func TestQuitGateYFires(t *testing.T) {
	m := newTestModel()

	m.Update(key("ctrl+c"))
	_, cmd := m.Update(key("y"))

	if !m.quitting {
		t.Fatal("y on an armed quit gate did not quit")
	}
	if cmd == nil {
		t.Fatal("y on an armed quit gate returned no command")
	}
}

func TestQuitGateSecondCtrlCConfirms(t *testing.T) {
	// Double-^C is the muscle-memory confirm: the second press fires.
	m := newTestModel()

	m.Update(key("ctrl+c"))
	_, cmd := m.Update(key("ctrl+c"))

	if !m.quitting {
		t.Fatal("a second ctrl+c did not confirm the quit gate")
	}
	if cmd == nil {
		t.Fatal("second ctrl+c returned no command")
	}
}

func TestQuitGateOtherKeyDisarms(t *testing.T) {
	m := newTestModel()

	m.Update(key("ctrl+c"))
	m.Update(key("n"))

	if m.confirm != confirmNone {
		t.Errorf("any other key must disarm the gate, got %v", m.confirm)
	}
	if m.quitting {
		t.Error("disarming the gate quit anyway")
	}
	// The gate eats exactly one decision key: the disarming printable rune
	// falls through to the composer, so "esc/^C, keep typing" never loses a
	// character (judge-3 F2).
	if got := m.ta.Value(); got != "n" {
		t.Errorf("disarm keypress did not reach the input: %q", got)
	}
	// And quit stays two-step afterwards: re-arm, not quit.
	m.Update(key("ctrl+c"))
	if m.confirm != confirmQuit || m.quitting {
		t.Fatalf("post-disarm ctrl+c: confirm=%v quitting=%v", m.confirm, m.quitting)
	}
}

func TestQuitGateArmsFromEveryContext(t *testing.T) {
	// ^C is reachable from every rung of the modality ladder — the gate must
	// arm from each, never quit on the first press.
	cases := map[string]func(m *Model){
		"composer":    func(m *Model) {},
		"palette":     func(m *Model) { m.Update(key("ctrl+k")) },
		"find bar":    func(m *Model) { m.Update(key("alt+f")) },
		"ac popup":    func(m *Model) { m.ta.SetValue("/"); m.syncAC() },
		"panel":       func(m *Model) { m.panel = panelSessions },
		"popover":     func(m *Model) { m.popover = true },
		"queue focus": func(m *Model) { m.queue = []string{"q"}; m.Update(key("ctrl+q")) },
		"approval":    func(m *Model) { m.handleEvent(client.Event{Type: "approval_request", ID: "apr-q"}) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestModel()
			setup(m)

			m.Update(key("ctrl+c"))

			if m.confirm != confirmQuit {
				t.Fatalf("ctrl+c from %s did not arm confirmQuit: %v", name, m.confirm)
			}
			if m.quitting {
				t.Fatalf("ctrl+c from %s quit outright", name)
			}
			if got := plain(m.View()); !strings.Contains(got, "quit bodek?") {
				t.Fatalf("gate not rendered from %s:\n%s", name, got)
			}
			// y fires from anywhere too.
			if _, cmd := m.Update(key("y")); !m.quitting || cmd == nil {
				t.Fatalf("y from %s did not quit (quitting=%v cmd=%v)", name, m.quitting, cmd)
			}
		})
	}
}
