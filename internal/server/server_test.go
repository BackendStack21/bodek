package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFreePort(t *testing.T) {
	p, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if p <= 0 || p > 65535 {
		t.Errorf("freePort = %d, out of range", p)
	}
}

func TestWaitReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := waitReady(srv.URL, 3*time.Second); err != nil {
		t.Errorf("waitReady: %v", err)
	}
}

func TestWaitReadyTimeout(t *testing.T) {
	// An unreachable address should time out quickly.
	if err := waitReady("http://127.0.0.1:1", 300*time.Millisecond); err == nil {
		t.Error("expected waitReady timeout")
	}
}

func TestFetchToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: wsTokenCookie, Value: "secret-token"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	tok, err := fetchToken(srv.URL)
	if err != nil || tok != "secret-token" {
		t.Fatalf("fetchToken = %q, %v", tok, err)
	}
}

func TestFetchTokenMissingCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if _, err := fetchToken(srv.URL); err == nil {
		t.Error("expected error when cookie missing")
	}
}

func TestFetchTokenRequestError(t *testing.T) {
	if _, err := fetchToken("http://127.0.0.1:1"); err == nil {
		t.Error("expected fetchToken error to unreachable host")
	}
}

func TestConnectViaURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: wsTokenCookie, Value: "tok"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	conn, err := Connect(Options{URL: srv.URL})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn.Token != "tok" {
		t.Errorf("Token = %q", conn.Token)
	}
	if conn.BaseURL != srv.URL {
		t.Errorf("BaseURL = %q, want %q", conn.BaseURL, srv.URL)
	}
	wantWS := "ws" + srv.URL[len("http"):] + "/ws"
	if conn.WSURL != wantWS {
		t.Errorf("WSURL = %q, want %q", conn.WSURL, wantWS)
	}
	conn.Stop() // no spawned process — must be a no-op
}

func TestConnectSpawnMissingBinary(t *testing.T) {
	_, err := Connect(Options{Bin: "definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Error("expected error for missing odek binary")
	}
}

func TestStopNilSafe(t *testing.T) {
	var c *Conn
	c.Stop() // must not panic
	(&Conn{}).Stop()
}
