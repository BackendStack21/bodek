package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestStructuredBatchBoundsAreHonest(t *testing.T) {
	rows := make([]string, 300)
	for i := range rows {
		body := strings.Repeat("x", 3000)
		rows[i] = `{"path":"file-` + string(rune('a'+i%26)) + `.go","content":"` + body + `","stdout":"` + body + `"}`
	}
	raw := `{"results":[` + strings.Join(rows, ",") + `]}`
	detail := boundedStructuredDetail("batch_read", raw)
	th := newTheme()
	if got := plain(structuredHeadSuffix("batch_read", detail, th)); got != "256/300 files shown" {
		t.Fatalf("bounded batch summary = %q", got)
	}
	details := plain(strings.Join(stepDetail("batch_read", detail, 80, th), "\n"))
	if !strings.Contains(details, "44 more items omitted") {
		t.Fatalf("missing item omission marker:\n%s", details[:min(len(details), 1000)])
	}
	if !strings.Contains(details, "item output omitted") {
		t.Fatalf("missing body omission marker:\n%s", details[:min(len(details), 1000)])
	}
}

func TestStructuredUnknownFallbackStaysGeneric(t *testing.T) {
	raw := `{"results":[{"stdout":"known"},{"metadata":{"nested":true}}]}`
	detail := boundedStructuredDetail("parallel_shell", raw)
	if _, ok := structuredJSONItems("parallel_shell", detail); ok {
		t.Fatal("unknown mixed results unexpectedly became structured items")
	}
	if got := structuredHeadSuffix("parallel_shell", detail, newTheme()); got != "" {
		t.Fatalf("unknown fallback inferred batch summary: %q", plain(got))
	}
}

func TestStructuredBatchDetailSurvivesLiveIngestion(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "batch_read", Data: `{"paths":["a.go","b.go"]}`})
	raw := `{"results":[{"path":"a.go","content":"1|package main","total_lines":9},{"path":"b.go","content":"1|package test","total_lines":4}]}`
	m.handleEvent(client.Event{Type: "tool_result", Name: "batch_read", Data: raw})

	s := m.msgs[0].steps[0]
	if s.detailResult == "" {
		t.Fatal("structured detail was discarded during live ingestion")
	}
	details := plain(strings.Join(stepDetail(s.name, stepDetailResult(s), 60, newTheme()), "\n"))
	if !strings.Contains(details, "a.go") || !strings.Contains(details, "b.go") {
		t.Fatalf("batch labels missing from detail:\n%s", details)
	}
	if s.result == s.detailResult {
		t.Fatal("normalized result and structured detail should remain separate")
	}
}

func TestStructuredBatchLogBracketDoesNotChangeItemCount(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "parallel_shell", Data: `{}`})
	raw := `{"results":[{"command":"first","stdout":"start\n[2] warning","exit_code":0},{"command":"second","stdout":"done","exit_code":0}]}`
	m.handleEvent(client.Event{Type: "tool_result", Name: "parallel_shell", Data: raw})

	s := m.msgs[0].steps[0]
	if got := plain(stepHeadSuffix(s.name, s.arg, stepDetailResult(s), newTheme())); got != "✓ 2 commands" {
		t.Fatalf("bracketed log changed batch count: %q", got)
	}
}

func TestStructuredBatchDetailPreservesFailureAfterLiveAndReplay(t *testing.T) {
	raw := `{"results":[{"path":"missing.go","error":"file not found","success":false}]}`

	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "tool_call", Name: "batch_patch", Data: `{"patches":[]}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "batch_patch", Data: raw})
	live := m.msgs[0].steps[0]
	if !live.isErr || !live.expanded {
		t.Fatalf("live structured failure not retained: %+v", live)
	}

	replay := newTestModel()
	call := client.SessionToolCall{ID: "call-1"}
	call.Function.Name = "batch_patch"
	call.Function.Arguments = `{"patches":[]}`
	replay.replayTranscript([]client.SessionMessage{
		{Role: "user", Content: "patch it"},
		{Role: "assistant", ToolCalls: []client.SessionToolCall{call}},
		{Role: "tool", Name: "batch_patch", ToolCallID: "call-1", Content: "┌── TOOL RESULT: batch_patch\n" + raw + "\n└── END TOOL RESULT: batch_patch"},
	})
	if len(replay.msgs) != 2 || !replay.msgs[1].steps[0].isErr {
		t.Fatalf("restored structured failure not retained: %#v", replay.msgs)
	}
	step := replay.msgs[1].steps[0]
	if got := plain(strings.Join(stepDetail(step.name, stepDetailResult(step), 60, newTheme()), "\n")); !strings.Contains(got, "missing.go") {
		t.Fatalf("restored batch label missing from detail: %s", got)
	}
}

func TestStructuredTextDoesNotInventBatchGrouping(t *testing.T) {
	th := newTheme()
	if got := structuredHeadSuffix("parallel_shell", "[2] warning", th); got != "" {
		t.Fatalf("legacy bracketed text inferred a batch: %q", plain(got))
	}
	if got := stepDetail("parallel_shell", "[2] warning", 60, th); len(got) != 1 || !strings.Contains(plain(got[0]), "[2] warning") {
		t.Fatalf("legacy bracketed text was not kept plain: %#v", got)
	}
}

func TestCompletedPlanHeaderRenders(t *testing.T) {
	th := newTheme()
	result := "[Current plan: v3 — all 2 steps complete.]"
	if got := plain(stepHeadSuffix("plan", "", result, th)); got != "v3 · 2/2 done · 0 blocked" {
		t.Fatalf("completed plan suffix = %q", got)
	}
	lines := planSnapshotLines(result, 60, th)
	if len(lines) != 1 || !strings.Contains(plain(lines[0]), "2/2 done") {
		t.Fatalf("completed plan detail = %#v", lines)
	}
}
