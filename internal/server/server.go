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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BackendStack21/bodek/internal/watchdog"
)

const wsTokenCookie = "odek_ws_token"

// readyTimeout bounds how long Connect waits for the server to come up. It is a
// variable so tests can shorten it.
var readyTimeout = 30 * time.Second

// stopTimeout bounds how long Stop waits for the spawned server's graceful
// shutdown before killing it regardless of activity. It is a variable so
// tests can shorten it.
var stopTimeout = 30 * time.Second

// activityGrace bounds how long Stop waits for stderr silence before
// declaring the graceful window dead. It must stay comfortably above the
// old uniform 8s wait: a silent-but-working teardown (fsync, DB flush)
// writes nothing while it works, so a short grace would kill progress
// faster than the code this replaces. It is a variable so tests can
// shorten it.
var activityGrace = 12 * time.Second

// StopEvent reports progress of Conn.Stop so callers can show the user what
// the shutdown wait is doing.
type StopEvent int

const (
	StopStopping  StopEvent = iota // graceful SIGINT sent; waiting for exit
	StopEscalated                  // graceful window expired or silent; SIGKILL sent
	StopStopped                    // child exited gracefully within the window
)

// StopProgress reports the countdown while Stop waits for the child to
// exit: Elapsed since SIGINT, Max the hard deadline, Idle how long the
// child's stderr has been silent (the escalation trigger).
type StopProgress struct {
	Elapsed time.Duration
	Max     time.Duration
	Idle    time.Duration
}

// String renders the event for status lines.
func (e StopEvent) String() string {
	switch e {
	case StopStopping:
		return "stopping"
	case StopEscalated:
		return "escalated"
	case StopStopped:
		return "stopped"
	default:
		return fmt.Sprintf("StopEvent(%d)", int(e))
	}
}

// Conn holds everything needed to talk to an odek serve instance.
type Conn struct {
	BaseURL string // http://127.0.0.1:port
	WSURL   string // ws://127.0.0.1:port/ws
	Origin  string // http://127.0.0.1:port (accepted by the server's origin check)
	Token   string // per-instance CSRF token
	Version string // engine version as printed by `<bin> version` (e.g. "v0.2.0"); spawn mode only

	proc     *exec.Cmd        // non-nil when bodek spawned the server
	scan     *tokenScanWriter // non-nil when bodek spawned the server
	reaped   atomic.Bool      // the reaper observed the child exit
	reapDone chan struct{}    // closed when the reaper's Wait returns
	watch    func()           // cancels the orphan watchdog (nil when none)
	watchMu  sync.Mutex
	lastAct  atomic.Int64 // last child stderr write, unix nanos (0 = never)
	stopping atomic.Bool  // Stop reentrancy guard

	// OnStopEvent, when set, receives shutdown progress from Stop.
	OnStopEvent func(StopEvent)

	// OnStopProgress, when set, receives ~1s-granularity countdown ticks
	// during Stop's graceful wait, for live countdown rendering.
	OnStopProgress func(StopProgress)
}

// watchdogBin is the executable the orphan watchdog re-execs as. It is a
// variable so tests can substitute a harmless stand-in.
var watchdogBin func() (string, error)

func init() {
	watchdogBin = func() (string, error) { return os.Executable() }
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
		if err := waitSpawned(c.BaseURL, c.scan, c.procAlive, readyTimeout); err != nil {
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
	// odek serve exposes no version over HTTP/WS, so ask the binary directly.
	// Best effort: any failure leaves Version empty and never blocks startup.
	c.Version = binVersion(context.Background(), bin)
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
	c.scan = &tokenScanWriter{w: stderr, act: &c.lastAct}

	cmd := exec.Command(bin, args...)
	cmd.Stderr = c.scan
	cmd.Env = os.Environ()
	// Own process group so the watchdog (and Stop) can signal the server
	// and any of its subprocesses as a unit, without touching bodek itself.
	setPgroup(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start odek serve: %w", err)
	}
	c.proc = cmd
	c.startReaper()
	c.startWatchdog()
	return nil
}

// startWatchdog launches the orphan guard as a separate process: a re-exec
// of the bodek binary that kills the spawned server's process group if this
// process dies without stopping it (SIGKILL, crash, lost terminal). The
// guard is self-terminating — it exits once the server does — and is a
// no-op on platforms without process-group signalling.
func (c *Conn) startWatchdog() {
	if !watchdog.Supported() {
		return
	}
	self, err := watchdogBin()
	if err != nil {
		return // best effort: graceful Stop remains the primary path
	}
	wd := exec.Command(self, watchdogArg,
		strconv.Itoa(os.Getpid()), strconv.Itoa(c.proc.Process.Pid))
	wd.Stdout = io.Discard
	wd.Stderr = io.Discard
	setPgroup(wd) // detached: not in bodek's group, immune to group signals
	if err := wd.Start(); err != nil {
		return // best effort: graceful Stop remains the primary path
	}
	c.watchMu.Lock()
	c.watch = func() {
		// Kill and reap: an unreaped guard reads as alive to kill -0 probes.
		_ = wd.Process.Kill()
		go func() { _ = wd.Wait() }()
	}
	c.watchMu.Unlock()
}

// versionTimeout bounds the `<bin> version` probe so a hung binary never
// delays startup.
const versionTimeout = 2 * time.Second

// binVersion runs `<bin> version` and returns the token following "odek" on
// the first output line, exactly as printed (e.g. "v0.2.0" from "odek v0.2.0",
// or "1.2.3" from a bare "odek 1.2.3"). Any failure — missing binary, non-zero
// exit, timeout, or unrecognized output — yields "" and is not fatal.
func binVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	fields := strings.Fields(line)
	if len(fields) >= 2 && fields[0] == "odek" {
		return fields[1]
	}
	return ""
}

