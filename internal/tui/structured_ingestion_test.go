package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// Retired names remain here deliberately: old sessions must be inspectable
// without restoring tool-specific renderers or normalizing away JSON fields.
func TestRetiredToolsUseGenericLiveAndReplayRendering(t *testing.T) {
	for _, name := range retiredToolNames {
		t.Run(name, func(t *testing.T) {
			args := `{"command":"echo historical","paths":["a.go"],"requests":[{"url":"https://example.test"}]}`
			raw := `{"results":[{"path":"a.go","command":"echo historical","stdout":"old output","exit_code":0,"duration_ms":12,"success":true,"status":200}],"metadata":{"keep":"unknown fields"}}`
			live := newTestModel()
			live.handleEvent(client.Event{Type: "tool_call", Name: name, Data: args})
			live.handleEvent(client.Event{Type: "tool_result", Name: name, Data: raw})
			live.handleEvent(client.Event{Type: "done"})

			call := client.SessionToolCall{ID: "call-1"}
			call.Function.Name, call.Function.Arguments = name, args
			replay := newTestModel()
			replay.replayTranscript([]client.SessionMessage{
				{Role: "assistant", ToolCalls: []client.SessionToolCall{call}},
				{Role: "tool", Name: name, ToolCallID: "call-1", Content: "┌── TOOL RESULT: " + name + "\n" + raw + "\n└── END TOOL RESULT: " + name},
			})
			for _, m := range []*Model{live, replay} {
				if len(m.msgs) != 1 || len(m.msgs[0].steps) != 1 {
					t.Fatalf("unexpected transcript: %#v", m.msgs)
				}
				s := m.msgs[0].steps[0]
				if !s.done || s.result != raw || s.callArgs != args || s.subagent || s.resultCard != nil {
					t.Fatalf("historical step lost generic data: %+v", s)
				}
				if got := stepHeadSuffix(s.name, s.arg, s.result, m.th); got != "" {
					t.Fatalf("retired tool received a typed summary: %q", plain(got))
				}
				// Page the real expanded view through both invocation and JSON result.
				m.msgs[0].steps[0].expanded = true
				m.inspect = &inspectTarget{msgIdx: 0, stepIdx: 0, itemIdx: -1}
				m.invalidateInspect()
				var rendered strings.Builder
				for page := 0; page < 6; page++ {
					out, _, _ := m.renderStep(m.msgs[0].steps[0], false, 0, 0, 0)
					rendered.WriteString(plain(out))
					m.Update(key("pgdown"))
				}
				for _, want := range []string{"invocation · arguments", `"results"`, `"duration_ms"`, `"metadata"`, "unknown fields"} {
					if !strings.Contains(rendered.String(), want) {
						t.Errorf("generic expanded view missing %q: %s", want, rendered.String()[:min(rendered.Len(), 1500)])
					}
				}
			}
		})
	}
}

func TestRetiredToolFallbackRemainsBounded(t *testing.T) {
	for _, name := range retiredToolNames {
		for _, raw := range []string{strings.Repeat("界", 100000), strings.Repeat("line\n", 1000)} {
			got := toolResultPreview(name, raw)
			if len(got) > 128*1024+100 || len(strings.Split(got, "\n")) > 201 || !strings.Contains(got, "…") {
				t.Fatalf("%s fallback is unbounded or lacks an omission marker", name)
			}
		}
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
