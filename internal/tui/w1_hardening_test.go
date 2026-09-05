package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/charmbracelet/lipgloss"
)

// W1 hardening — sanitize coverage, width-discipline, and wire-field hygiene.
// Regression tests for the judge-5 robustness audit (see .ux-review/).

func TestSanitizeStripsInvisibleClasses(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"c0 escape", "\x1b[31mred", "[31mred"}, // introducer stripped — remnants are inert text
		{"c1 csi", "\u009b31mred", "31mred"},
		{"bidi override", "safe\u202Efoo", "safefoo"},
		{"zero-width family", "a\u200Bb\u2060c\uFEFFd\u00ADe", "abcde"},
		{"line separators", "l\u2028x\u2029y", "lxy"},
		{"tab kept for copy fidelity", "a\tb", "a\tb"},
		{"newline kept", "a\nb", "a\nb"},
		{"cjk and emoji survive", "中文 ✅", "中文 ✅"},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("%s: sanitize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestTruncateRespectsColumnBudget(t *testing.T) {
	cjk := strings.Repeat("中", 20) // 40 display columns, 20 runes
	if got := truncate(cjk, 10); lipgloss.Width(got) > 10 {
		t.Errorf("truncate(20 CJK, 10) = %d cols, want <= 10", lipgloss.Width(got))
	}
	if got := truncate(cjk, 10); !strings.HasSuffix(got, "…") {
		t.Errorf("truncate should mark the cut with an ellipsis, got %q", got)
	}
	zwj := strings.Repeat("🧑\u200d💻", 3) // ZWJ sequences: 6 cols each
	if got := truncate(zwj, 8); lipgloss.Width(got) > 8 {
		t.Errorf("truncate(ZWJ emoji, 8) = %d cols, want <= 8", lipgloss.Width(got))
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("ascii parity: truncate = %q, want %q", got, "hell…")
	}
	if got := truncate("hello", 1); got != "…" {
		t.Errorf("n=1: truncate = %q, want %q", got, "…")
	}
	if got := truncate("hello", 0); got != "" {
		t.Errorf("n=0: truncate = %q, want empty", got)
	}
	if got := truncate("hi", 10); got != "hi" {
		t.Errorf("short string must pass through, got %q", got)
	}
}

func TestToolNameWireSanitized(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	m.handleEvent(client.Event{Type: "tool_call", Name: "\x1b]0;pwned\x07read_file", Data: "{}"})
	i := m.cur()
	if i < 0 || len(m.msgs[i].steps) == 0 {
		t.Fatal("expected a step to be recorded")
	}
	name := m.msgs[i].steps[len(m.msgs[i].steps)-1].name
	if strings.ContainsRune(name, '\x1b') || strings.ContainsRune(name, '\x07') {
		t.Errorf("step name still carries escape bytes: %q", name)
	}
	if strings.ContainsRune(m.status, '\x1b') {
		t.Errorf("status line still carries escape bytes: %q", m.status)
	}
	if strings.ContainsRune(m.lastTool, '\x1b') {
		t.Errorf("lastTool still carries escape bytes: %q", m.lastTool)
	}
}

// tool_call stores collapse(name); tool_result must compare the same way
// or a padded/hostile wire name never completes the step.
func TestToolResultMatchesCollapsedName(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: "tool_call", Name: "  shell  ", Data: `{"command":"ls"}`})
	if len(m.msgs[1].steps) != 1 || m.msgs[1].steps[0].name != "shell" {
		t.Fatalf("collapsed step name = %+v", m.msgs[1].steps)
	}
	m.handleEvent(client.Event{Type: "tool_result", Name: "  shell  ", Data: "ok"})
	if !m.msgs[1].steps[0].done {
		t.Fatal("tool_result did not complete the step — name match ignored collapse()")
	}
}

func TestErrorMarkerSanitized(t *testing.T) {
	m := newTestModel()
	m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
	m.curIdx = len(m.msgs) - 1
	idx := m.curIdx
	m.handleEvent(client.Event{Type: "error", Message: "boom \x1b[2J wiped"})
	if idx < 0 || idx >= len(m.msgs) {
		t.Fatal("no message recorded")
	}
	// finalize() may reset curIdx — inspect the captured index.
	if c := m.msgs[idx].content; strings.ContainsRune(c, '\x1b') {
		t.Errorf("error marker carries escape bytes: %q", c)
	}
}

func TestSubmitSanitizesPastedContent(t *testing.T) {
	m := newTestModel()
	m.busy = false
	m.sendPrompt("\x1b[31mevil pasted")
	found := false
	for _, msg := range m.msgs {
		if msg.role == roleUser {
			found = true
			if strings.ContainsRune(msg.content, '\x1b') {
				t.Errorf("submitted prompt carries escape bytes: %q", msg.content)
			}
			if msg.content != "[31mevil pasted" { // ESC stripped — printable remnants are inert text
				t.Errorf("submitted prompt = %q, want %q", msg.content, "[31mevil pasted")
			}
		}
	}
	if !found {
		t.Fatal("no user message recorded")
	}
}

func TestSessionInfoModelCollapsed(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "session", Model: "\x1b]0;evil\x07glm-x"})
	if m.model != "]0;evilglm-x" { // ESC/BEL stripped — printable remnants are inert
		t.Errorf("m.model = %q, want %q", m.model, "]0;evilglm-x")
	}
}

