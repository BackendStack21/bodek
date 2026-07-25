// Package server launches and supervises an `odek serve` process, or attaches
// to one already running, and resolves the connection details bodek needs:
// the base HTTP URL, the WebSocket URL, and the per-instance auth token.
package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const wsTokenCookie = "odek_ws_token"

// readyTimeout bounds how long Connect waits for the server to come up. It is a
// variable so tests can shorten it.
var readyTimeout = 30 * time.Second

// Conn holds everything needed to talk to an odek serve instance.
type Conn struct {
	BaseURL string // http://127.0.0.1:port
	WSURL   string // ws://127.0.0.1:port/ws
	Origin  string // http://127.0.0.1:port (accepted by the server's origin check)
	Token   string // per-instance CSRF token

	proc *exec.Cmd        // non-nil when bodek spawned the server
	scan *tokenScanWriter // non-nil when bodek spawned the server
}

// Options configures how the odek serve instance is obtained.
type Options struct {
	// URL of an already-running odek serve (e.g. "http://127.0.0.1:8080").
	// A "?token=…" query (as printed by odek serve) is honored and stripped.
	// When set, bodek attaches instead of spawning.
	URL string

	// Token is the per-instance WS auth token, given explicitly (e.g. via
	// --token). It takes precedence over a token embedded in URL.
	Token string

	// Bin is the odek binary to spawn (default "odek"). Ignored when URL set.
	Bin string

	// Sandbox toggles the Docker sandbox for a spawned server. odek serve
	// defaults sandbox on; bodek defaults it off for a frictionless local TUI.
	Sandbox bool

	// ExtraArgs are passed through to `odek serve` (e.g. model/config flags).
	ExtraArgs []string

	// Stderr, if set, receives the spawned server's stderr.
	Stderr io.Writer
}

// Connect attaches to or launches an odek serve instance and resolves its
// auth token, returning a ready Conn.
//
// Token resolution order: Options.Token, then "?token=" in Options.URL, then
// the "WS token:" line a spawned odek serve prints to stderr, then a legacy
// fallback for servers that predate enforced auth.
func Connect(opts Options) (*Conn, error) {
	c := &Conn{}
	token := opts.Token

	if opts.URL != "" {
		base, urlToken := splitTokenURL(opts.URL)
		base = strings.TrimRight(base, "/")
		c.BaseURL = base
		c.Origin = base
		c.WSURL = "ws" + strings.TrimPrefix(base, "http") + "/ws"
		if token == "" {
			token = urlToken
		}
		if err := waitReady(c.BaseURL, readyTimeout); err != nil {
			return nil, fmt.Errorf("odek serve did not become ready: %w", err)
		}
	} else {
		port, err := freePort()
		if err != nil {
			return nil, fmt.Errorf("allocate port: %w", err)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		c.BaseURL = "http://" + addr
		c.Origin = c.BaseURL
		c.WSURL = "ws://" + addr + "/ws"
		if err := c.spawn(opts, addr); err != nil {
			return nil, err
		}
		// A current odek prints its token to stderr at startup; an older one
		// prints nothing, so stop waiting as soon as the server answers.
		if err := waitSpawned(c.BaseURL, c.scan, readyTimeout); err != nil {
			c.Stop()
			return nil, fmt.Errorf("odek serve did not become ready: %w", err)
		}
		if token == "" {
			token = c.scan.Token()
		}
	}

	if token == "" {
		legacy, err := legacyToken(c.BaseURL)
		if err != nil {
			c.Stop()
			return nil, err
		}
		token = legacy
	}
	c.Token = token
	return c, nil
}

func (c *Conn) spawn(opts Options, addr string) error {
	bin := opts.Bin
	if bin == "" {
		bin = "odek"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("cannot find %q on PATH — install odek or pass --url to attach to a running server", bin)
	}
	args := []string{"serve", "--addr", addr}
	if !opts.Sandbox {
		args = append(args, "--no-sandbox")
	} else {
		args = append(args, "--sandbox")
	}
	args = append(args, opts.ExtraArgs...)

	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	c.scan = &tokenScanWriter{w: stderr}

	cmd := exec.Command(bin, args...)
	cmd.Stderr = c.scan
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start odek serve: %w", err)
	}
	c.proc = cmd
	return nil
}

