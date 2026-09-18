package client

import (
	"net"
	"testing"
	"time"
)

// Regression: Dial had no dial timeout — a black-holed (or silently
// non-responsive) remote blocked until the OS TCP timeout. The dial (and
// the WS handshake over the same connection) must be bounded.
func TestDialIsBoundedAgainstSilentPeer(t *testing.T) {
	old := wsDialTimeout
	wsDialTimeout = 200 * time.Millisecond
	defer func() { wsDialTimeout = old }()

	// A peer that accepts TCP but never reads or writes: the WS handshake
	// can never complete, so only an explicit deadline can unblock Dial.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open, silently — never respond.
			time.Sleep(10 * time.Second)
			_ = c.Close()
		}
	}()

	addr := ln.Addr().String()
	done := make(chan error, 1)
	go func() {
		_, err := Dial("ws://"+addr+"/ws", "http://127.0.0.1", "http://"+addr, "tok")
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Dial blocked past the dial timeout against a silent peer")
	}
}
