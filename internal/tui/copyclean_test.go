package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
	// The answer may carry the surface card's single padding column (the
	// card pads lines to full width) — no other leading decoration allowed.
	line := strings.TrimSpace(lines[ansIdx])
	if line != "The answer." || strings.IndexFunc(lines[ansIdx], func(r rune) bool { return r != ' ' }) > 2 {
		t.Errorf("answer carries more than the card padding: %q", lines[ansIdx])
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

// TestAnswerCard pins the surface-card treatment: the assistant's FINAL
// RESPONSE paints an EMBER surface background — the deliverable of the turn,
// visually separated from the work above it — for every theme except
// high-contrast (pure text). User turns stay plain, and the fill never
// leaks into copied text.
func TestAnswerCard(t *testing.T) {
	// Assert what each theme PAINTS, not style internals: force TrueColor
	// for the check and restore the harness profile after.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	for name, wantESC := range map[string]string{
		"ember-dark":    "[48;2;16;19;26m",    // #10131A
		"ember-light":   "[48;2;239;236;229m", // #EFECE5
		"classic":       "[48;2;22;22;30m",    // #16161E
		"high-contrast": "",
	} {
		var pal palette
		switch name {
		case "ember-dark":
			pal = emberDark
		case "ember-light":
			pal = emberLight
		case "classic":
			pal = classic
		default:
			pal = emberHighContrast
		}
		out := themeFrom(pal).answerCard.Render("x")
		if wantESC == "" {
			if strings.Contains(out, "[48;") {
				t.Errorf("%s painted a surface: %q", name, out)
			}
			continue
		}
		if !strings.Contains(out, wantESC) {
			t.Errorf("%s surface escape %q missing: %q", name, wantESC, out)
		}
	}

	// The answer renders inside the card and keeps the text copy-clean;
	// user turns stay plain (no surface).
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := newTestModel()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "fix the login bug"},
		message{role: roleAsst, content: "**Fixed.** The cookie was stale.",
			rendered: "Fixed. The cookie was stale.",
			items:    []turnItem{{thinking: true, text: "hmm"}}},
	)
	userOut, _ := m.renderMessage(m.msgs[0], 0, 0)
	if strings.Contains(userOut, "[48;") {
		t.Error("user turn painted a surface — only the answer carries one")
	}
	asstOut, _ := m.renderMessage(m.msgs[1], 1, 0)
	if !strings.Contains(asstOut, "[48;2;16;19;26m") {
		t.Errorf("answer card surface missing:\n%q", asstOut)
	}
	if !strings.Contains(plain(asstOut), "Fixed. The cookie was stale.") {
		t.Errorf("answer card lost text:\n%s", plain(asstOut))
	}
	// The dimmed work section stays outside the card.
	if strings.Contains(plain(asstOut), "hmm") == false {
		t.Errorf("work section missing from the turn:\n%s", plain(asstOut))
	}
	lipgloss.SetColorProfile(termenv.Ascii)
}

// TestAnswerTextIsThemeBright pins the answer card's text color: glamour's
// stock dark preset paints body text ANSI-252 gray, which reads dimmed on
// the surface card. Every theme's answer body must paint the palette's
// full-brightness text color instead of the stock gray.
func TestAnswerTextIsThemeBright(t *testing.T) {
	for name, wantSGR := range map[string]string{
		"ember-dark":    "38;2;231;233;238", // #E7E9EE
		"ember-light":   "38;2;34;36;44",    // #22252C (glamour quantizes g−1)
		"classic":       "38;2;229;231;235", // #E5E7EB
		"high-contrast": "38;2;255;255;255", // #FFFFFF
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BODEK_THEME", name)
			r, err := glamour.NewTermRenderer(
				glamour.WithStyles(answerGlamourStyle()),
				glamour.WithWordWrap(80),
			)
			if err != nil {
				t.Fatal(err)
			}
			out, err := r.Render("plain answer body")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, wantSGR) {
				t.Errorf("%s answer body missing text color %q:\n%q", name, wantSGR, out)
			}
			if strings.Contains(out, "38;5;252") {
				t.Errorf("%s answer body still painted stock glamour gray 252:\n%q", name, out)
			}
		})
	}
}
