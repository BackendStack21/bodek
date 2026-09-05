package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestLastCompleteSentences(t *testing.T) {
	in := "First beat. Second beat. Growing tail"
	if got := lastCompleteSentences(in, 2); !strings.Contains(got, "First") || !strings.Contains(got, "Second") || strings.Contains(got, "Growing") {
		t.Errorf("complete last 2 = %q", got)
	}
	if got := lastCompleteSentences("no period yet at all", 2); got != "" {
		t.Errorf("unfinished only = %q, want empty", got)
	}
	if got := lastCompleteSentences("Done.", 2); got != "Done." {
		t.Errorf("single complete = %q", got)
	}
}

func TestThinkingExcerptLiveHoldsSentences(t *testing.T) {
	held := thinkingExcerptLive("I will read the file. Then I patch it. And now I am mid")
	if !strings.Contains(held, "I will read") || !strings.Contains(held, "patch") {
		t.Errorf("live excerpt dropped a finished sentence: %q", held)
	}
	if strings.Contains(held, "mid") {
		t.Errorf("live excerpt chased the unfinished tail: %q", held)
	}

	stem := thinkingExcerptLive("opening clause without a stop " + strings.Repeat("word ", 40))
	if n := len([]rune(stem)); n > thinkingLiveStem {
		t.Errorf("live stem exceeded the freeze cap (%d > %d): %q", n, thinkingLiveStem, stem)
	}
	if !strings.Contains(stem, "opening") {
		t.Errorf("live stem lost the opening: %q", stem)
	}
}

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
	if !strings.Contains(got, "▸") {
		t.Errorf("running step must use the static live glyph, not a spinner: %q", got)
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
	// Calm default: the finished member drops to a head-only row — its
	// result stays behind ^E like every tool response.
	if strings.Contains(out, "package a") {
		t.Errorf("finished member's result must stay hidden by default:\n%s", out)
	}
	m.expandAll = true
	out = plain(func() string { s, _ := m.renderMessage(m.msgs[1], 1, 0); return s }())
	if !strings.Contains(out, "package a") {
		t.Errorf("details view should reveal the finished member's result:\n%s", out)
	}
	m.expandAll = false

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
	// Calm default: both beats stay hidden.
	if strings.Contains(out, "┊") || strings.Contains(out, "plan sentence") {
		t.Errorf("reasoning beats must stay hidden by default:\n%s", out)
	}
	// ^E: each beat rails and the meta numbers the cycles.
	m.expandAll = true
	out = plain(func() string { s, _ := m.renderMessage(m.msgs[0], 0, 0); return s }())
	if strings.Count(out, "┊") < 2 {
		t.Errorf("each thinking block should rail:\n%s", out)
	}
	if !strings.Contains(out, "beat 1/2") || !strings.Contains(out, "beat 2/2") {
		t.Errorf("multi-block turn should number each think cycle:\n%s", out)
	}
	m.expandAll = false
}

func TestThinkingDurationFreezesWhenToolStarts(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "thinking", Content: "I will read the file."})
	if m.msgs[0].items[0].dur != 0 {
		t.Fatal("open thinking must not stamp dur yet")
	}
	m.msgs[0].items[0].started = time.Now().Add(-2 * time.Second)
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"x"}`})
	sealed := m.msgs[0].items[0].dur
	if sealed < 2*time.Second {
		t.Fatalf("tool_call should seal thinking dur, got %v", sealed)
	}
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "ok"})
	if got := thinkingDur(m.msgs[0].items[0], true); got != sealed {
		t.Fatalf("thinkingDur still live after the tool: %v, sealed %v", got, sealed)
	}
	if m.msgs[0].items[0].dur != sealed {
		t.Fatalf("sealed dur moved after tool_result: %v → %v", sealed, m.msgs[0].items[0].dur)
	}
}

func TestThinkingDurationFreezesWhenReplyStarts(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true
	m.handleEvent(client.Event{Type: "thinking", Content: "I will answer."})
	m.msgs[0].items[0].started = time.Now().Add(-time.Second)
	m.handleEvent(client.Event{Type: "token", Content: "Here goes."})
	if m.msgs[0].items[0].dur < time.Second {
		t.Fatalf("reply should seal thinking dur, got %v", m.msgs[0].items[0].dur)
	}
	if got := thinkingDur(m.msgs[0].items[0], true); got != m.msgs[0].items[0].dur {
		t.Fatalf("thinkingDur still live after the reply: %v", got)
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
