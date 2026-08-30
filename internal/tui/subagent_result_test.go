package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestParseAgentResult pins the tolerant framed-result parse: the envelope
// must carry status AND summary; prose never parses; a trailing artifact
// block after the JSON object is tolerated.
func TestParseAgentResult(t *testing.T) {
	r := parseAgentResult(`{"status":"success","summary":"Built user handlers.","files_changed":["a.go","b.go"],"tokens_used":4200,"iterations":5}`)
	if r == nil {
		t.Fatal("valid envelope did not parse")
	}
	if r.status != "success" || r.summary != "Built user handlers." || len(r.files) != 2 || r.tokens != 4200 || r.iters != 5 {
		t.Fatalf("parsed fields wrong: %+v", r)
	}

	if parseAgentResult(`no json here`) != nil {
		t.Error("prose parsed as a result card")
	}
	if parseAgentResult(`{"files_changed":[]}`) != nil {
		t.Error("envelope without status/summary parsed")
	}

	// JSON object followed by trailing artifact metadata lines.
	r = parseAgentResult(`{"status":"error","summary":"boom","files_changed":null,"tokens_used":0,"iterations":0}` + "\n" + `📎 report.md · text/markdown · 48 KiB · a1b2c3d4`)
	if r == nil || r.status != "error" || r.summary != "boom" {
		t.Fatalf("envelope with trailing lines parsed wrong: %+v", r)
	}
}

// TestAgentResultCard: a framed delegate result renders as a structured
// card; a prose result keeps the generic preview.
func TestAgentResultCard(t *testing.T) {
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "subagent_state", TaskID: "t1", TaskIdx: 0, Phase: "finished", Status: "success", TokensUsed: 3200})
	m.handleEvent(client.Event{Type: "tool_result", Name: "delegate_tasks", Data: `{"status":"success","summary":"Built handlers and routing. 3 tests added.","files_changed":["handlers/user.go","routes.go"],"tokens_used":13100,"iterations":7}`})
	s := stateStep(t, m)
	if s.resultCard == nil {
		t.Fatal("framed result did not attach a result card")
	}
	if s.resultCard.status != "success" || len(s.resultCard.files) != 2 {
		t.Fatalf("result card fields wrong: %+v", s.resultCard)
	}
	s.expanded = true
	out, _ := renderStepsForTest(m, m.msgs[0], 0, 0)
	for _, want := range []string{"Built handlers and routing.", "handlers/user.go", "2 files", "13.1k tok"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered card missing %q", want)
		}
	}

	// Prose result: no card, generic preview still lands.
	m2 := stateFixture(t)
	m2.handleEvent(client.Event{Type: "tool_result", Name: "delegate_tasks", Data: "plain text output\nline two"})
	s2 := stateStep(t, m2)
	if s2.resultCard != nil {
		t.Fatal("prose result produced a card")
	}
	if s2.result == "" {
		t.Error("prose result lost the generic preview")
	}
}

// TestAgentResultSanitize: every wire-derived string is sanitized before it
// reaches a card line.
func TestAgentResultSanitize(t *testing.T) {
	r := parseAgentResult("{\"status\":\"success\",\"summary\":\"bad \\u001b[31mtext\\nleak\",\"files_changed\":[\"x\\u001b.go\"],\"tokens_used\":10,\"iterations\":1}")
	if r == nil {
		t.Fatal("envelope did not parse")
	}
	m := stateFixture(t)
	m.handleEvent(client.Event{Type: "tool_result", Name: "delegate_tasks", Data: `{"status":"success","summary":"ok","files_changed":["a.go"],"tokens_used":10,"iterations":1}`})
	s := stateStep(t, m)
	lines := agentResultLines(m, s.resultCard, 200)
	joined := strings.Join(lines, "\n")
	if strings.ContainsAny(joined, "\x1b") {
		t.Errorf("card lines carry escape bytes: %q", joined)
	}
}
