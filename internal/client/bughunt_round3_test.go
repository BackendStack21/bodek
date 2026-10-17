package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// odek's 404 for an unknown session must not read as a missing route:
// that is permanent and switched the jobs watcher off for the whole run.
func TestRegressSessionNotFoundIsNotRouteMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "session not found", http.StatusNotFound)
	}))
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.Jobs("gone", "tok"); !errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrJobsUnavailable) {
		t.Errorf("Jobs err = %v, want ErrSessionNotFound", err)
	}
	// The plan route stays permanent: odek < v1.27 answers its missing
	// route with this very body (the id parser swallows "{id}/plan").
	if _, err := c.SessionPlan("gone", "tok"); !errors.Is(err, ErrPlanUnavailable) {
		t.Errorf("SessionPlan err = %v, want ErrPlanUnavailable", err)
	}
}

// Frames go out unescaped and never past odek's 8 MiB cap, which would
// make odek drop the connection and lose the prompt silently.
func TestRegressPromptFrameBounds(t *testing.T) {
	got := make(chan int, 4)
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		c.MaxPayloadBytes = 32 << 20
		for {
			var s string
			if err := ws.Message.Receive(c, &s); err != nil {
				return
			}
			got <- len(s)
		}
	}))
	cl, _ := newTestServer(t, mux)

	// 3 MiB of markup fits only without HTML escaping (6 bytes per <).
	html := strings.Repeat("<a>&", 3<<20/4)
	if err := cl.SendPrompt("review", PromptOpts{Attachments: []Attachment{{Name: "x.html", Content: html}}}); err != nil {
		t.Fatalf("markup prompt refused: %v", err)
	}
	select {
	case n := <-got:
		if n > maxFrameBytes {
			t.Fatalf("frame %d bytes exceeds the cap", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("frame never arrived")
	}

	big := strings.Repeat("x", 9<<20)
	err := cl.SendPrompt("too big", PromptOpts{Attachments: []Attachment{{Name: "big.txt", Content: big}}})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized prompt err = %v, want ErrFrameTooLarge", err)
	}
}

// Close must not wait out writeTimeout behind a write stuck on a peer that
// stopped reading — the TUI calls it on the update goroutine.
func TestRegressCloseDoesNotWaitOutStuckWrite(t *testing.T) {
	oldGrace := closeGrace
	closeGrace = 100 * time.Millisecond
	t.Cleanup(func() { closeGrace = oldGrace })
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) { time.Sleep(20 * time.Second) }))
	cl, _ := newTestServer(t, mux)

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_ = cl.SendPrompt("stuck", PromptOpts{Attachments: []Attachment{{Name: "a", Content: strings.Repeat("x", 7<<20)}}})
	}()
	time.Sleep(300 * time.Millisecond) // let the write fill the socket buffers
	start := time.Now()
	_ = cl.Close()
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("Close blocked %v behind the stuck write", d)
	}
	<-sent
}
