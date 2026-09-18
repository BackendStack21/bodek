package client

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// TestReadLoopExitsWhenEventsAbandoned guards the reconnect swap: when the
// consumer abandons a full Events channel (a reconnect during a delta
// firehose), closing the socket must let readLoop exit instead of parking
// forever on a send to a channel nobody reads.
func TestReadLoopExitsWhenEventsAbandoned(t *testing.T) {
	// A 10x flood guarantees the parked state: once the consumer stops
	// draining, readLoop refills the channel and parks on a send.
	flood := eventBuffer * 10
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		defer close(done)
		for i := 0; i < flood; i++ {
			if err := ws.Message.Send(c, `{"type":"note","content":"x"}`); err != nil {
				return
			}
		}
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]
	base := runtime.NumGoroutine() // before Dial: excludes readLoop entirely
	cl, err := Dial(wsURL+"/ws", srv.URL, srv.URL, "test-token")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Consume exactly the channel capacity, then vanish — the readLoop is
	// provably parked on a send to the full channel, exactly the reconnect
	// swap's state when reconnect.go drops the old client mid-firehose.
	for i := 0; i < eventBuffer; i++ {
		select {
		case <-cl.Events:
		case <-time.After(10 * time.Second):
			t.Fatalf("stalled draining event %d", i)
		}
	}
	// The 104 overflow frames are in flight; give readLoop a moment to park.
	time.Sleep(200 * time.Millisecond)

	_ = cl.Close()

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("readLoop goroutine leaked after Close on an abandoned full Events channel: %d goroutines (base %d)", n, base)
	}
}
