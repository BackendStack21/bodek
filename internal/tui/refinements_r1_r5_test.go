package tui

// R1–R5 transcript-signal refinements, written RED-first. Each test pins one
// refinement: generalized verdict chips (build/vet/race), collapsed dot
// tallies, per-child agent state glyphs, chip-style receipts, and the
// last-event age stamp on the streaming head.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── R1: verdict chips for build / vet / lint / race ─────────────────────────

func TestBuildVerdictChips(t *testing.T) {
	th := newTheme()
	cases := []struct {
		name, arg, result, want string
	}{
		{"build pass (silent)", "go build ./...", "", "built"},
		{"build no output marker", "go build ./...", "(no output)", "built"},
		{"build fail", "go build ./...",
			"# github.com/x/y\ny.go:9:2: undefined: Foo\nexit status 1", "build failed"},
		{"build fail rust", "cargo build", "error[E0432]: unresolved import `x`", "build failed"},
		{"build tsc fail", "tsc", "src/a.ts(3,7): error TS2322: Type '1' is not assignable", "build failed"},
		{"build fail exit only", "make build", "some noise\nexit status 2", "build failed"},
		{"no fabricated success", "go build ./...", "0 issues.", ""},
		{"build needs gate", "cat build.log", "y.go:9:2: undefined: Foo", ""},
		{"vet pass", "go vet ./...", "", "vet"},
		{"vet fail", "go vet ./...",
			"# github.com/x/y\ny.go:5:2: Printf call has arguments but no formatting directives", "vet failed"},
		{"vet needs gate", "grep vet notes.txt", "", ""},
	}
	for _, tc := range cases {
		got := plain(stepHeadSuffix("shell", tc.arg, tc.result, th))
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: chip = %q, want none", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: chip %q missing %q", tc.name, got, tc.want)
		}
	}
}

func TestRaceVerdictChips(t *testing.T) {
	th := newTheme()
	if got := plain(stepHeadSuffix("shell", "go test -race ./...",
		"WARNING: DATA RACE\nWrite at 0x00c000 by goroutine 7:", th)); got != "race detected" {
		t.Errorf("race fail chip = %q, want race detected", got)
	}
	if got := plain(stepHeadSuffix("shell", "go test -race ./...",
		"ok  \texample.com/pkg\t1.5s", th)); got != "✓ tests pass · race" {
		t.Errorf("race pass chip = %q, want ✓ tests pass · race", got)
	}
	// Plain test runs stay untouched — the race suffix is flag-gated.
	if got := plain(stepHeadSuffix("shell", "go test ./...",
		"ok  \texample.com/pkg\t1.5s", th)); got != "✓ tests pass" {
		t.Errorf("plain pass chip = %q", got)
	}
}

func TestLintChipVocabulary(t *testing.T) {
	th := newTheme()
	if got := plain(stepHeadSuffix("shell", "golangci-lint run", "0 issues.", th)); got != "✓ lint" {
		t.Errorf("lint clean = %q, want ✓ lint", got)
	}
	if got := plain(stepHeadSuffix("shell", "make lint", "2 issues.", th)); got != "lint 2" {
		t.Errorf("lint issues = %q, want lint 2", got)
	}
}

// ── R2: collapsed dot tallies ───────────────────────────────────────────────

func TestCollapseTallyDots(t *testing.T) {
	m := newTestModel()
	msg := message{
		role: roleAsst, collapsed: true,
		steps: []step{
			{name: "read_file", arg: "a.go", done: true, result: "ok"},
			{name: "read_file", arg: "b.go", done: true, result: "ok"},
			{name: "read_file", arg: "c.go", done: true, result: "boom", isErr: true},
		},
	}
	got := m.collapseSummary(msg)
	if !strings.Contains(got, "3 tool steps · ··✗") {
		t.Errorf("collapsed summary missing dot tally: %q", got)
	}
	// Sanity: all-fine turn shows no ✗.
	ok := message{role: roleAsst, collapsed: true,
		steps: []step{{name: "read_file", arg: "a.go", done: true, result: "ok"}}}
	if !strings.Contains(m.collapseSummary(ok), "1 tool step · ·") {
		t.Errorf("single-step tally missing: %q", m.collapseSummary(ok))
	}
	// Capped width: 40 steps collapse to cap glyphs + ellipsis head, not 40.
	var many []step
	for i := 0; i < 40; i++ {
		many = append(many, step{name: "read_file", arg: "x.go", done: true, result: "ok"})
	}
	tal := stepTally(message{steps: many})
	if w := lipgloss.Width(tal); w > 26 {
		t.Errorf("tally width %d exceeds cap", w)
	}
	if !strings.Contains(tal, "…") {
		t.Errorf("capped tally should carry an ellipsis head: %q", tal)
	}
}

