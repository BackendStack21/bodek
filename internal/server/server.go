// Package server launches and supervises an `odek serve` process, or attaches
// to one already running, and resolves the connection details bodek needs:
// the base HTTP URL, the WebSocket URL, and the per-instance auth token.
package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const wsTokenCookie = "odek_ws_token"

// Conn holds everything needed to talk to an odek serve instance.
type Conn struct {
	BaseURL string // http://127.0.0.1:port
	WSURL   string // ws://127.0.0.1:port/ws
	Origin  string // http://127.0.0.1:port (accepted by the server's origin check)
	Token   string // per-instance CSRF token

	proc *exec.Cmd // non-nil when bodek spawned the server
}

// Options configures how the odek serve instance is obtained.
type Options struct {
	// URL of an already-running odek serve (e.g. "http://127.0.0.1:8080").
	// When set, bodek attaches instead of spawning.
	URL string

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
func Connect(opts Options) (*Conn, error) {
	c := &Conn{}

	if opts.URL != "" {
		base := strings.TrimRight(opts.URL, "/")
		c.BaseURL = base
		c.Origin = base
		c.WSURL = "ws" + strings.TrimPrefix(base, "http") + "/ws"
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
	}

	if err := waitReady(c.BaseURL, 30*time.Second); err != nil {
		c.Stop()
		return nil, fmt.Errorf("odek serve did not become ready: %w", err)
	}

	token, err := fetchToken(c.BaseURL)
	if err != nil {
		c.Stop()
		return nil, fmt.Errorf("fetch auth token: %w", err)
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

	cmd := exec.Command(bin, args...)
	cmd.Stderr = opts.Stderr
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

// freePort asks the OS for an unused TCP port on the loopback interface.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitReady polls the server root until it responds or the timeout elapses.
func waitReady(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
		resp, err := client.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

// fetchToken performs GET / and reads the per-instance CSRF token from the
// odek_ws_token Set-Cookie header.
func fetchToken(baseURL string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == wsTokenCookie && ck.Value != "" {
			return ck.Value, nil
		}
	}
	return "", fmt.Errorf("server did not issue an %s cookie", wsTokenCookie)
}
