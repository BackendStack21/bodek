package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCommandPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/he", "he", true},
		{"/", "", true},
		{"/model gpt", "", false}, // has a space → no longer completing the name
		{"hello", "", false},
		{"a/b", "", false},
	}
	for _, c := range cases {
		got, ok := commandPrefix(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("commandPrefix(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSlashCommandsViaSubmit(t *testing.T) {
	m := wired(t)

	// /clear
	m.msgs = append(m.msgs, message{role: roleUser, content: "x"})
	m.ta.SetValue("/clear")
	exec(m.submit())
	if len(m.msgs) != 0 {
		t.Errorf("/clear left %d messages", len(m.msgs))
	}

	// /help appends a pre-styled help card; input is reset.
	m.ta.SetValue("/help")
	exec(m.submit())
	if len(m.msgs) == 0 {
		t.Fatal("/help did not append a help card")
	}
	help := m.msgs[len(m.msgs)-1]
	if !help.raw || !strings.Contains(plain(help.content), "commands") ||
		!strings.Contains(plain(help.content), "/sessions") {
		t.Errorf("/help card malformed (raw=%v):\n%s", help.raw, plain(help.content))
	}
	if m.ta.Value() != "" {
		t.Errorf("input not reset after command: %q", m.ta.Value())
	}

	// /thinking on / off / toggle
	m.ta.SetValue("/thinking on")
	exec(m.submit())
	if !m.thinkOn {
		t.Error("/thinking on failed")
	}
	m.ta.SetValue("/thinking off")
	exec(m.submit())
	if m.thinkOn {
		t.Error("/thinking off failed")
	}
	m.ta.SetValue("/thinking")
	exec(m.submit())
	if !m.thinkOn {
		t.Error("/thinking toggle failed")
	}

	// /model with an argument sets the pending model.
	m.ta.SetValue("/model gpt-4o")
	exec(m.submit())
	if m.pendModel != "gpt-4o" || m.model != "gpt-4o" {
		t.Errorf("/model arg = %q/%q", m.pendModel, m.model)
	}

	// Unknown command surfaces a notice rather than being sent to the agent.
	m.ta.SetValue("/nope")
	exec(m.submit())
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "unknown command") {
			found = true
		}
	}
	if !found {
		t.Error("unknown command produced no notice")
	}
	if m.busy {
		t.Error("a slash command must not start an agent turn")
	}
}

func TestSlashCommandOpensPanels(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("/sessions")
	exec(m.submit())
	if m.panel != panelSessions {
		t.Errorf("/sessions did not open the panel: %d", m.panel)
	}
	m.closePanel()

	m.ta.SetValue("/model")
	exec(m.submit())
	if m.panel != panelModels {
		t.Errorf("/model (no arg) did not open the model picker: %d", m.panel)
	}
}

func TestCommandCompletion(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("/se")
	m.syncAC()
	if !m.ac.open || m.ac.mode != acCmd {
		t.Fatalf("command popup not open in cmd mode (open=%v mode=%d)", m.ac.open, m.ac.mode)
	}
	if len(m.ac.items) == 0 || m.ac.items[0].ID != "/sessions" {
		t.Fatalf("completion items = %+v", m.ac.items)
	}
	// Tab completes the command name with a trailing space.
	m.acceptCompletion()
	if m.ta.Value() != "/sessions " {
		t.Errorf("accepted value = %q", m.ta.Value())
	}
	if m.ac.open {
		t.Error("popup should close after accept")
	}
}

// While the command popup is open, plain keys must keep flowing into the
// input (narrowing the filter) instead of being swallowed.
func TestCommandPopupKeepsTyping(t *testing.T) {
	m := wired(t)
	m.Update(key("/")) // opens the command popup
	if !m.ac.open || m.ac.mode != acCmd {
		t.Fatalf("command popup not open: %+v", m.ac)
	}
	m.Update(key("h")) // must reach the input, not be swallowed
	if got := m.ta.Value(); got != "/h" {
		t.Fatalf("input after typing = %q, want %q", got, "/h")
	}
	if !m.ac.open || m.ac.mode != acCmd {
		t.Fatal("popup should stay open while the name is typed")
	}
	if len(m.ac.items) != 1 || m.ac.items[0].ID != "/help" {
		t.Errorf("popup should re-filter to /help: %+v", m.ac.items)
	}
	// Backspace edits the input too.
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.ta.Value(); got != "/" {
		t.Errorf("input after backspace = %q, want %q", got, "/")
	}
	// ctrl+c still quits while the popup has capture.
	m.Update(key("ctrl+c"))
	if !m.quitting {
		t.Error("ctrl+c should quit while the popup is open")
	}
}

func TestCommandPopupEnterExecutes(t *testing.T) {
	m := wired(t)
	m.msgs = append(m.msgs, message{role: roleUser, content: "x"})
	m.ta.SetValue("/cl")
	m.syncAC() // opens popup with /clear highlighted
	if m.ac.mode != acCmd || len(m.ac.items) == 0 {
		t.Fatalf("cmd popup not ready: %+v", m.ac)
	}
	m.Update(key("enter")) // executes the highlighted command directly
	if len(m.msgs) != 0 {
		t.Errorf("/clear via popup enter left %d messages", len(m.msgs))
	}
	if m.ac.open {
		t.Error("popup should close after executing")
	}
}

func TestSlashCancelAndQuit(t *testing.T) {
	m := wired(t)
	m.busy = true
	m.sessionID = "s1"
	m.authToken = "a1"
	m.ta.SetValue("/cancel")
	if cmd := m.submit(); cmd == nil {
		t.Error("/cancel should return a cancel command while busy")
	} else {
		exec(cmd)
	}

	m.ta.SetValue("/quit")
	_ = m.submit()
	if !m.quitting {
		t.Error("/quit should set quitting")
	}
}

func TestRunSelectedCommandEmpty(t *testing.T) {
	m := wired(t)
	m.ac.open = true
	m.ac.mode = acCmd
	m.ac.items = nil
	if m.runSelectedCommand() != nil {
		t.Error("runSelectedCommand with no items should be nil")
	}
	if m.ac.open {
		t.Error("popup should close")
	}
}

func TestOpenCmdACResetsSelection(t *testing.T) {
	m := wired(t)
	m.ac.sel = 5
	m.openCmdAC("model") // matches a single command
	if m.ac.sel != 0 {
		t.Errorf("selection not reset: %d", m.ac.sel)
	}
	if len(m.ac.items) != 1 || m.ac.items[0].ID != "/model" {
		t.Errorf("openCmdAC items = %+v", m.ac.items)
	}
}

func TestCommandModeDropsStaleRefResult(t *testing.T) {
	m := wired(t)
	// Simulate an in-flight @-search, then switch to command mode.
	m.ac.mode = acRef
	m.ac.seq = 5
	m.ta.SetValue("/he")
	m.syncAC() // switches to cmd mode and bumps seq
	// A late ref result for the old seq must be ignored.
	m.Update(acResultMsg{seq: 5, items: nil})
	if m.ac.mode != acCmd {
		t.Error("stale ref result clobbered command-mode popup")
	}
}
