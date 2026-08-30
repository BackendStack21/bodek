package client

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// TestSendSubagentCancelFrame pins the wire shape of the per-agent stop:
// session-scoped auth plus the task id, mirroring the cancel frame.
func TestSendSubagentCancelFrame(t *testing.T) {
	var mu sync.Mutex
	var frames []map[string]any
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var data []byte
			if err := ws.Message.Receive(c, &data); err != nil {
				return
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			mu.Lock()
			frames = append(frames, m)
			mu.Unlock()
		}
	}))
	cl, _ := newTestServer(t, mux)

	if err := cl.SendSubagentCancel("s1", "a1", "task-uuid"); err != nil {
		t.Fatalf("SendSubagentCancel: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(frames)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no frame arrived")
		case <-time.After(20 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	f := frames[0]
	if f["type"] != "subagent_cancel" || f["session_id"] != "s1" || f["auth_token"] != "a1" || f["task_id"] != "task-uuid" {
		t.Errorf("subagent_cancel frame = %v", f)
	}
}
