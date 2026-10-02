package tui

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestPlainProgressNotesBeforeTools(t *testing.T) {
	for _, kind := range []string{"token", "token_delta"} {
		t.Run(kind, func(t *testing.T) {
			m := newTestModel()
			m.plain = true
			streamingTurn(m)
			for _, text := range []string{"I’ll inspect ", "the files. 检查文件。"} {
				ev := client.Event{Type: kind, Content: text}
				m.handleEvent(ev)
				if lines := m.plainEventLines(ev); len(lines) != 0 {
					t.Fatalf("fragments printed separately: %v", lines)
				}
			}
			ev := client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"main.go"}`}
			m.handleEvent(ev)
			lines := m.plainEventLines(ev)
			if len(lines) != 2 || lines[0] != "I’ll inspect the files. 检查文件。" || !strings.HasPrefix(lines[1], "▸ read_file") {
				t.Fatalf("note must precede its tool: %v", lines)
			}
			m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "ok"})
			m.handleEvent(client.Event{Type: "token", Content: "The fix is ready."})
			m.handleEvent(client.Event{Type: "done"})
			lines = m.plainEventLines(client.Event{Type: "done"})
			if len(lines) != 2 || lines[0] != "The fix is ready." {
				t.Fatalf("completion repeated or lost prose: %v", lines)
			}
			if got := m.msgs[1].content; got != "I’ll inspect the files. 检查文件。\n\nThe fix is ready." {
				t.Fatalf("printing changed the transcript: %q", got)
			}
		})
	}
}

// A real Bubble Tea renderer checks that ingestion returns an executable
// scrollback print, rather than only changing the transcript model.
type plainPrintProbe struct{ cmd tea.Cmd }

func (p plainPrintProbe) Init() tea.Cmd { return p.cmd }
func (p plainPrintProbe) View() string  { return "" }
func (p plainPrintProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if reflect.TypeOf(msg) == reflect.TypeOf(tea.Println("")()) {
		return p, tea.Quit
	}
	return p, nil
}

func capturePlainPrint(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := tea.NewProgram(plainPrintProbe{cmd: cmd}, tea.WithInput(strings.NewReader("")),
		tea.WithOutput(&out), tea.WithoutSignals(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		t.Fatalf("scrollback print was not delivered: %v", err)
	}
	return out.String()
}

func TestPlainBatchProgressPrintsInWireOrder(t *testing.T) {
	m := newTestModel()
	m.plain = true
	m.events = progressProbeEvents()
	_, cmd := m.Update(eventBatchMsg{
		{Type: "turn_started", Initiated: "operator"},
		{Type: "token", Content: "Inspecting now."},
		{Type: "tool_call", Name: "read_file", Data: `{"path":"main.go"}`},
		{Type: "tool_result", Name: "read_file", Data: "ok"},
		{Type: "thinking_delta", Content: "a reasoning summary"},
		{Type: "token_delta", Content: "Checking "},
		{Type: "token_delta", Content: "the result."},
		{Type: "tool_call", Name: "shell", Data: `{"command":"go test"}`},
		{Type: "tool_result", Name: "shell", Data: "ok"},
		{Type: "token", Content: "First turn complete."},
		{Type: "done"},
		{Type: "turn_started", Initiated: "system"},
		{Type: "token", Content: "Second turn complete."},
		{Type: "done"},
	})
	out := capturePlainPrint(t, cmd)
	previous := -1
	for _, text := range []string{"Inspecting now.", "▸ read_file", "▪ read_file", "Checking the result.", "▸ shell", "▪ shell", "First turn complete.", "Second turn complete."} {
		index := strings.Index(out, text)
		if index <= previous || strings.Count(out, text) != 1 {
			t.Fatalf("missing, repeated or reordered %q in %q", text, out)
		}
		previous = index
	}
	if strings.Count(out, "✓ done") != 2 || strings.Contains(out, "a reasoning summary") {
		t.Fatalf("wrong boundaries or fragment output: %q", out)
	}
}

func TestPlainBatchDisconnectKeepsPartialNotes(t *testing.T) {
	m := newTestModel()
	m.plain = true
	m.events = progressProbeEvents()
	_, cmd := m.ingestWireBatch([]client.Event{
		{Type: "token", Content: "Starting the work."},
		{Type: "tool_call", Name: "shell", Data: "go test"},
		{Type: "token_delta", Content: "Partial findings."},
		{Type: client.EventDisconnected},
	})
	out := capturePlainPrint(t, cmd)
	for _, text := range []string{"Starting the work.", "▸ shell", "Partial findings.", "connection lost"} {
		if !strings.Contains(out, text) {
			t.Fatalf("disconnect discarded %q in %q", text, out)
		}
	}
	if strings.Count(out, "Starting the work.") != 1 {
		t.Fatalf("disconnect repeated earlier output: %q", out)
	}
}

func TestPlainSessionWakePrintsThroughBothIngestionPaths(t *testing.T) {
	for _, batch := range []bool{false, true} {
		m := newTestModel()
		m.plain = true
		m.events = progressProbeEvents()
		ev := client.Event{Type: "session", SessionID: "wake-session", AuthToken: "session-token", SystemInitiated: true}
		var cmd tea.Cmd
		if batch {
			_, cmd = m.ingestWireBatch([]client.Event{ev})
		} else {
			_, cmd = m.ingestWireEvent(ev)
		}
		out := capturePlainPrint(t, cmd)
		if strings.Count(out, "[wake]") != 1 || m.sessionID != ev.SessionID || m.authToken != ev.AuthToken || m.cur() < 0 || !m.msgs[m.cur()].systemWake {
			t.Fatalf("batch=%v lost session/wake state or output: %q", batch, out)
		}
	}
}

func TestPlainProgressRetainsPartialFailure(t *testing.T) {
	for _, failure := range []string{"provider failed", "context canceled"} {
		t.Run(failure, func(t *testing.T) {
			m := newTestModel()
			m.plain = true
			m.events = progressProbeEvents()
			streamingTurn(m)
			m.handleEvent(client.Event{Type: "token_delta", Content: "Useful partial findings."})
			_, cmd := m.ingestWireEvent(client.Event{Type: "error", Message: failure})
			out := capturePlainPrint(t, cmd)
			if strings.Count(out, "Useful partial findings.") != 1 || strings.Contains(out, "✓ done") {
				t.Fatalf("partial failure output: %q", out)
			}
		})
	}
}

func progressProbeEvents() <-chan client.Event {
	events := make(chan client.Event)
	close(events)
	return events
}

func TestPlainProgressPreservesFormattingAndSanitizes(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)
	note := "**Plan**\n\n" + strings.Repeat("完整内容🙂 ", 80) + "\n- check the files"
	m.handleEvent(client.Event{Type: "token", Content: "\x1b]52;c;ZXhmaWw=\a" + note})
	ev := client.Event{Type: "thinking", Content: "Next reasoning summary."}
	m.handleEvent(ev)
	lines := m.plainEventLines(ev)
	if len(lines) != 2 || !strings.HasSuffix(lines[0], note) || strings.ContainsAny(lines[0], "\x1b\a") || lines[1] != "[think] Next reasoning summary." {
		t.Fatalf("formatted note lost or unsanitized: %q", lines)
	}
	if got := m.plainEventLines(client.Event{Type: "thinking_delta", Content: "more"}); len(got) != 0 {
		t.Fatalf("printed the same note again: %v", got)
	}
	// An empty new turn must not repeat the previous answer on completion.
	m.handleEvent(client.Event{Type: "done"})
	m.handleEvent(client.Event{Type: "turn_started", Initiated: "operator"})
	m.handleEvent(client.Event{Type: "done"})
	if lines := m.plainEventLines(client.Event{Type: "done"}); len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ done") {
		t.Fatalf("empty turn repeated previous prose: %v", lines)
	}
}

func TestPlainProgressEmptyAndStyledCards(t *testing.T) {
	m := newTestModel()
	if lines := m.plainReplyLines(); lines != nil {
		t.Fatalf("empty transcript printed: %v", lines)
	}
	m.msgs = []message{{role: roleUser, content: "question"}, {role: roleAsst, raw: true, content: "styled card"}}
	if lines := m.plainReplyLines(); lines != nil {
		t.Fatalf("styled card printed as wire prose: %v", lines)
	}
	m.msgs = append(m.msgs, message{role: roleAsst, content: "\n "})
	if lines := m.plainReplyLines(); lines != nil {
		t.Fatalf("blank prose printed: %v", lines)
	}
	if cmd := plainLinesCmd(nil); cmd != nil {
		t.Fatal("empty lines produced a print command")
	}
}

func TestBufferedProgressNotesVisibleWithoutReasoning(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		m := newTestModel()
		m.resize(width, 30)
		streamingTurn(m)
		m.handleEvent(client.Event{Type: "token", Content: "Inspecting the files."})
		flush(t, m)
		if out := plain(m.View()); !strings.Contains(out, "Inspecting the files.") {
			t.Fatalf("%d-column live note missing: %s", width, out)
		}
		m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"main.go"}`})
		m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "ok"})
		m.handleEvent(client.Event{Type: "token_delta", Content: "The change is "})
		m.handleEvent(client.Event{Type: "token_delta", Content: "ready."})
		m.handleEvent(client.Event{Type: "done"})
		out := plain(m.conversation())
		previous := -1
		for _, text := range []string{"Inspecting the files.", "read_file", "The change is ready."} {
			index := strings.Index(out, text)
			if index <= previous || strings.Count(out, text) != 1 {
				t.Fatalf("%d-column note/tool/final order wrong: %s", width, out)
			}
			previous = index
		}
	}
}
