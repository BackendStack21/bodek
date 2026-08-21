package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/server"
)

// TestE2ERealServe drives bodek's client against a real `odek serve` process
// (ODEK_BIN, default "odek") — the contract check that the in-process
// stand-ins can't provide: real server_info/pong frames, the sessions
// envelope, profiles, limits with effective_prices, and health.
//
// Gate: BODEK_E2E=true. No LLM call is made — only the connection hello,
// heartbeat, and read-only REST surface.
func TestE2ERealServe(t *testing.T) {
	if os.Getenv("BODEK_E2E") == "" {
		t.Skip("set BODEK_E2E=true (and optionally ODEK_BIN) to run the live-server E2E")
	}
	bin := os.Getenv("ODEK_BIN")
	if bin == "" {
		bin = "odek"
	}

	conn, err := server.Connect(server.Options{Bin: bin, Sandbox: false})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(conn.Stop)

	cl, err := Dial(conn.WSURL, conn.Origin, conn.BaseURL, conn.Token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	// server_info hello → then ping/pong measured round-trip.
	var sawHello bool
	deadline := time.After(10 * time.Second)
	for !(sawHello) {
		select {
		case ev, ok := <-cl.Events:
			if !ok {
				t.Fatal("event channel closed before server_info")
			}
			if ev.Type == "server_info" {
				sawHello = true
				if ev.Model == "" || ev.UptimeSeconds < 0 {
					t.Errorf("server_info = %+v", ev)
				}
			}
		case <-deadline:
			t.Fatal("no server_info within 10s")
		}
	}
	if err := cl.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	sawPong := false
	for !sawPong {
		select {
		case ev := <-cl.Events:
			if ev.Type == "pong" {
				sawPong = true
				if ev.T == 0 {
					t.Errorf("pong missing t: %+v", ev)
				}
			}
		case <-deadline:
			t.Fatal("no pong within deadline")
		}
	}

	// Read-only REST surface against the real server.
	page, err := cl.SearchSessions("", 50, 0)
	if err != nil {
		t.Errorf("SearchSessions: %v", err)
	} else if page.Limit != 50 {
		t.Errorf("envelope limit = %d", page.Limit)
	}
	profiles, err := cl.Profiles()
	if err != nil || len(profiles) == 0 {
		t.Errorf("Profiles = %+v, %v", profiles, err)
	}
	limits, err := cl.Limits()
	if err != nil {
		t.Errorf("Limits: %v", err)
	}
	h, err := cl.Health()
	if err != nil || h.Status != "ok" {
		t.Errorf("Health = %+v, %v", h, err)
	}
	_ = limits // prices may be unconfigured; decode is what matters
}

// fakeLLM answers OpenAI-compatible /chat/completions calls: streamed
// requests get SSE fragments ("Hello world"), buffered requests get a bulk
// answer. No tools are ever requested, so the agent loop finishes in one
// iteration.
func fakeLLM() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			for _, frag := range []string{"Hel", "lo ", "world"} {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", frag)
				fl.Flush()
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "bulk answer"},
			}},
		})
	})
}

// TestE2ERealServePrompt runs full prompt turns against a real `odek serve`
// backed by the fake LLM: with --stream the answer must arrive exclusively
// as token_delta fragments (the bulk token re-send is suppressed), and
// without it as one bulk token event. HOME is redirected so no sessions land
// in the operator's real store.
func TestE2ERealServePrompt(t *testing.T) {
	if os.Getenv("BODEK_E2E") == "" {
		t.Skip("set BODEK_E2E=true (and optionally ODEK_BIN) to run the live-server E2E")
	}
	bin := os.Getenv("ODEK_BIN")
	if bin == "" {
		bin = "odek"
	}

	llm := httptest.NewServer(fakeLLM())
	t.Cleanup(llm.Close)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ODEK_BASE_URL", llm.URL)
	t.Setenv("ODEK_API_KEY", "test-key")

	run := func(extra []string) (deltas, bulk string, evErr string) {
		t.Helper()
		conn, err := server.Connect(server.Options{Bin: bin, Sandbox: false, ExtraArgs: extra})
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(conn.Stop)
		cl, err := Dial(conn.WSURL, conn.Origin, conn.BaseURL, conn.Token)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer cl.Close()

		// Drain the hello, then prompt.
		for ev := range cl.Events {
			if ev.Type == "server_info" {
				break
			}
		}
		if err := cl.SendPrompt("hi", PromptOpts{}); err != nil {
			t.Fatalf("prompt: %v", err)
		}
		var sawDone bool
		deadline := time.After(30 * time.Second)
		for !sawDone {
			select {
			case ev, ok := <-cl.Events:
				if !ok {
					t.Fatal("socket closed before done")
				}
				switch ev.Type {
				case "token_delta":
					deltas += ev.Content
				case "token":
					bulk += ev.Content
				case "error":
					evErr = ev.Message
				case "done":
					sawDone = true
				}
			case <-deadline:
				t.Fatal("no done within 30s")
			}
		}
		// Let the async learn-loop goroutines settle before Stop.
		time.Sleep(300 * time.Millisecond)
		return deltas, bulk, evErr
	}

	deltas, bulk, evErr := run([]string{"--stream"})
	if evErr != "" {
		t.Fatalf("streamed run errored: %s", evErr)
	}
	if deltas != "Hello world" {
		t.Errorf("streamed fragments = %q", deltas)
	}
	if bulk != "" {
		t.Errorf("bulk token re-sent alongside deltas: %q", bulk)
	}

	deltas, bulk, evErr = run(nil)
	if evErr != "" {
		t.Fatalf("buffered run errored: %s", evErr)
	}
	if bulk != "bulk answer" {
		t.Errorf("bulk answer = %q", bulk)
	}
	if deltas != "" {
		t.Errorf("unexpected deltas without --stream: %q", deltas)
	}
}