// The header paints m.model raw. A poisoned id (resume, picker, /model paste)
// must not inject CSI into the bar — even if an assignment site missed collapse.
func TestHeaderSanitizesModelName(t *testing.T) {
	m := newTestModel()
	m.model = "x\x1b[2Jevil"
	if strings.Contains(m.header(), "\x1b[2J") {
		t.Errorf("header leaked CSI from model id: %q", m.header())
	}
}

// /model, the picker, the palette, and session resume all write m.model
// without collapse — unlike the session/server_info frames.
func TestModelSourcesCollapseEscapes(t *testing.T) {
	const evil = "ok\x1b[2Jevil"

	t.Run("slash-model", func(t *testing.T) {
		m := newTestModel()
		m.runCommand("model", evil)
		if strings.ContainsRune(m.model, '\x1b') || strings.ContainsRune(m.pendModel, '\x1b') {
			t.Errorf("/model stored escapes: model=%q pend=%q", m.model, m.pendModel)
		}
	})

	t.Run("picker", func(t *testing.T) {
		m := newTestModel()
		m.models = []client.ModelInfo{{ID: evil}}
		m.panel = panelModels
		m.panelSelect()
		if strings.ContainsRune(m.model, '\x1b') || strings.ContainsRune(m.pendModel, '\x1b') {
			t.Errorf("picker stored escapes: model=%q pend=%q", m.model, m.pendModel)
		}
	})

	t.Run("palette", func(t *testing.T) {
		m := newTestModel()
		m.models = []client.ModelInfo{{ID: evil}}
		m.togglePalette()
		var ran bool
		for _, e := range m.pal.all {
			if e.kind != "model" {
				continue
			}
			e.run(m)
			ran = true
			break
		}
		if !ran {
			t.Fatal("palette had no model entry")
		}
		if strings.ContainsRune(m.model, '\x1b') || strings.ContainsRune(m.pendModel, '\x1b') {
			t.Errorf("palette stored escapes: model=%q pend=%q", m.model, m.pendModel)
		}
	})

	t.Run("resume", func(t *testing.T) {
		m := newTestModel()
		m.handleSessionDetail(sessionDetailMsg{
			sess:  client.Session{ID: "s1", Model: evil},
			token: "tok",
		})
		if strings.ContainsRune(m.model, '\x1b') {
			t.Errorf("resume stored escapes: model=%q", m.model)
		}
	})
}

func TestSkillSuggestFieldsCollapsed(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested",
		SkillName: "\x1b[31mevil-skill", Detail: "\x1b[31mmulti\nline\tdetail"})
	if m.skillSuggest == nil {
		t.Fatal("suggestion not stored")
	}
	if strings.ContainsAny(m.skillSuggest.SkillName, "\x1b\t\n") {
		t.Errorf("SkillName not collapsed: %q", m.skillSuggest.SkillName)
	}
	if strings.ContainsAny(m.skillSuggest.Detail, "\x1b\t\n") {
		t.Errorf("Detail not collapsed: %q", m.skillSuggest.Detail)
	}
}

// @-reference rows arrive from /api/resources. CSI in Label/Detail must
// not reach the popup; CSI in ID must not land in the composer.
func TestResourceCompletionSanitizesWireFields(t *testing.T) {
	m := newTestModel()
	m.ac.open = true
	m.ac.mode = acRef
	m.ac.items = []client.Resource{{
		ID:     "@f\x1b[2Jevil",
		Type:   "file",
		Label:  "x\x1b[2Jy",
		Detail: "z\x1b]0;p\x07",
	}}
	if pop := m.acPopup(); strings.Contains(pop, "\x1b[2J") || strings.Contains(pop, "\x1b]0;") {
		t.Errorf("ac popup leaked CSI from wire fields: %q", pop)
	}

	m.ta.SetValue("@f")
	m.acceptCompletion()
	if strings.ContainsRune(m.ta.Value(), '\x1b') {
		t.Errorf("acceptCompletion inserted escapes: %q", m.ta.Value())
	}
}

// Engine notices keep sanitize() (CSI gone, newlines kept). A wire
// SkillName/Target/Detail with a newline paints a second strip row.
func TestEngineNoticesCollapseNewlines(t *testing.T) {
	cases := []struct {
		name string
		ev   client.Event
	}{
		{"skill", client.Event{Type: "skill_event", SubType: "suggested", SkillName: "x\n● disconnected"}},
		{"memory", client.Event{Type: "memory_event", SubType: "stored", Target: "x\n● disconnected"}},
		{"signal", client.Event{Type: "agent_signal", SubType: "info", Detail: "x\n● disconnected"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestModel()
			m.handleEvent(c.ev)
			if len(m.notices) != 1 {
				t.Fatalf("notices = %d, want 1: %v", len(m.notices), m.notices)
			}
			if strings.Contains(m.notices[0], "\n") {
				t.Errorf("notice kept a newline: %q", m.notices[0])
			}
		})
	}
}

// The conversation is the click/scroll hit-test source of truth: a line that
// physically wraps desyncs every index below it. Every emitted line must fit
// the viewport.
func TestConversationLinesNeverExceedViewport(t *testing.T) {
	m := newTestModel()
	m.width = 40
	m.height = 30
	m.vp.Width = 40
	m.vp.Height = 20
	m.msgs = append(m.msgs,
		message{role: roleUser, content: strings.Repeat("中", 60)},
		message{role: roleAsst, content: strings.Repeat("中", 200)},
	)
	out := m.conversation()
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > m.vp.Width {
			t.Fatalf("conversation line %d is %d cols, viewport %d: %q", i, w, m.vp.Width, line)
		}
	}
}
