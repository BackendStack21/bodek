package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// --plain replaces the alt-screen transcript with an append-only scrollback
// log: agent events print as linear text above a minimal input chrome while
// events stream in. Severity rides text prefixes (never color alone), and
// the mode is the honest surface for screen readers and `bodek --plain
// < task > run.log` pipelines.

func TestPlainEventLines(t *testing.T) {
	m := newTestModel()
	m.model = "deepseek-v4"

	tests := []struct {
		name string
		ev   client.Event
		want string // exact single line, or substring when suffixed with …
	}{
		{"tool_call", client.Event{Type: "tool_call", Name: "shell",
			Data: `{"command":"ls -la"}`}, `▸ shell · {"command":"ls -la"}`},
		{"tool_result ok", client.Event{Type: "tool_result", Name: "shell",
			Data: "main.go\nREADME.md"}, "▪ shell ✓"},
		{"tool_result fail", client.Event{Type: "tool_result", Name: "shell",
			Data: "Error: exit status 1"}, "▪ shell ✗"},
		{"thinking", client.Event{Type: "thinking",
			Content: "checking the files"}, "[think] checking the files"},
		{"error", client.Event{Type: "error", Message: "boom"}, "[error] boom"},
		{"skill note", client.Event{Type: "skill_event", SubType: "loaded",
			SkillName: "go"}, "· skill · loaded go"},
	}
	for _, tc := range tests {
		got := m.plainEventLines(tc.ev)
		if len(got) != 1 {
			t.Errorf("%s: got %d lines (%v), want 1", tc.name, len(got), got)
			continue
		}
		if tc.ev.Type == "tool_result" && !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: line %q missing %q", tc.name, got[0], tc.want)
			continue
		}
		if got[0] != tc.want {
			t.Errorf("%s: line = %q, want %q", tc.name, got[0], tc.want)
		}
	}
}

func TestPlainEventLinesApproval(t *testing.T) {
	m := newTestModel()
	got := m.plainEventLines(client.Event{Type: "approval_request",
		Risk: "shell_exec", Command: "rm -rf x"})
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	for _, want := range []string{"⚠ approval", "shell_exec", "rm -rf x", "Esc"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("approval line %q missing %q", got[0], want)
		}
	}
}

func TestPlainEventLinesSuppressed(t *testing.T) {
	// Streaming fragments and telemetry never print: the reply lands whole
	// on done, and per-token lines would bury the log.
	m := newTestModel()
	for _, ev := range []client.Event{
		{Type: "token", Content: "hel"},
		{Type: "token_delta", Content: "lo"},
		{Type: "thinking_delta", Content: "hm"},
		{Type: "usage", ContextTokens: 99},
	} {
		if got := m.plainEventLines(ev); got != nil {
			t.Errorf("%s: printed %v, want nothing", ev.Type, got)
		}
	}
}

func TestPlainDoneLines(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleUser, content: "q"},
		message{role: roleAsst, content: "The answer."})
	m.turnStats = append(m.turnStats, turnStats{toolCount: 2, outTok: 42, wall: 1500 * 1e6})

	got := m.plainEventLines(client.Event{Type: "done"})
	if len(got) < 2 {
		t.Fatalf("got %d lines (%v), want reply + summary", len(got), got)
	}
	if got[0] != "The answer." {
		t.Errorf("reply line = %q, want the finalized reply", got[0])
	}
	for _, want := range []string{"✓ done", "2 tools", "42 tok"} {
		if !strings.Contains(got[len(got)-1], want) {
			t.Errorf("summary %q missing %q", got[len(got)-1], want)
		}
	}
}

func TestPlainClip(t *testing.T) {
	long := strings.Repeat("x", 500)
	if got := plainClip(long); len(got) > 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("plainClip kept %d chars, want a bounded prefix ending in …", len(got))
	}
	if got := plainClip("short"); got != "short" {
		t.Errorf("plainClip(short) = %q", got)
	}
}

func TestPlainPromptLine(t *testing.T) {
	if got := plainPromptLine("hi\nthere"); got != "❯ hi there" {
		t.Errorf("promptLine = %q, want the collapsed single line", got)
	}
}

func TestPlainCmdGating(t *testing.T) {
	m := newTestModel()
	ev := client.Event{Type: "tool_call", Name: "shell", Data: "ls"}

	if cmd := m.plainPrintCmd(ev); cmd != nil {
		t.Error("plain mode off: expected nil cmd")
	}
	m.plain = true
	if cmd := m.plainPrintCmd(ev); cmd == nil {
		t.Error("plain mode on: expected a print cmd")
	}
	if cmd := m.plainPrintCmd(client.Event{Type: "token", Content: "x"}); cmd != nil {
		t.Error("suppressed event still produced a cmd")
	}
}

func TestPlainViewIsChromeOnly(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "q"},
		message{role: roleAsst, content: "secret-reply-body"})
	m.curIdx = 1
	m.resize(100, 30)
	m.refresh()

	tui := m.View()
	if !strings.Contains(tui, "secret-reply-body") {
		t.Fatal("TUI view lost the transcript — fixture broken")
	}

	m.plain = true
	plain := m.View()
	if strings.Contains(plain, "secret-reply-body") {
		t.Error("plain view renders transcript content; it must live in scrollback")
	}
	if strings.Contains(plain, "bodek") && strings.Contains(plain, "⬡") {
		t.Error("plain view renders the full header; linear mode wants minimal chrome")
	}
	if plain == "" {
		t.Error("plain view empty — input chrome vanished")
	}
}

func TestPlainViewKeepsPanel(t *testing.T) {
	// Management panels stay reachable in linear mode, rendered as capped
	// bottom chrome instead of taking the whole terminal.
	m := newTestModel()
	m.plain = true
	m.panel = panelSessions
	m.resize(100, 30)
	if v := m.View(); v == "" {
		t.Error("plain view with an open panel rendered nothing")
	}
}