// Stop terminates a spawned server (no-op when attached to an external one).
// This lets odek run its own cleanup: sandbox teardown, memory flush, etc.
func (c *Conn) Stop() {
	if c == nil || c.proc == nil || c.proc.Process == nil {
		return
	}
	// Reentrancy guard: two concurrent Stops would interleave events and
	// double-signal. The first caller owns the shutdown.
	if !c.stopping.CompareAndSwap(false, true) {
		return
	}
	// An already-reaped child must not be signalled: the PID may have been
	// recycled and the group kill would hit an innocent process.
	if c.reaped.Load() {
		return
	}
	// Graceful shutdown owns the exit — retire the orphan watchdog first.
	c.watchMu.Lock()
	if c.watch != nil {
		c.watch()
		c.watch = nil
	}
	c.watchMu.Unlock()
	// SIGINT triggers odek serve's graceful shutdown (closes sockets, removes
	// sandbox containers), delivered to the server's whole process group so
	// its own subprocesses follow. SIGKILL escalation likewise targets the
	// group. Fall back to Kill if it lingers.
	if c.OnStopEvent != nil {
		c.OnStopEvent(StopStopping)
	}
	c.signalServer(syscall.SIGINT)
	// The reaper owns Wait (started at spawn); select on its completion
	// instead of a second Wait, which exec.Cmd forbids.
	done := c.reapDone
	if done == nil {
		done = make(chan struct{})
		go func() { _ = c.proc.Wait(); close(done) }()
	}
	// Progress-aware escalation (Stop): the idle clock starts at the SIGINT,
	// and every child stderr write through the scan writer resets it. A
	// silent child is declared dead after activityGrace — long before the
	// hard deadline — while an active teardown (memory flush writing output)
	// earns the full stopTimeout window.
	c.lastAct.Store(time.Now().UnixNano())
	start := time.Now()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	lastTick := time.Duration(-1)
	for {
		select {
		case <-done:
			if c.OnStopEvent != nil {
				c.OnStopEvent(StopStopped)
			}
			return
		case <-tick.C:
			now := time.Now()
			if c.OnStopProgress != nil {
				if s := now.Sub(start).Truncate(time.Second); s != lastTick {
					lastTick = s
					c.OnStopProgress(StopProgress{
						Elapsed: now.Sub(start),
						Max:     stopTimeout,
						Idle:    now.Sub(time.Unix(0, c.lastAct.Load())),
					})
				}
			}
			if now.Sub(time.Unix(0, c.lastAct.Load())) >= activityGrace || now.Sub(start) >= stopTimeout {
				// Re-check completion first: a child exiting exactly at the
				// deadline must not be labelled "forcefully" and SIGKILLed
				// into the void within the 250ms tick window.
				select {
				case <-done:
					if c.OnStopEvent != nil {
						c.OnStopEvent(StopStopped)
					}
					return
				default:
				}
				if c.OnStopEvent != nil {
					c.OnStopEvent(StopEscalated)
				}
				c.signalServer(syscall.SIGKILL)
				<-done // the kill always lands; never return with a live child
				return
			}
		}
	}
}

// splitTokenURL separates an attach URL into its base and an optional
// "?token=" value, so users can paste the exact URL odek serve prints.
func splitTokenURL(raw string) (base, token string) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, ""
	}
	q := u.Query()
	token = q.Get("token")
	q.Del("token") // strip only the token; other params must survive
	u.RawQuery = q.Encode()
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
	w    io.Writer
	mu   sync.Mutex
	buf  []byte // partial line not yet terminated by '\n'
	tok  string
	tail []string      // last complete lines, bounded, for failure diagnostics
	act  *atomic.Int64 // when set, stamped on every write (shutdown activity clock)
}

// maxTailLines bounds the stderr tail kept for error reporting.
const maxTailLines = 4

func (s *tokenScanWriter) Write(p []byte) (int, error) {
	s.scan(p)
	if s.act != nil {
		s.act.Store(time.Now().UnixNano())
	}
	return s.w.Write(p)
}

// Token returns the scanned token, or "" if no token line was seen yet.
func (s *tokenScanWriter) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok
}

// Tail returns the last n complete stderr lines, joined for error text.
func (s *tokenScanWriter) Tail(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tail) > n {
		s.tail = s.tail[len(s.tail)-n:]
	}
	return strings.Join(s.tail, "; ")
}

