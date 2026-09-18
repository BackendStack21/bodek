package client

import (
	"net/http"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// Regression: readLoop had no read deadline and no pong timeout, so a
// half-open TCP connection (laptop sleep, NAT reset, dropped VPN) never
// produced EventDisconnected — the TUI sat on a dead socket forever and the
// reconnect machinery never ran. An idle read deadline guarantees a silent
// death surfaces: the live path receives the server's inline heartbeat
// answers (bodek pings every 20s), so 45s of true inbound silence only
// happens on a dead link.
func TestHalfOpenSocketProducesDisconnect(t *testing.T) {
	oldIdle := readIdleTimeout
	readIdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readIdleTimeout = oldIdle })

	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		// Server goes silent: reads nothing, sends nothing, never closes —
		// the half-open shape. Hold the TCP pair open.
		time.Sleep(5 * time.Second)
	}))
	cl, _ := newTestServer(t, mux)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-cl.Events:
			if !ok {
				t.Fatal("channel closed without EventDisconnected")
			}
			if ev.Type == EventDisconnected {
				return // fixed: idle deadline surfaced the dead socket
			}
		case <-deadline:
			t.Fatal("no EventDisconnected on an idle half-open socket — dead links are never detected")
		}
	}
}
