package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestThinkReplyPairsRenderIndependently pins the segmented-turn contract: a
// long agentic turn with several think→say→tool cycles keeps every cycle on
// the timeline and renders each reasoning block with its own response card,
// in arrival order — instead of merging all prose into one trailing card.
func TestThinkReplyPairsRenderIndependently(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "scan the repo"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true

	feed := []client.Event{
		{Type: "thinking", Content: "first thought"},
		{Type: "token", Content: "Starting the scan."},
		{Type: "tool_call", Name: "search_files", Data: `{"pattern":"TODO"}`},
		{Type: "tool_result", Name: "search_files", Data: "main.go:1"},
		{Type: "thinking", Content: "second thought"},
		{Type: "token", Content: "Found one TODO."},
	}
	for _, ev := range feed {
		m.handleEvent(ev)
	}

	msg := &m.msgs[1]
	want := []struct {
		think bool
		reply bool
		text  string
	}{
		{true, false, "first thought"},
		{false, true, "Starting the scan."},
		{false, false, ""}, // tool step
		{true, false, "second thought"},
		{false, true, "Found one TODO."},
	}
	if len(msg.items) != len(want) {
		t.Fatalf("timeline = %+v", msg.items)
	}
	for i, w := range want {
		it := msg.items[i]
		switch {
		case w.think && (!it.thinking || it.text != w.text),
			w.reply && (!it.reply || it.text != w.text),
			!w.think && !w.reply && it.thinking:
			t.Errorf("item %d = %+v, want %+v", i, it, w)
		}
	}

	// Calm default: thoughts stay hidden while cards and work render.
	rendered, _ := m.renderMessage(*msg, 1, 0)
	out := plain(rendered)
	if strings.Contains(out, "first thought") || strings.Contains(out, "second thought") {
		t.Errorf("reasoning must stay hidden by default:\n%s", out)
	}

	// Details view keeps the pairs: thought → its card → work → next
	// thought → its card.
	m.expandAll = true
	rendered, _ = m.renderMessage(*msg, 1, 0)
	m.expandAll = false
	out = plain(rendered)
	first := strings.Index(out, "first thought")
	card1 := strings.Index(out, "Starting the scan.")
	work := strings.Index(out, "search_files")
	second := strings.Index(out, "second thought")
	card2 := strings.Index(out, "Found one TODO.")
	for _, idx := range []int{first, card1, work, second, card2} {
		if idx < 0 {
			t.Fatalf("missing segment in:\n%s", out)
		}
	}
	if first >= card1 || card1 >= work || work >= second || second >= card2 {
		t.Errorf("think→reply pairs not interleaved chronologically:\n%s", out)
	}

	// The compat blob joins distinct cycles with a blank line.
	m.handleEvent(client.Event{Type: "done"})
	if got := m.msgs[1].content; got != "Starting the scan.\n\nFound one TODO." {
		t.Errorf("content blob = %q", got)
	}

	// Finalized: every reply segment carries its own glamour cache.
	for i, it := range m.msgs[1].items {
		if it.reply && it.rendered == "" {
			t.Errorf("finalized reply %d has no cached render", i)
		}
	}
	rendered, _ = m.renderMessage(m.msgs[1], 1, 0)
	if out := plain(rendered); !strings.Contains(out, "Found one TODO.") {
		t.Errorf("finalized render lost the last reply:\n%s", out)
	}
}

// TestTurnMarkerJoinsLastReply verifies cancel / interrupt / error markers
// land on the FINAL reply segment — attached to the last answer card — while
// the raw blob keeps the legacy "previous\n\nmarker" shape. A turn with no
// prose at all carries the marker as its only reply segment.
func TestTurnMarkerJoinsLastReply(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = 0
	m.busy = true

	m.handleEvent(client.Event{Type: "thinking", Content: "hmm"})
	m.handleEvent(client.Event{Type: "token", Content: "partial answer"})
	m.handleEvent(client.Event{Type: "cancelled"})
	m.handleEvent(client.Event{Type: "error", Message: "context canceled"})

	msg := &m.msgs[0]
	var replies []turnItem
	for _, it := range msg.items {
		if it.reply {
			replies = append(replies, it)
		}
	}
	if len(replies) != 1 || !strings.HasSuffix(replies[0].text, "\n\n**Cancelled.**") {
		t.Fatalf("marker not attached to the open reply: %+v", replies)
	}
	if got := msg.content; got != "partial answer\n\n**Cancelled.**" {
		t.Errorf("content blob = %q", got)
	}
	rendered, _ := m.renderMessage(*msg, 0, 0)
	out := plain(rendered)
	if strings.Index(out, "partial answer") > strings.Index(out, "Cancelled.") {
		t.Errorf("marker should render at the end of the final card:\n%s", out)
	}

	// No prose yet: the marker stands alone as the turn's only segment.
	m2 := newTestModel()
	m2.msgs = append(m2.msgs, message{role: roleAsst, streaming: true})
	m2.curIdx = 0
	setTurnMarker(&m2.msgs[0], "**Error:** boom")
	if n := len(m2.msgs[0].items); n != 1 || m2.msgs[0].items[0].text != "**Error:** boom" {
		t.Fatalf("marker-only turn timeline = %+v", m2.msgs[0].items)
	}
	if got := m2.msgs[0].content; got != "**Error:** boom" {
		t.Errorf("marker-only blob = %q", got)
	}
}

// TestStepRefsTrackInterleavedCards verifies mouse hit-testing stays exact
// when answer cards interleave with work items: each recorded ref line must
// be the step's own header row in the assembled transcript.
func TestStepRefsTrackInterleavedCards(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst,
		content: "halfway report",
		items: []turnItem{
			{thinking: true, text: "plan"},
			{stepIdx: 0},
			{reply: true, text: "halfway report"},
			{stepIdx: 1},
		},
		steps: []step{
			{name: "read_file", done: true, result: "ok"},
			{name: "shell", done: true, result: "done"},
		},
	})
	_ = m.conversation()
	if len(m.stepLineIndex) != 2 {
		t.Fatalf("expected 2 step refs, got %+v", m.stepLineIndex)
	}
	lines := strings.Split(plain(m.conversation()), "\n")
	for i, ref := range m.stepLineIndex {
		want := []string{"read_file", "shell"}[i]
		actual := -1
		for j, ln := range lines {
			if strings.Contains(ln, want) && strings.Contains(ln, "▶") {
				actual = j
				break
			}
		}
		if actual < 0 {
			t.Fatalf("step %s header not found in transcript", want)
		}
		if ref.line != actual {
			t.Errorf("step %d (%s): ref points at line %d, header actually at %d",
				i, want, ref.line, actual)
		}
	}
}

// TestResidualBlobNeverLost covers the hybrid shape a hand-built message can
// reach (blob text set before any event): the timeline cards must render AND
// the pre-existing blob must not vanish behind them.
func TestResidualBlobNeverLost(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "preset answer",
		streaming: true})
	m.curIdx = 0

	m.handleEvent(client.Event{Type: "token", Content: " streamed tail"})
	m.handleEvent(client.Event{Type: "done"})

	out := plain(m.conversation())
	for _, want := range []string{"preset answer", "streamed tail"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered transcript lost %q:\n%s", want, out)
		}
	}
}
