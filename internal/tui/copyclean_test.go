package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestTranscriptIsCopyClean pins the no-bars rule: text copied out of the
// transcript must be paste-clean, so no turn structure may draw vertical
// rule characters alongside user, assistant, or note content.
func TestTranscriptIsCopyClean(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "fix the login bug"},
		message{role: roleAsst, content: "**Fixed.** The cookie was stale.",
			items: []turnItem{
				{thinking: true, text: "let me look at the code"},
				{stepIdx: 0},
			},
			steps: []step{{name: "patch", arg: "auth.go", done: true, result: "ok"}},
			stats: &turnStats{latency: 2, ctxTok: 900, outTok: 120, toolCount: 1}},
		message{role: roleNote, content: "system notice"},
	)
	m.refresh()
	out := plain(m.conversation())
	for _, bar := range []string{"┃", "▎", "▌", "┆", "║"} {
		if strings.ContainsRune(out, []rune(bar)[0]) {
			t.Errorf("transcript draws vertical bar %q — copied text would not be paste-clean:\n%s", bar, out)
		}
	}
}

// TestAnswerSeparatedFromReasoning verifies the final response renders as
// its own block: after the dimmed reasoning and tool work, separated by a
// blank line, starting at column zero — full brightness, copy-clean.
func TestAnswerSeparatedFromReasoning(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, content: "The answer.",
		items: []turnItem{
			{thinking: true, text: "hmm, thinking about it"},
			{stepIdx: 0},
		},
		steps: []step{{name: "shell", arg: "ls", done: true, result: "file.go"}},
	})
	rendered, _ := m.renderMessage(m.msgs[0], 0, 0)
	lines := strings.Split(plain(rendered), "\n")

	thinkIdx, stepIdx, ansIdx := -1, -1, -1
	for i, ln := range lines {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "…") && strings.Contains(trim, "hmm") {
			thinkIdx = i
		}
		if strings.Contains(trim, "shell") && stepIdx < 0 {
			stepIdx = i
		}
		if trim == "The answer." {
			ansIdx = i
		}
	}
	if thinkIdx < 0 || stepIdx < 0 || ansIdx < 0 {
		t.Fatalf("missing sections in:\n%s", plain(rendered))
	}
	if thinkIdx >= stepIdx || stepIdx >= ansIdx {
		t.Fatalf("reasoning, steps, and answer out of order:\n%s", plain(rendered))
	}
	// The blank line separates the work section from the answer, and the
	// answer itself carries no leading decoration (column zero).
	if strings.TrimSpace(lines[ansIdx-1]) != "" {
		t.Errorf("answer not preceded by a blank line:\n%q", lines[ansIdx-1])
	}
	if lines[ansIdx] != "The answer." {
		t.Errorf("answer not at column zero: %q", lines[ansIdx])
	}
	// Live streaming turns keep the same separation.
	m2 := newTestModel()
	streamingTurn(m2)
	m2.handleEvent(client.Event{Type: "thinking", Content: "live thought"})
	m2.handleEvent(client.Event{Type: "token", Content: "live answer"})
	out := plain(m2.conversation())
	for _, want := range []string{"live thought", "live answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("streamed turn missing %q:\n%s", want, out)
		}
	}
	if think, ans := strings.Index(out, "live thought"), strings.Index(out, "live answer"); think > ans {
		t.Errorf("streamed answer rendered before reasoning:\n%s", out)
	}
}