func (s *tokenScanWriter) scan(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok != "" {
		// Token already found: stop parsing, but keep the tail following the
		// server's output so a post-banner failure reaches the error card.
		s.appendTail(p)
		return
	}
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return
		}
		line := string(s.buf[:i])
		s.buf = s.buf[i+1:]
		s.appendTailLine(line)
		if tok := parseTokenLine(line); tok != "" {
			s.tok = tok
			return
		}
	}
}

// appendTail splits p into complete lines and keeps the last maxTailLines
// of them in the diagnostics tail. Callers hold s.mu.
func (s *tokenScanWriter) appendTail(p []byte) {
	// A chunk may start with the tail of a line whose head was buffered by
	// a previous partialTail call — merge before splitting, or the line
	// lands severed in the diagnostics tail.
	if len(s.buf) > 0 {
		p = append(s.buf, p...)
		s.buf = nil
	}
	rest := p
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			s.partialTail(rest)
			return
		}
		s.appendTailLine(string(rest[:i]))
		rest = rest[i+1:]
	}
}

// partialTail buffers a trailing chunk without a newline so a failure line
// split across Write calls still lands whole in the tail.
func (s *tokenScanWriter) partialTail(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	s.buf = append(s.buf, chunk...)
	// Only remember it as a line if it eventually terminates; the next
	// appendTail call re-reads s.buf from the start.
	if i := bytes.IndexByte(s.buf, '\n'); i >= 0 {
		line := string(s.buf[:i])
		s.buf = s.buf[i+1:]
		s.appendTailLine(line)
	}
}

func (s *tokenScanWriter) appendTailLine(line string) {
	s.tail = append(s.tail, line)
	if len(s.tail) > maxTailLines {
		s.tail = s.tail[len(s.tail)-maxTailLines:]
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

// procAlive reports whether the spawned child is still running. Signal(0)
// alone cannot tell an exited-but-unreaped child (zombie) from a live one —
// the ProcessState stays nil until Wait is reaped — so startReaper reaps in
// the background and procAlive consults that result first.
func (c *Conn) procAlive() bool {
	if c.reaped.Load() {
		return false
	}
	return c.proc != nil && c.proc.Process != nil &&
		c.proc.Process.Signal(syscall.Signal(0)) == nil
}

// startReaper waits for the spawned child in the background so its exit is
// observed immediately (no zombie) and procAlive can report death. The
// result feeds Stop's Wait — Stop must never Wait the same Cmd twice.
func (c *Conn) startReaper() {
	if c.proc == nil {
		return
	}
	proc := c.proc
	done := make(chan struct{})
	c.reapDone = done
	go func() {
		_ = proc.Wait()
		c.reaped.Store(true)
		close(done)
	}()
}

// waitSpawned waits until a spawned server answers HTTP or prints its token
// line. Old odek versions print no token, so readiness alone eventually ends
// the wait (the legacy token path handles those) — but only after a short
// grace poll for the token: the HTTP listener can answer before the stderr
// token line flushes, and falling to the legacy probe in that window makes
// /api/models 403 hard-fail a server bodek itself just spawned. A child that
// dies mid-wait fails immediately instead of burning the full timeout.
func waitSpawned(baseURL string, scan *tokenScanWriter, alive func() bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	readyAt := time.Time{}
	for time.Now().Before(deadline) {
		if scan != nil && scan.Token() != "" {
			return nil
		}
		if probeReady(baseURL) {
			if readyAt.IsZero() {
				readyAt = time.Now()
			} else if time.Since(readyAt) >= spawnedTokenGrace {
				return nil // genuinely a tokenless (old) server
			}
		} else if !readyAt.IsZero() {
			readyAt = time.Time{} // flapping: restart the grace clock
		}
		// Re-check the token before declaring death: a fast-exiting
		// (or token-print-and-exit) server can flush its banner after
		// this iteration's top-of-loop check — the token wins.
		if alive != nil && !alive() {
			if scan == nil || scan.Token() == "" {
				err := fmt.Errorf("odek serve exited before becoming ready")
				if tail := stderrTail(scan); tail != nil {
					return fmt.Errorf("%w%w", err, tail)
				}
				return err
			}
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	err := fmt.Errorf("timed out after %s", timeout)
	if tail := stderrTail(scan); tail != nil {
		return fmt.Errorf("%w%w", err, tail)
	}
	return err
}

// stderrTail returns the captured server stderr tail for inclusion in a
// waitSpawned error (bind failures, config errors), or nil when empty.
func stderrTail(scan *tokenScanWriter) error {
	if scan == nil {
		return nil
	}
	if tail := scan.Tail(maxTailLines); tail != "" {
		return fmt.Errorf(": %s", tail)
	}
	return nil
}

// spawnedTokenGrace is how long a ready-but-tokenless spawned server is
// polled for the stderr token line before the legacy path takes over.
const spawnedTokenGrace = 2 * time.Second

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