// ── R3: per-child agent state glyphs ────────────────────────────────────────

func TestAgentStateGlyphVocabulary(t *testing.T) {
	cases := []struct {
		card  agentCard
		glyph string
	}{
		{agentCard{phase: "queued", status: "queued"}, "◔"},
		{agentCard{phase: "active", status: "running"}, "▸"},
		{agentCard{phase: "finished", status: "success"}, "✓"},
		{agentCard{phase: "finished", status: "error"}, "✗"},
		{agentCard{phase: "finished", status: "cancelled"}, "✗"},
		{agentCard{phase: "finished", status: "timeout"}, "✗"},
		{agentCard{phase: "active", status: "running", lost: true}, "✗"},
	}
	for _, tc := range cases {
		a := tc.card
		if got := a.glyph(); got != tc.glyph {
			t.Errorf("phase %q status %q glyph = %q, want %q", a.phase, a.status, got, tc.glyph)
		}
	}
	// The chip strip passes the lost card through with its dim marker.
	s := &step{subagent: true, agents: []*agentCard{
		{taskID: "t1", idx: 0, phase: "active", status: "running", lost: true},
	}}
	chips := s.agentChips()
	if len(chips) != 1 || !chips[0].dim || chips[0].glyph != "✗" {
		t.Errorf("lost card chip = %#v, want dim ✗", chips)
	}
}

// ── R4: chip-style turn receipt ─────────────────────────────────────────────

func TestReceiptChips(t *testing.T) {
	r := receipt{files: 2, adds: 3, dels: 1, hasDiff: true, tests: "✓"}
	got := formatReceipt(r)
	if got != "✎ 2 · +3 −1 · ✓ tests" {
		t.Errorf("receipt = %q, want chip form ✎ 2 · +3 −1 · ✓ tests", got)
	}
	if got := formatReceipt(receipt{}); got != "" {
		t.Errorf("empty receipt should render empty, got %q", got)
	}
}

// ── R5: last-event age on the streaming head ────────────────────────────────

func staleFixture() (*Model, func(time.Time)) {
	m := newTestModel()
	m.ta.Focus()
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "hi"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	return m, func(last time.Time) {
		m.runStart = time.Now().Add(-10 * time.Second)
		m.lastEvent = last
		m.handleEvent(client.Event{Type: "thinking", Content: "hmm"})
	}
}

func TestLastEventAgeStaleHead(t *testing.T) {
	m, set := staleFixture()
	set(time.Now().Add(-9 * time.Second))
	rendered, _ := m.renderMessage(m.msgs[1], 1, 0)
	head := strings.Split(plain(rendered), "\n")[0]
	if !strings.Contains(head, "· 9s") {
		t.Errorf("stale head missing last-event age: %q", head)
	}

	// Fresh: no age segment rides the head.
	set(time.Now())
	rendered, _ = m.renderMessage(m.msgs[1], 1, 0)
	head = strings.Split(plain(rendered), "\n")[0]
	if strings.Contains(head, "· ") {
		t.Errorf("fresh head must not carry an age segment: %q", head)
	}

	// Idle: the age never renders when not busy.
	m.busy = false
	m.lastEvent = time.Now().Add(-30 * time.Second)
	rendered, _ = m.renderMessage(m.msgs[1], 1, 0)
	head = strings.Split(plain(rendered), "\n")[0]
	if strings.Contains(head, "· ") {
		t.Errorf("idle head must not carry an age segment: %q", head)
	}
}

func TestLastEventStampsOnBatch(t *testing.T) {
	m, set := staleFixture()
	set(time.Time{})
	if !m.lastEvent.IsZero() {
		t.Fatal("fixture should start with a zero stamp")
	}
	m.ingestWireBatch([]client.Event{{Type: "thinking", Content: "x"}})
	if m.lastEvent.IsZero() {
		t.Error("eventBatchMsg must stamp lastEvent")
	}
	// Under the stale threshold the age stays hidden even when busy.
	if m.staleAge() != "" {
		t.Errorf("fresh stamp must not report an age: %q", m.staleAge())
	}
}
