package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// REST calls must never follow a redirect: Go copies the custom token
// headers onto a cross-host hop (and replays POST bodies on 307/308).
func TestStopJobDoesNotFollowRedirect(t *testing.T) {
	var leaked atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Odek-Ws-Token") != "" || r.Header.Get("X-Session-Token") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(`{"status":"stopped"}`))
	}))
	defer evil.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	c := &Client{baseURL: origin.URL, serveToken: "instance-secret"}
	if _, err := c.StopJob("s1", "session-secret", "j1"); err == nil {
		t.Fatal("StopJob followed a redirect; want a status error")
	}
	if leaked.Load() {
		t.Fatal("auth headers reached the redirect target")
	}
}

// The REST clients Dial builds (interactive and slow) refuse redirects too.
func TestDialClientsDoNotFollowRedirects(t *testing.T) {
	base := validateServer(t, func(c *ws.Conn) { time.Sleep(2 * time.Second) }, nil)
	c, err := Dial("ws"+strings.TrimPrefix(base, "http")+"/ws", base, base, "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for name, hc := range map[string]*http.Client{"http": c.http, "slowHTTP": c.slowHTTP} {
		if hc.CheckRedirect == nil || hc.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
			t.Errorf("%s follows redirects", name)
		}
	}
}
