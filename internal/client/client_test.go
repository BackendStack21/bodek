package client

import (
	"encoding/json"
	"testing"
)

func TestEventBufferHoldsAThinkingTurn(t *testing.T) {
	if eventBuffer < 4096 {
		t.Errorf("eventBuffer = %d, want at least 4096 so a thinking firehose cannot fill the channel", eventBuffer)
	}
	if deltaCoalesceMax < 8 {
		t.Errorf("deltaCoalesceMax = %d, want enough merging to shrink a token-by-token stream", deltaCoalesceMax)
	}
}

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
			name:  "approval_request friction",
			frame: `{"type":"approval_request","id":"apr-2","risk":"shell_exec","command":"rm -rf x","friction":true,"friction_approvals":4}`,
			check: func(t *testing.T, e Event) {
				if !e.Friction || e.FrictionApprovals != 4 || e.AllowTrust {
					t.Fatalf("bad friction decode: %+v", e)
				}
			},
		},
		{
			name:  "approval_ack",
			frame: `{"type":"approval_ack","id":"apr-1","action":"approve"}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "approval_ack" || e.Action != "approve" {
					t.Fatalf("bad approval_ack decode: %+v", e)
				}
			},
		},
		{
			name:  "token_delta",
			frame: `{"type":"token_delta","content":"hel"}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "token_delta" || e.Content != "hel" {
					t.Fatalf("bad token_delta decode: %+v", e)
				}
			},
		},
		{
			name:  "thinking_delta",
			frame: `{"type":"thinking_delta","content":"hmm"}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "thinking_delta" || e.Content != "hmm" {
					t.Fatalf("bad thinking_delta decode: %+v", e)
				}
			},
		},
		{
			name:  "done with cache metrics",
			frame: `{"type":"done","latency":2.5,"contextTokens":900,"outputTokens":120,"cacheCreationTokens":800,"cacheReadTokens":40,"cachedTokens":10,"sessionContextTokens":900,"sessionOutputTokens":120}`,
			check: func(t *testing.T, e Event) {
				if e.CacheCreationTokens != 800 || e.CacheReadTokens != 40 || e.CachedTokens != 10 {
					t.Fatalf("bad done cache decode: %+v", e)
				}
				if e.ContextTokens != 900 || e.OutputTokens != 120 {
					t.Fatalf("bad done token decode: %+v", e)
				}
			},
		},
		{
			name:  "cancelled",
			frame: `{"type":"cancelled","session_id":"s1","idle":true}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "cancelled" || !e.Idle || e.SessionID != "s1" {
					t.Fatalf("bad cancelled decode: %+v", e)
				}
			},
		},
		{
			name:  "server_info",
			frame: `{"type":"server_info","version":"1.24.0","model":"glm-5.3","sandbox":true,"stream":true,"uptime_seconds":1903,"ws_connections":2}`,
			check: func(t *testing.T, e Event) {
				if e.Version != "1.24.0" || e.Model != "glm-5.3" || !e.Sandbox || !e.Stream ||
					e.UptimeSeconds != 1903 || e.WSConnections != 2 {
					t.Fatalf("bad server_info decode: %+v", e)
				}
			},
		},
		{
			name:  "pong",
			frame: `{"type":"pong","t":1755768000000,"stream":false,"ws_connections":1}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "pong" || e.T == 0 || e.Stream || e.WSConnections != 1 {
					t.Fatalf("bad pong decode: %+v", e)
				}
			},
		},
		{
			name:  "keepalive",
			frame: `{"type":"keepalive","t":1755768000000}`,
			check: func(t *testing.T, e Event) {
				if e.Type != "keepalive" || e.T == 0 {
					t.Fatalf("bad keepalive decode: %+v", e)
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