// Stop terminates a spawned server (no-op when attached to an external one).
// This lets odek run its own cleanup: sandbox teardown, memory flush, etc.
func (c *Conn) Stop() {
	if c == nil || c.proc == nil || c.proc.Process == nil {
		return
	}
	// SIGINT triggers odek serve's graceful shutdown (closes sockets, removes
	// sandbox containers). Fall back to Kill if it lingers.
	_ = c.proc.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = c.proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = c.proc.Process.Kill()
	}
}

// splitTokenURL separates an attach URL into its base and an optional
// "?token=" value, so users can paste the exact URL odek serve prints.
func splitTokenURL(raw string) (base, token string) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, ""
	}
	token = u.Query().Get("token")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), token
}

// tokenScanWriter passes a spawned server's stderr through unchanged while
// scanning each line for the per-instance token odek serve prints at startup:
//
//	odek serve ⚡  http://127.0.0.1:8080/?token=<hex>
//	  WebSocket: ws://127.0.0.1:8080/ws
//	  WS token:  <hex>
type tokenScanWriter struct {
	w   io.Writer
	mu  sync.Mutex
	buf []byte // partial line not yet terminated by '\n'
	tok string
}

func (s *tokenScanWriter) Write(p []byte) (int, error) {
	s.scan(p)
	return s.w.Write(p)
}

// Token returns the scanned token, or "" if no token line was seen yet.
func (s *tokenScanWriter) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok
}

func (s *tokenScanWriter) scan(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok != "" {
		return // already found; keep passing bytes through
	}
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return
		}
		line := string(s.buf[:i])
		s.buf = s.buf[i+1:]
		if tok := parseTokenLine(line); tok != "" {
			s.tok = tok
			return
		}
	}
}

// parseTokenLine extracts the token from a "WS token:  <hex>" line, falling
// back to the "?token=" query in the "odek serve ⚡  <url>" banner line.
func parseTokenLine(line string) string {
	if i := strings.Index(line, "WS token:"); i >= 0 {
		return strings.TrimSpace(line[i+len("WS token:"):])
	}
	if i := strings.Index(line, "?token="); i >= 0 {
		tok := line[i+len("?token="):]
		if j := strings.IndexAny(tok, "& \t\r"); j >= 0 {
			tok = tok[:j]
		}
		return tok
	}
	return ""
}

// freePort asks the OS for an unused TCP port on the loopback interface.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitReady polls the server root until it responds or the timeout elapses.
func waitReady(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probeReady(baseURL) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

// waitSpawned waits until a spawned server prints its token line or answers
// HTTP, whichever comes first. Old odek versions print no token line, so
// readiness alone also ends the wait (the legacy token path handles those).
func waitSpawned(baseURL string, scan *tokenScanWriter, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if scan.Token() != "" || probeReady(baseURL) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

// probeReady reports whether the server root answers without a server error.
func probeReady(baseURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

// legacyToken resolves the token against servers that did not provide one up
// front: try the old cookie-based fetch first, then probe whether the API
// enforces auth at all (old odek versions did not).
func legacyToken(baseURL string) (string, error) {
	if tok, err := fetchToken(baseURL); err == nil {
		return tok, nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/api/models")
	if err != nil {
		return "", fmt.Errorf("probe auth enforcement: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("this odek serve requires a WS token — attach with the token URL it printed (bodek --url 'http://127.0.0.1:8080/?token=…') or pass --token")
	}
	return "", nil
}

// fetchToken performs GET / and reads the per-instance CSRF token from the
// odek_ws_token Set-Cookie header. Current odek serve only sets the cookie
// when the request URL carries "?token=", so a plain GET / usually yields
// nothing — callers must treat that as "no cookie", not a hard failure.
func fetchToken(baseURL string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	for _, ck := range resp.Cookies() {
		if ck.Name == wsTokenCookie && ck.Value != "" {
			return ck.Value, nil
		}
	}
	return "", fmt.Errorf("server did not issue an %s cookie", wsTokenCookie)
}
