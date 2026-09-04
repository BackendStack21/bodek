package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestLastSentences(t *testing.T) {
	in := "First beat. Second beat.\nThird beat!"
	if got := lastSentences(in, 2); !strings.Contains(got, "Second") || !strings.Contains(got, "Third") || strings.Contains(got, "First") {
		t.Errorf("last 2 = %q", got)
	}
	if got := lastSentences("only one", 2); got != "only one" {
		t.Errorf("short input = %q", got)
	}
	if got := lastSentences("Keep\nnewlines inside. Next.", 2); !strings.Contains(got, "Keep") {
		t.Errorf("should keep a short pair whole-ish: %q", got)
	}
}

func TestThinkingExcerptKeepsNewlines(t *testing.T) {
	in := "I will reset jobsPrev.\nThen re-baseline the snapshot."
	got := thinkingExcerpt(in)
	if !strings.Contains(got, "\n") {
		t.Errorf("excerpt flattened newlines: %q", got)
	}
	if strings.Contains(got, "  ") && strings.Contains(got, "I will") && !strings.Contains(got, "\n") {
		t.Errorf("excerpt looks collapsed: %q", got)
	}
}

func TestStepPeekCapsAtTwoLines(t *testing.T) {
	th := newTheme()
	body := "one\ntwo\nthree\nfour"
	got := stepPeek("shell", body, 80, th)
	if len(got) != 2 {
		t.Fatalf("peek lines = %d, want 2: %v", len(got), got)
	}
	plain0 := plain(got[0])
	plain1 := plain(got[1])
	if !strings.Contains(plain0, "one") || !strings.Contains(plain1, "two") {
		t.Errorf("peek = %q / %q", plain0, plain1)
	}
	if stepPeek("shell", "   ", 80, th) != nil {
		t.Error("blank result must peek nothing")
	}
}

func TestTurnReceipt(t *testing.T) {
	msg := message{steps: []step{
		{name: "apply_patch", arg: "internal/tui/events.go", done: true,
			result: "--- a\n+++ b\n@@\n-old\n+new\n+newer\n"},
		{name: "write_file", arg: "internal/tui/jobs_tab.go", done: true, result: "ok"},
		{name: "read_file", arg: "internal/tui/model.go", done: true, result: "package tui"},
		{name: "shell", arg: "go test ./internal/tui/", done: true,
			result: "ok  \tgithub.com/BackendStack21/bodek/internal/tui\t0.54s"},
	}}
	r := scanReceipt(msg)
	if r.files != 2 {
		t.Errorf("files = %d, want 2 (writes only)", r.files)
	}
	if !r.hasDiff || r.adds < 1 || r.dels < 1 {
		t.Errorf("diff = +%d −%d has=%v", r.adds, r.dels, r.hasDiff)
	}
	if r.tests != "✓" {
		t.Errorf("tests = %q, want ✓", r.tests)
	}
	got := formatReceipt(r)
	for _, want := range []string{"touched 2", "+", "−", "tests ✓"} {
		if !strings.Contains(got, want) {
			t.Errorf("receipt %q missing %q", got, want)
		}
	}
}

func TestCollapseSummaryPrefersReceipt(t *testing.T) {
	m := newTestModel()
	msg := message{
		role: roleAsst, collapsed: true, content: "I'll fix the disconnect path.",
		steps: []step{{name: "write_file", arg: "events.go", done: true, result: "ok"}},
	}
	got := m.collapseSummary(msg)
	if !strings.Contains(got, "touched 1") {
		t.Errorf("folded card should use the receipt: %q", got)
	}
	if strings.Contains(got, "reply:") {
		t.Errorf("receipt should replace the generic reply preview: %q", got)
	}
}

func TestRunningStepLiveProgress(t *testing.T) {
	m := newTestModel()
	out, _ := renderStepsForTest(m, message{streaming: true, steps: []step{
		{name: "read_file", arg: "events.go", started: time.Now().Add(-2 * time.Second)},
	}}, 0, 0)
	got := plain(out)
	if !strings.Contains(got, "reading") {
		t.Errorf("running step missing progress copy: %q", got)
	}
	if !strings.Contains(got, "2.0s") && !strings.Contains(got, "1.9s") && !strings.Contains(got, "2.1s") {
		// 2s ± a tick — don't assert an exact tenth.
		if !strings.Contains(got, "s") {
			t.Errorf("running step missing a duration: %q", got)
		}
	}
}

func TestSwarmBandParallel(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.msgs[1].steps = []step{
		{name: "read_file", arg: "a.go", started: time.Now().Add(-2 * time.Second)},
		{name: "read_file", arg: "b.go", started: time.Now().Add(-time.Second)},
		{name: "read_file", arg: "c.go", started: time.Now()},
	}
	m.msgs[1].items = []turnItem{{stepIdx: 0}, {stepIdx: 1}, {stepIdx: 2}}
	out := plain(func() string { s, _ := m.renderMessage(m.msgs[1], 1, 0); return s }())
	if !strings.Contains(out, "parallel") || !strings.Contains(out, "3 running") {
		t.Errorf("expected swarm band:\n%s", out)
	}

	m.msgs[1].steps[0].done = true
	m.msgs[1].steps[0].result = "package a"
	out = plain(func() string { s, _ := m.renderMessage(m.msgs[1], 1, 0); return s }())
	if !strings.Contains(out, "2 running") {
		t.Errorf("swarm should shrink to the unfinished pair:\n%s", out)
	}
	if !strings.Contains(out, "package a") {
		t.Errorf("completed member should drop to a peek row:\n%s", out)
	}

	m.msgs[1].steps[1].done = true
	m.msgs[1].steps[1].result = "package b"
	out = plain(func() string { s, _ := m.renderMessage(m.msgs[1], 1, 0); return s }())
	if strings.Contains(out, "parallel") {
		t.Errorf("one leftover must dissolve the band:\n%s", out)
	}
}

func TestIntentRailTwoBeats(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "thinking", Content: "First plan sentence."})
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"main.go"}`})
	m.handleEvent(client.Event{Type: "thinking", Content: "Second plan sentence."})
	out := plain(func() string { s, _ := m.renderMessage(m.msgs[0], 0, 0); return s }())
	if strings.Count(out, "┊") < 2 {
		t.Errorf("each thinking block should rail:\n%s", out)
	}
	if !strings.Contains(out, "2 beats") {
		t.Errorf("multi-block turn should name the beat count:\n%s", out)
	}
}

func TestReceiptRidesTurnHead(t *testing.T) {
	m := newTestModel()
	msg := message{
		role:  roleAsst,
		stats: &turnStats{latency: 1.2, ctxTok: 10, outTok: 4},
		steps: []step{{name: "write_file", arg: "foo.go", done: true, result: "ok"}},
	}
	out := plain(func() string { s, _ := m.renderMessage(msg, 0, 0); return s }())
	head := strings.Split(out, "\n")[0]
	if !strings.Contains(head, "touched 1") {
		t.Errorf("turn head missing receipt: %q", head)
	}
}

func TestThinkingStoresStartTime(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "thinking", Content: "plan"})
	if m.msgs[0].items[0].started.IsZero() {
		t.Fatal("thinking item must stamp started")
	}
	m.handleEvent(client.Event{Type: "done", Latency: 0.2, ContextTokens: 1, OutputTokens: 1})
	if m.msgs[0].items[0].dur <= 0 {
		t.Fatal("finalize must stamp thinking duration")
	}
}
