package client

import (
	"encoding/json"
	"net/http"
	"testing"

	ws "golang.org/x/net/websocket"
)

// TestSubagentsREST pins GET /api/subagents decoding and the ?key= filter.
func TestSubagentsREST(t *testing.T) {
	var gotKey string
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		buf := make([]byte, 512)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}))
	mux.HandleFunc("/api/subagents", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"entries": []map[string]any{
				{
					"task_id": "t1", "run_key": "rk1", "goal": "explore the repo",
					"phase": "finished", "status": "success",
					"iterations": 3, "tokens_used": 1500, "duration_seconds": 4.2,
				},
			},
			"count": 1,
		})
	})
	cl, _ := newTestServer(t, mux)

	entries, err := cl.Subagents("")
	if err != nil {
		t.Fatalf("Subagents: %v", err)
	}
	if len(entries) != 1 || entries[0].TaskID != "t1" || entries[0].Goal != "explore the repo" || entries[0].TokensUsed != 1500 {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := cl.Subagents("rk1"); err != nil {
		t.Fatalf("Subagents(key): %v", err)
	}
	if gotKey != "rk1" {
		t.Errorf("key filter not sent, got %q", gotKey)
	}
}
