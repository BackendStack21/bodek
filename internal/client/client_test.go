package client

import (
	"encoding/json"
	"testing"
)

// TestDecodeEvents verifies that representative odek serve frames decode into
// the union Event with the right fields populated.
func TestDecodeEvents(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		check func(t *testing.T, e Event)
	}{
		{
			name:  "session",
			frame: `{"type":"session","session_id":"20260618-abc","model":"deepseek-v4-flash","sandbox":true}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "session" || e.SessionID != "20260618-abc" || e.Model != "deepseek-v4-flash" || !e.Sandbox {
					t.Fatalf("bad session decode: %+v", e)
				}
			},
		},
		{
			name:  "token",
			frame: `{"type":"token","content":"hello "}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "token" || e.Content != "hello " {
					t.Fatalf("bad token decode: %+v", e)
				}
			},
		},
		{
			name:  "tool_call",
			frame: `{"type":"tool_call","name":"shell","data":"{\"command\":\"ls\"}"}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "tool_call" || e.Name != "shell" || e.Data == "" {
					t.Fatalf("bad tool_call decode: %+v", e)
				}
			},
		},
		{
			name:  "done",
			frame: `{"type":"done","latency":4.2,"sessionContextTokens":1200,"sessionOutputTokens":340}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "done" || e.Latency != 4.2 || e.SessionContextTokens != 1200 || e.SessionOutputTokens != 340 {
					t.Fatalf("bad done decode: %+v", e)
				}
			},
		},
		{
			name:  "approval_request",
			frame: `{"type":"approval_request","id":"apr-1","risk":"network_egress","command":"curl x","description":"fetch","allow_trust":true}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "approval_request" || e.ID != "apr-1" || e.Risk != "network_egress" || !e.AllowTrust {
					t.Fatalf("bad approval decode: %+v", e)
				}
			},
		},
		{
			name:  "memory_event",
			frame: `{"type":"memory_event","event":"merge","target":"user","count":3}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "memory_event" || e.SubType != "merge" || e.Target != "user" || e.Count != 3 {
					t.Fatalf("bad memory_event decode: %+v", e)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e Event
			if err := json.Unmarshal([]byte(tc.frame), &e); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tc.check(t, e)
		})
	}
}
