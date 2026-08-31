package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// Tests for plan tool_call transcript specialization: semantic one-liners
// replace the JSON preview, hostile model-authored
// text is sanitized/truncated, and anything unparseable falls back to the
// generic argPreview path.

func TestPlanArgSummary_Verbs(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		want string // prefix "has:" → substring match, else exact
	}{
		{"create", `{"verb":"create","steps":[{"id":"a"},{"id":"b"}]}`, "has:create · 2 steps"},
		{"update single", `{"verb":"update","updates":[{"id":"p3","status":"in_progress"}]}`,
			"p3 → in_progress"},
		{"update multi", `{"verb":"update","updates":[{"id":"s1","status":"done"},{"id":"s2","status":"blocked"}]}`,
			"has:s1 → done · s2 → blocked"},
		{"complete", `{"verb":"complete","step_id":"p2"}`, "p2 → done"},
		{"get", `{"verb":"get"}`, "state check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planArgSummary(tc.arg)
			if sub, ok := strings.CutPrefix(tc.want, "has:"); ok {
				if !strings.Contains(got, sub) {
					t.Fatalf("planArgSummary(%s) = %q, want contains %q", tc.arg, got, sub)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("planArgSummary(%s) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

func TestPlanArgSummary_Fallbacks(t *testing.T) {
	for _, arg := range []string{
		``,                    // empty
		`not json at all`,     // unparseable
		`{"verb":"exotic"}`,   // unknown verb — future engines stay generic
		`{"verb":"update"}`,   // update without usable entries
		`{"verb":"complete"}`, // complete without step_id
		`[1,2,3]`,             // wrong shape entirely
	} {
		if got := planArgSummary(arg); got != "" {
			t.Errorf("planArgSummary(%q) = %q, want empty fallback", arg, got)
		}
	}
}

func TestPlanArgSummary_HostileText(t *testing.T) {
	// sanitize()'s threat model is the terminal: control/escape bytes must
	// never survive into stored previews. Angle brackets are inert glyphs
	// here (steps render as styled text, never markdown) and may remain.
	got := planArgSummary(`{"verb":"complete","step_id":"\u001b[31mevil\u001b[0m<s>"}`)
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("escape byte survived sanitization: %q", got)
	}
	if !strings.Contains(got, "<s>") {
		t.Fatalf("expected sanitized-but-complete id, got %q", got)
	}
	wide := planArgSummary(`{"verb":"create","steps":[` +
		strings.Repeat(`{"id":"s"},`, 20) + `{"id":"z"}]}`)
	if len([]rune(wide)) > 200 { // far above the 72-cap would mean a bypass
		t.Fatalf("summary runaway length: %d runes", len([]rune(wide)))
	}
}

func TestPlanToolCall_StoresSemanticPreview(t *testing.T) {
	m := &Model{}
	m.msgs = append(m.msgs, message{}) // open assistant turn so cur() >= 0
	m.handleEvent(client.Event{
		Type: "tool_call", Name: "plan",
		Data: `{"verb":"update","updates":[{"id":"p4","status":"blocked"}]}`,
	})

	i := m.cur()
	if i < 0 || len(m.msgs[i].steps) != 1 {
		t.Fatalf("step not ingested: msgs=%d cur=%d", len(m.msgs), i)
	}
	step := m.msgs[i].steps[0]
	if step.arg != "p4 → blocked" {
		t.Fatalf("stored preview = %q, want semantic one-liner", step.arg)
	}
	if m.lastArg != "p4 → blocked" {
		t.Fatalf("lastArg = %q, want the same summary", m.lastArg)
	}
	// Non-plan tools keep the generic preview untouched.
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"ls -la"}`})
	if got := m.msgs[i].steps[1].arg; got != "ls -la" {
		t.Fatalf("generic preview changed: %q", got)
	}
}
