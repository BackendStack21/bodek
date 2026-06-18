package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// newTestServer spins an httptest server that speaks the bits of the odek serve
// protocol bodek depends on: a /ws endpoint plus the REST APIs.
func newTestServer(t *testing.T, mux *http.ServeMux) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	cl, err := Dial(wsURL+"/ws", srv.URL, srv.URL, "test-token")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl, srv
}

func TestDialPromptStream(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		// Verify the auth header was forwarded.
		if got := c.Request().Header.Get("X-Odek-Ws-Token"); got != "test-token" {
			return
		}
		var data []byte
		if err := ws.Message.Receive(c, &data); err != nil {
			return
		}
		var p prompt
		_ = json.Unmarshal(data, &p)
		_ = ws.JSON.Send(c, map[string]any{"type": "session", "session_id": "s1", "auth_token": "a1", "model": "m"})
		_ = ws.Message.Send(c, `{"type":"token","content":"`+p.Content+`"}`)
		_ = ws.Message.Send(c, `{"type":"done","latency":1.0}`)
	}))
	cl, _ := newTestServer(t, mux)

	if err := cl.SendPrompt("hello", PromptOpts{Thinking: "enabled", Model: "x"}); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	want := []string{"session", "token", "done"}
	for i, w := range want {
		select {
		case ev := <-cl.Events:
			if ev.Type != w {
				t.Errorf("event %d = %q, want %q", i, ev.Type, w)
			}
			if w == "token" && ev.Content != "hello" {
				t.Errorf("token content = %q", ev.Content)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for %q", w)
		}
	}
}

func TestSendApprovalAndDisconnect(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		var data []byte
		if err := ws.Message.Receive(c, &data); err != nil {
			return
		}
		var a approval
		_ = json.Unmarshal(data, &a)
		_ = ws.JSON.Send(c, map[string]any{"type": "ack", "name": a.Action})
		// Close → client should surface EventDisconnected.
	}))
	cl, _ := newTestServer(t, mux)

	if err := cl.SendApproval("apr-1", "approve"); err != nil {
		t.Fatalf("SendApproval: %v", err)
	}
	var sawAck, sawDisc bool
	for !sawDisc {
		select {
		case ev, ok := <-cl.Events:
			if !ok {
				t.Fatal("channel closed without EventDisconnected")
			}
			switch ev.Type {
			case "ack":
				sawAck = true
				if ev.Name != "approve" {
					t.Errorf("ack name = %q", ev.Name)
				}
			case EventDisconnected:
				sawDisc = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timeout")
		}
	}
	if !sawAck {
		t.Error("never saw ack event")
	}
}

func TestMalformedFrameIgnored(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		var data []byte
		_ = ws.Message.Receive(c, &data)
		_ = ws.Message.Send(c, `not json`) // ignored
		_ = ws.Message.Send(c, `{"type":"token","content":"ok"}`)
	}))
	cl, _ := newTestServer(t, mux)
	_ = cl.SendPrompt("x", PromptOpts{})
	select {
	case ev := <-cl.Events:
		if ev.Type != "token" {
			t.Errorf("expected token after malformed frame, got %q", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestDialError(t *testing.T) {
	if _, err := Dial("ws://127.0.0.1:1/ws", "http://127.0.0.1:1", "http://127.0.0.1:1", "t"); err == nil {
		t.Error("expected dial error to unreachable server")
	}
}

func TestCloseNilConn(t *testing.T) {
	c := &Client{}
	if err := c.Close(); err != nil {
		t.Errorf("Close on nil conn = %v", err)
	}
}

func TestRESTEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) { _, _ = c.Write(nil) }))
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Session{{ID: "s1", Task: "do it", Turns: 2}})
	})
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.Header.Get("X-Session-Token") != "tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("X-Session-Token", "tok")
			json.NewEncoder(w).Encode(Session{ID: "s1", Messages: []SessionMessage{{Role: "user", Content: "hi"}}})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]ModelInfo{{ID: "m1", Current: true, Description: "model one"}})
	})
	mux.HandleFunc("/api/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "main" {
			t.Errorf("query = %q", r.URL.Query().Get("q"))
		}
		json.NewEncoder(w).Encode([]Resource{{ID: "@main.go", Type: "file", Label: "main.go"}})
	})
	mux.HandleFunc("/api/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Query().Get("session_id") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	cl, _ := newTestServer(t, mux)

	sess, err := cl.Sessions()
	if err != nil || len(sess) != 1 || sess[0].Task != "do it" {
		t.Fatalf("Sessions = %+v, %v", sess, err)
	}
	detail, tok, err := cl.SessionDetail("s1", "tok")
	if err != nil || tok != "tok" || len(detail.Messages) != 1 {
		t.Fatalf("SessionDetail = %+v tok=%q err=%v", detail, tok, err)
	}
	if err := cl.DeleteSession("s1", "tok"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	models, err := cl.Models()
	if err != nil || len(models) != 1 || !models[0].Current {
		t.Fatalf("Models = %+v, %v", models, err)
	}
	res, err := cl.Resources("main", 6)
	if err != nil || len(res) != 1 || res[0].ID != "@main.go" {
		t.Fatalf("Resources = %+v, %v", res, err)
	}
	if err := cl.Cancel("s1", "tok"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
}

func TestRESTErrorStatuses(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {}))
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/api/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	cl, _ := newTestServer(t, mux)

	if _, _, err := cl.SessionDetail("s1", ""); err == nil {
		t.Error("expected SessionDetail error on 401")
	}
	if err := cl.DeleteSession("s1", ""); err == nil {
		t.Error("expected DeleteSession error on 401")
	}
	if err := cl.Cancel("s1", ""); err == nil {
		t.Error("expected Cancel error on 500")
	}
	if _, err := cl.Sessions(); err == nil {
		t.Error("expected Sessions error (404)")
	}
}
