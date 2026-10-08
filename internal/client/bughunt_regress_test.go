package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// wss:// must start a TLS handshake (ClientHello record type 0x16).
func TestRegressWSSStartsTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	first := make(chan byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		b := make([]byte, 1)
		if _, err := c.Read(b); err == nil {
			first <- b[0]
		}
	}()
	addr := ln.Addr().String()
	go func() { _, _ = Dial("wss://"+addr+"/ws", "https://"+addr, "https://"+addr, "tok") }()
	select {
	case b := <-first:
		if b != 0x16 {
			t.Fatalf("wss dial sent %q (0x%02x) instead of a TLS ClientHello", b, b)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no bytes received")
	}
}

func validateServer(t *testing.T, onWS func(*ws.Conn), mux func(*http.ServeMux)) string {
	t.Helper()
	m := http.NewServeMux()
	m.Handle("/ws", ws.Server{
		Handshake: func(*ws.Config, *http.Request) error { return nil },
		Handler:   ws.Handler(onWS),
	})
	if mux != nil {
		mux(m)
	}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return srv.URL
}

// The trailing <16 thinking deltas must surface without a later frame.
func TestRegressThinkingTailFlushesWhenIdle(t *testing.T) {
	base := validateServer(t, func(c *ws.Conn) {
		for range 3 {
			_ = ws.Message.Send(c, `{"type":"thinking_delta","content":"x"}`)
		}
		time.Sleep(3 * time.Second)
	}, nil)
	c, err := Dial("ws"+strings.TrimPrefix(base, "http")+"/ws", base, base, "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	select {
	case ev := <-c.Events:
		if ev.Type != "thinking_delta" {
			t.Fatalf("got %v", ev.Type)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("pending thinking deltas were held back while the stream idled")
	}
}

// Consolidation is LLM-backed; it must not be cut at the 3s interactive budget.
func TestRegressConsolidateOutlivesInteractiveTimeout(t *testing.T) {
	base := validateServer(t, func(c *ws.Conn) { time.Sleep(6 * time.Second) }, func(m *http.ServeMux) {
		m.HandleFunc("/api/memory/consolidate", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(3500 * time.Millisecond)
			_, _ = w.Write([]byte(`{}`))
		})
	})
	c, err := Dial("ws"+strings.TrimPrefix(base, "http")+"/ws", base, base, "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.ConsolidateMemory("user"); err != nil {
		t.Fatalf("consolidate failed although the server completes it: %v", err)
	}
}
