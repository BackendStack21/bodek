package client

import (
	"encoding/json"
	"testing"
)

// odek ≥ v1.40 stamps the session frame with system_initiated when the turn
// was started by the wake-on-complete dispatcher; the field is absent on
// operator turns. Event must decode both shapes.
func TestEventDecodeSystemInitiated(t *testing.T) {
	var wake Event
	if err := json.Unmarshal([]byte(`{"type":"session","session_id":"s1","system_initiated":true}`), &wake); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !wake.SystemInitiated {
		t.Error("system_initiated=true did not decode onto Event.SystemInitiated")
	}

	var op Event
	if err := json.Unmarshal([]byte(`{"type":"session","session_id":"s1"}`), &op); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if op.SystemInitiated {
		t.Error("absent system_initiated must decode false (operator turns)")
	}
}

// Wake-surface frames (bg_wake / bg_job) must decode far enough to dispatch
// on Type — the TUI routes them and refetches anything richer over REST.
func TestEventDecodeWakeFrames(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want string
	}{
		{`{"type":"bg_wake","session_id":"s1","t":1756830000000}`, "bg_wake"},
		{`{"type":"bg_job","job_id":"bg_ab12","session_id":"s1","status":"exited"}`, "bg_job"},
	} {
		var ev Event
		if err := json.Unmarshal([]byte(tc.wire), &ev); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.wire, err)
		}
		if ev.Type != tc.want {
			t.Errorf("Type = %q, want %q", ev.Type, tc.want)
		}
	}
}
