package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestLegacyTokenProbeError(t *testing.T) {
	// Unreachable server: the cookie fetch fails and the auth-enforcement
	// probe fails too, so legacyToken returns a hard error.
	if _, err := legacyToken("http://127.0.0.1:1"); err == nil {
		t.Error("expected legacyToken probe error to unreachable host")
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

func TestConnectURLTokenQuery(t *testing.T) {
	// The exact URL odek serve prints carries ?token=; it must be honored and
	// stripped before deriving the connection URLs.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	conn, err := Connect(Options{URL: srv.URL + "/?token=qtok"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn.Token != "qtok" {
		t.Errorf("Token = %q, want %q", conn.Token, "qtok")
	}
	if conn.BaseURL != srv.URL {
		t.Errorf("BaseURL = %q, want query stripped to %q", conn.BaseURL, srv.URL)
	}
}

func TestConnectExplicitTokenPrecedence(t *testing.T) {
	// Options.Token beats ?token= in the URL and skips the legacy fallback
	// entirely (no cookie, no probe needed).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models" {
			t.Error("explicit token should skip the legacy auth probe")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	conn, err := Connect(Options{URL: srv.URL + "/?token=urltok", Token: "explicit"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn.Token != "explicit" {
		t.Errorf("Token = %q, want %q", conn.Token, "explicit")
	}
}

func TestConnectLegacyOldServer(t *testing.T) {
	// No cookie and an unprotected API: this odek predates enforced auth, so
	// Connect proceeds with an empty token.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	conn, err := Connect(Options{URL: srv.URL})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn.Token != "" {
		t.Errorf("Token = %q, want empty for a pre-auth odek", conn.Token)
	}
}

func TestConnectEnforcedAuthError(t *testing.T) {
	// No cookie and 403 from the API: auth is enforced, so Connect must fail
	// with guidance on how to supply the token.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := Connect(Options{URL: srv.URL})
	if err == nil {
		t.Fatal("expected Connect to fail when auth is enforced")
	}
	if !strings.Contains(err.Error(), "requires a WS token") ||
		!strings.Contains(err.Error(), "--token") {
		t.Errorf("error should explain how to pass the token, got: %v", err)
	}
}

func TestSplitTokenURL(t *testing.T) {
	cases := []struct{ raw, base, token string }{
		{"http://127.0.0.1:8080/?token=abc", "http://127.0.0.1:8080/", "abc"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", ""},
		{"http://127.0.0.1:8080/?token=a%20b", "http://127.0.0.1:8080/", "a b"},
		{"\x7f", "\x7f", ""}, // unparseable URL is returned as-is, without a token
	}
	for _, tc := range cases {
		base, token := splitTokenURL(tc.raw)
		if base != tc.base || token != tc.token {
			t.Errorf("splitTokenURL(%q) = %q, %q; want %q, %q", tc.raw, base, token, tc.base, tc.token)
		}
	}
}

func TestParseTokenLine(t *testing.T) {
	cases := []struct{ line, want string }{
		{"  WS token:  cafef00d", "cafef00d"},
		{"odek serve ⚡  http://127.0.0.1:8080/?token=beef", "beef"},
		{"odek serve ⚡  http://127.0.0.1:8080/?token=beef&x=1", "beef"},
		{"  WebSocket: ws://127.0.0.1:8080/ws", ""},
		{"random log line", ""},
	}
	for _, tc := range cases {
		if got := parseTokenLine(tc.line); got != tc.want {
			t.Errorf("parseTokenLine(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
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
