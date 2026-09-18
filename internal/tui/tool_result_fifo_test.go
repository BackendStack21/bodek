package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// Regression: tool_result sealed steps with a backward name-scan, so two
// concurrent calls to the SAME tool swapped results — the first result
// sealed the newest step and the second result overwrote the oldest.
// Results arrive in call order (odek serializes parallel results), so
// matching must be FIFO: the first undone step with that name.
func TestToolResultSealsStepsInCallOrder(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"first"}`})
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"second"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "RESULT-FIRST"})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "RESULT-SECOND"})

	i := m.cur()
	if i < 0 {
		t.Fatal("no live message after tool calls")
	}
	steps := m.msgs[i].steps
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if !steps[0].done || !steps[1].done {
		t.Fatalf("both steps should be sealed (done: %v, %v)", steps[0].done, steps[1].done)
	}
	if !strings.Contains(steps[0].result, "RESULT-FIRST") {
		t.Errorf("step 0 result = %q, want RESULT-FIRST (results must seal in call order)", steps[0].result)
	}
	if !strings.Contains(steps[1].result, "RESULT-SECOND") {
		t.Errorf("step 1 result = %q, want RESULT-SECOND", steps[1].result)
	}
}

// Sequential same-name calls still seal the newest unfinished step: after
// the first call finishes, only the second remains undone.
func TestToolResultSealsNewestAfterSequentialReuse(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"one"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "OUT-ONE"})
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"two"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "shell", Data: "OUT-TWO"})

	steps := m.msgs[m.cur()].steps
	if len(steps) != 2 || !steps[0].done || !steps[1].done {
		t.Fatalf("steps = %d done=%v/%v, want 2 sealed", len(steps), steps[0].done, steps[1].done)
	}
	if !strings.Contains(steps[1].result, "OUT-TWO") {
		t.Errorf("second step result = %q, want OUT-TWO", steps[1].result)
	}
}
