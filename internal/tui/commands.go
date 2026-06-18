package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// command is a slash command typed in the input as "/name [args]".
type command struct {
	name string
	desc string
	run  func(m *Model, args string) tea.Cmd
}

// slashCommands is the registry. Keeping it a function keeps the closures
// simple and avoids package-init ordering concerns.
func slashCommands() []command {
	return []command{
		{"help", "show available commands", func(m *Model, _ string) tea.Cmd {
			m.showHelp()
			return nil
		}},
		{"clear", "clear the conversation", func(m *Model, _ string) tea.Cmd {
			m.msgs = nil
			m.curIdx = -1
			m.refresh()
			return nil
		}},
		{"sessions", "browse & resume saved sessions", func(m *Model, _ string) tea.Cmd {
			return m.openSessions()
		}},
		{"model", "switch model — /model [name]", func(m *Model, args string) tea.Cmd {
			if args != "" {
				m.pendModel = args
				m.model = args
				m.addNote("model set to " + args + " (applies next turn)")
				m.refresh()
				return nil
			}
			return m.openModels()
		}},
		{"thinking", "extended thinking — /thinking [on|off]", func(m *Model, args string) tea.Cmd {
			switch strings.ToLower(args) {
			case "on", "enabled", "true":
				m.thinkOn = true
			case "off", "disabled", "false":
				m.thinkOn = false
			default:
				m.thinkOn = !m.thinkOn
			}
			state := "off"
			if m.thinkOn {
				state = "on"
			}
			m.addNote("thinking " + state)
			m.refresh()
			return nil
		}},
		{"cancel", "cancel the running turn", func(m *Model, _ string) tea.Cmd {
			return m.cancelRun()
		}},
		{"quit", "exit bodek", func(m *Model, _ string) tea.Cmd {
			m.quitting = true
			return tea.Quit
		}},
	}
}

// commandPrefix reports the command token when the input is a line-initial
// slash command still being typed (a leading "/" with no whitespace yet).
func commandPrefix(s string) (string, bool) {
	if !strings.HasPrefix(s, "/") {
		return "", false
	}
	body := s[1:]
	if strings.ContainsAny(body, " \t\n") {
		return "", false
	}
	return body, true
}

// runCommandLine parses and dispatches a full "/name args" line.
func (m *Model) runCommandLine(text string) tea.Cmd {
	body := strings.TrimPrefix(text, "/")
	name, args := body, ""
	if i := strings.IndexAny(body, " \t"); i >= 0 {
		name, args = body[:i], strings.TrimSpace(body[i+1:])
	}
	return m.runCommand(name, args)
}

// runCommand finds and executes a command by name, resetting the input.
func (m *Model) runCommand(name, args string) tea.Cmd {
	m.ta.Reset()
	m.closeAC()
	for _, c := range slashCommands() {
		if c.name == name {
			return c.run(m, args)
		}
	}
	m.addNote("unknown command: /" + name + " — try /help")
	m.refresh()
	return nil
}

// runSelectedCommand executes the command highlighted in the popup.
func (m *Model) runSelectedCommand() tea.Cmd {
	if len(m.ac.items) == 0 {
		m.closeAC()
		return nil
	}
	name := strings.TrimPrefix(m.ac.items[m.ac.sel].ID, "/")
	return m.runCommand(name, "")
}

// openCmdAC populates the popup with commands matching the typed prefix.
func (m *Model) openCmdAC(query string) {
	var items []client.Resource
	for _, c := range slashCommands() {
		if strings.HasPrefix(c.name, query) {
			items = append(items, client.Resource{
				ID: "/" + c.name, Type: "command", Label: "/" + c.name, Detail: c.desc,
			})
		}
	}
	m.ac.open = true
	m.ac.loading = false
	m.ac.mode = acCmd
	m.ac.query = query
	m.ac.items = items
	m.ac.seq++ // invalidate any in-flight @-search result
	if m.ac.sel >= len(items) {
		m.ac.sel = 0
	}
	m.relayout()
	m.refresh()
}

// showHelp appends a rendered help card listing commands and key bindings.
func (m *Model) showHelp() {
	var b strings.Builder
	b.WriteString("### Commands\n\n")
	for _, c := range slashCommands() {
		b.WriteString(fmt.Sprintf("- `/%s` — %s\n", c.name, c.desc))
	}
	b.WriteString("\n### Keys\n\n")
	b.WriteString("`@` attach files/sessions · `^R` sessions · `^O` model · " +
		"`^T` thinking · `^J` newline · `Esc` cancel · `^L` clear · `^C` quit\n")
	content := b.String()
	m.msgs = append(m.msgs, message{role: roleAsst, content: content, rendered: m.render(content)})
	m.refresh()
}
