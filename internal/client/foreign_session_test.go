package client

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// odekBootstrap mimics odek's GET /api/sessions/{id}: an unknown session
// token gets a mint-only reply (token header, {"session_id","bootstrapped"}
// body, no transcript); the minted token gets the session.
func odekBootstrap(t *testing.T, minted string, mintedWorks bool) (*Client, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("X-Session-Token") == minted && mintedWorks {
			w.Header().Set("X-Session-Token", minted)
			_, _ = w.Write([]byte(`{"id":"web-1","model":"m","messages":[{"role":"user","content":"hi"}]}`))
			return
		}
		if minted != "" {
			w.Header().Set("X-Session-Token", minted)
		}
		_, _ = w.Write([]byte(`{"session_id":"web-1","bootstrapped":true}`))
	}))
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}, &calls
}

// A session another front-end created (the WebUI) loads: the bootstrap
// reply is never mistaken for the session, and the detail is refetched once
// with the minted token.
func TestSessionDetailFollowsBootstrap(t *testing.T) {
	c, calls := odekBootstrap(t, "minted", true)
	sess, tok, err := c.SessionDetail("web-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != "web-1" || len(sess.Messages) != 1 || tok != "minted" {
		t.Fatalf("session = %+v token=%q, want the full transcript and the minted token", sess, tok)
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("requests = %d, want bootstrap + one refetch", n)
	}
}

// A bootstrap without a token, or one the server then refuses, is an error
// — never an empty session that would adopt an empty id.
func TestSessionDetailBootstrapFailures(t *testing.T) {
	c, _ := odekBootstrap(t, "", false)
	if s, _, err := c.SessionDetail("web-1", ""); err == nil || s.ID != "" {
		t.Fatalf("no minted token: session=%+v err=%v, want an error", s, err)
	}
	c, calls := odekBootstrap(t, "minted", false)
	if s, _, err := c.SessionDetail("web-1", ""); err == nil || s.ID != "" {
		t.Fatalf("refused minted token: session=%+v err=%v, want an error", s, err)
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("requests = %d, want exactly one retry", n)
	}
}

// A known token loads in one request, as before.
func TestSessionDetailKnownTokenSingleRequest(t *testing.T) {
	c, calls := odekBootstrap(t, "minted", true)
	if _, _, err := c.SessionDetail("web-1", "minted"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}
