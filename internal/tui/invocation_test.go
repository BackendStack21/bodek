package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestToolInvocationSurvivesLiveAndReplay(t *testing.T) {
	command := "printf 'start'\n" + strings.Repeat("echo x", 125) + " important-tail"
	data, err := json.Marshal(map[string]string{"command": command, "workdir": "/tmp/work"})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: string(data)})
	s := m.msgs[0].steps[0]
	if s.callArgs != string(data) || s.argsOmitted {
		t.Fatal("live tool call did not retain its arguments")
	}
	if !m.hintsShown[hintSteps] {
		t.Fatal("invocation hint must appear while the call is running")
	}
	for _, want := range []string{"invocation · command", "important-tail", "workdir", "/tmp/work"} {
		if !strings.Contains(invocationText(s), want) {
			t.Errorf("live invocation missing %q", want)
		}
	}

	call := client.SessionToolCall{ID: "one"}
	call.Function.Name = "shell"
	call.Function.Arguments = string(data)
	replay := newTestModel()
	replay.replayTranscript([]client.SessionMessage{
		{Role: "user", Content: "run it"},
		{Role: "assistant", ToolCalls: []client.SessionToolCall{call}},
	})
	if got := replay.msgs[1].steps[0].callArgs; got != string(data) {
		t.Fatalf("replayed invocation = %q", got)
	}
	if !strings.Contains(invocationText(replay.msgs[1].steps[0]), "important-tail") {
		t.Fatal("replayed command tail is inaccessible")
	}
}

func TestExpandedStepShowsInvocationBeforeResult(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"printf 'audit me' && echo hidden-tail"}`})
	if strings.Contains(plain(m.conversation()), "hidden-tail") {
		t.Fatal("collapsed tool exposed its command tail")
	}
	m.toggleStep(0, 0)
	if out := plain(m.conversation()); !strings.Contains(out, "hidden-tail") {
		t.Fatalf("running tool has no expanded invocation: %q", out)
	}
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "tool-output"})
	out := plain(m.conversation())
	commandAt, resultAt := strings.Index(out, "hidden-tail"), strings.Index(out, "tool-output")
	if commandAt < 0 || resultAt < 0 || commandAt >= resultAt {
		t.Fatalf("expanded invocation must precede result: %q", out)
	}
}

func TestInvocationWrapsAndPagesWithoutLosingTail(t *testing.T) {
	m := newTestModel()
	command := strings.Repeat("long-token", 70) + " important-tail"
	data, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: string(data)})
	m.msgs[0].steps[0].expanded = true
	m.inspect = &inspectTarget{msgIdx: 0, stepIdx: 0, itemIdx: -1}
	width := max(m.vp.Width-8, 4)
	for _, line := range invocationDetailLines(m.msgs[0].steps[0], width, m.th) {
		if lipgloss.Width(line) > width {
			t.Fatalf("invocation line is wider than the page: %d > %d", lipgloss.Width(line), width)
		}
	}
	if strings.Contains(plain(m.conversation()), "important-tail") {
		t.Fatal("command tail should need another page in this fixture")
	}
	found := false
	for i := 0; i < 30; i++ {
		m.Update(key("pgdown"))
		if strings.Contains(plain(m.conversation()), "important-tail") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("paging never reached the command tail")
	}
}

func TestInvocationDisplaysUnsafeCharactersAndLimit(t *testing.T) {
	data, err := json.Marshal(map[string]string{
		"command": "echo safe\x1b]52;c;secret\x07\u202e",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := step{name: "shell", callArgs: string(data)}
	got := invocationText(s)
	if strings.ContainsAny(got, "\x1b\x07") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("unsafe control reached invocation display: %q", got)
	}
	for _, want := range []string{`\x1B`, `\x07`, `\u202E`} {
		if !strings.Contains(got, want) {
			t.Errorf("hidden character %q was not represented: %q", want, got)
		}
	}
	if got := visibleInvocation(string([]byte{0xff, 'x'})); got != `\xFFx` {
		t.Fatalf("invalid UTF-8 byte was hidden: %q", got)
	}

	raw := `{"commands":["first","second"],"note":"` + strings.Repeat("x", toolArgsLimit) + `"}`
	retained, omitted := retainToolArgs(raw)
	if !omitted || len(retained) > toolArgsLimit {
		t.Fatal("tool arguments were not bounded")
	}
	limited := invocationText(step{name: "parallel_shell", callArgs: retained, argsOmitted: omitted})
	if !strings.Contains(limited, "limited to 256 KiB") || !strings.Contains(limited, "remaining invocation arguments omitted") {
		t.Fatal("bounded invocation has no visible limit marker")
	}
}

func TestExpandedApprovalCommandWrapsWideCharacters(t *testing.T) {
	m := newTestModel()
	m.approvals = []client.Event{{Type: "approval_request", Command: strings.Repeat("界", 45) + " command-tail"}}
	m.apprExpanded = true
	out := plain(m.approvalBody())
	if !strings.Contains(strings.ReplaceAll(out, "\n", ""), "command-tail") {
		t.Fatalf("approval command tail was clipped: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > m.cardInner() {
			t.Fatalf("approval line exceeds display width: %d > %d", lipgloss.Width(line), m.cardInner())
		}
	}
}

func TestNestedInvocationAndCopyTarget(t *testing.T) {
	s := step{name: "parallel_shell", callArgs: `{"commands":[{"command":"go test ./..."},{"command":"go vet ./..."}]}`}
	got := invocationText(s)
	for _, want := range []string{"go test ./...", "go vet ./..."} {
		if !strings.Contains(got, want) {
			t.Errorf("nested invocation missing %q", want)
		}
	}
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, steps: []step{s}})
	m.inspect = &inspectTarget{msgIdx: 0, stepIdx: 0, itemIdx: -1}
	if m.copyFocusedInvocation() == nil || !m.copyFlashing() {
		t.Fatal("selected invocation was not offered to the clipboard")
	}
}
