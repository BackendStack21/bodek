package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ws "golang.org/x/net/websocket"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestShortenHome covers the $HOME replacement arms: exact home, nested path,
// foreign path, blank input, and the UserHomeDir error path ($HOME unset).
func TestShortenHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{home, "~"},
		{home + "/sub/dir", "~/sub/dir"},
		{"/elsewhere", "/elsewhere"},
	}
	for _, c := range cases {
		if got := shortenHome(c.in); got != c.want {
			t.Errorf("shortenHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Without $HOME, os.UserHomeDir errors and the path is returned verbatim.
	t.Setenv("HOME", "")
	if got := shortenHome("/abs/path"); got != "/abs/path" {
		t.Errorf("shortenHome without $HOME = %q", got)
	}
}

// TestWelcomeWithCWD exercises the working-directory line of the splash.
func TestWelcomeWithCWD(t *testing.T) {
	out := plain(welcome(newTheme(), 80, "/nonexistent/workdir"))
	if !strings.Contains(out, "/nonexistent/workdir") {
		t.Error("welcome splash missing the cwd line")
	}
}

func TestResourceGlyph(t *testing.T) {
	cases := map[string]string{
		"command": "/",
		"session": "⟳",
		"skill":   "✦",
		"file":    "≡",
		"other":   "≡",
	}
	for typ, want := range cases {
		if got := resourceGlyph(typ); got != want {
			t.Errorf("resourceGlyph(%q) = %q, want %q", typ, got, want)
		}
	}
}

// TestRunStatsCommand drives the /stats registry closure through runCommand.
func TestRunStatsCommand(t *testing.T) {
	m := wired(t)
	if cmd := m.runCommand("stats", ""); cmd != nil {
		t.Error("/stats should be a local command with no tea.Cmd")
	}
	if m.panel != panelStats {
		t.Error("/stats did not open the dashboard sheet")
	}
}

// TestShowStatsBranches hits the /stats card's conditional arms: the >100%
// context clamp, the slowest-turn latency suffix, thinking on, the narrow-width
// model budget floor, and the session-id title suffix.
func TestShowStatsBranches(t *testing.T) {
	m := newTestModel()
	m.resize(30, 30) // innerW = 24 → model budget falls below the 8-col floor
	m.thinkOn = true
	m.sessionID = "sess-123"
	m.maxContext = 100
	m.winCtxTok = 250 // 250% → clamped to 100%
	m.sessionStart = time.Now().Add(-time.Minute)
	m.turnStats = []turnStats{
		{latency: 1.0, thought: true},
		{latency: 6.0}, // peak 6.0 > mean 3.5 + 0.05 → "slowest" suffix
	}
	m.showStats()
	if m.panel != panelStats {
		t.Fatal("showStats did not open the sheet")
	}
	out := plain(m.View())
	for _, want := range []string{"sess-123", "slowest", "6.0s", "think on", "100%"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats card missing %q", want)
		}
	}
}

// TestEscWhenIdle covers the esc key with no run in flight (a no-op).
func TestEscWhenIdle(t *testing.T) {
	m := wired(t)
	m.Update(key("esc"))
	if m.quitting {
		t.Error("idle esc should not quit")
	}
}

// TestCtrlPanelKeys covers the ^R (sessions) and ^O (models) keybindings.
func TestCtrlPanelKeys(t *testing.T) {
	m := wired(t)

	_, cmd := m.Update(key("ctrl+r"))
	m.Update(exec(cmd))
	if m.panel != panelSessions {
		t.Fatalf("ctrl+r did not open sessions panel: %d", m.panel)
	}
	m.Update(key("esc"))

	_, cmd = m.Update(key("ctrl+o"))
	m.Update(exec(cmd))
	if m.panel != panelModels {
		t.Fatalf("ctrl+o did not open models panel: %d", m.panel)
	}
	m.Update(key("esc"))
}

// TestErrorEventWithoutActiveMessage covers the error-event fallback: with no
// streaming message to hold the error it becomes a notice.
func TestErrorEventWithoutActiveMessage(t *testing.T) {
	m := wired(t)
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if len(m.notices) == 0 || !strings.Contains(m.notices[len(m.notices)-1], "boom") {
		t.Errorf("error notice missing: %v", m.notices)
	}
}

// TestDisconnectWithLogPath covers the "server log" hint appended on
// disconnect when the spawned server captured stderr to a file.
func TestDisconnectWithLogPath(t *testing.T) {
	m := wired(t)
	m.opts.LogPath = "/tmp/odek-serve.log"
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "server log") && strings.Contains(n, "/tmp/odek-serve.log") {
			found = true
		}
	}
	if !found {
		t.Errorf("server-log notice missing: %v", m.notices)
	}
}

// TestSubmitThinkingEnabled covers the thinking=enabled prompt option.
func TestSubmitThinkingEnabled(t *testing.T) {
	m := wired(t)
	m.thinkOn = true
	m.ta.SetValue("hi")
	exec(m.submit())
	if !m.busy {
		t.Fatal("model not busy after submit")
	}
	deadline := time.After(3 * time.Second)
	for m.busy {
		select {
		case ev := <-m.cl.Events:
			m.handleEvent(ev)
		case <-deadline:
			t.Fatal("did not receive done")
		}
	}
}

// TestStepGlyphsDedupAndCap covers duplicate-glyph skipping and the 4-glyph
// cap in one pass.
func TestStepGlyphsDedupAndCap(t *testing.T) {
	steps := []step{
		{name: "shell"},      // ❯
		{name: "bash_tool"},  // ❯ again — deduped
		{name: "read_file"},  // ◰
		{name: "list_dir"},   // ▤
		{name: "grep"},       // ⌕ — fourth distinct glyph
		{name: "write_file"}, // ✎ — beyond the cap
	}
	got := stepGlyphs(steps)
	if len(got) != 4 {
		t.Fatalf("stepGlyphs = %v (%d), want 4 glyphs", got, len(got))
	}
	if got[0] != "❯" || got[1] != "◰" || got[2] != "▤" || got[3] != "⌕" {
		t.Errorf("stepGlyphs order/dedup wrong: %v", got)
	}
}

// TestFetchModelsCmd executes the startup model-list fetch against the stand-in.
func TestFetchModelsCmd(t *testing.T) {
	m := wired(t)
	msg, ok := exec(m.fetchModels()).(modelsMsg)
	if !ok {
		t.Fatal("fetchModels did not return a modelsMsg")
	}
	if msg.err != nil || len(msg.items) != 2 {
		t.Errorf("fetchModels = %d items, err %v", len(msg.items), msg.err)
	}
}

// TestRelayoutNotReady covers the early return before the first resize.
func TestRelayoutNotReady(t *testing.T) {
	m := &Model{}
	m.relayout() // must not panic or size anything
	if m.ready {
		t.Error("relayout marked an unready model ready")
	}
}

// TestSyncACSameCommandQuery covers the no-op when the command prefix is
// unchanged since the popup was opened.
func TestSyncACSameCommandQuery(t *testing.T) {
	m := wired(t)
	m.ta.SetValue("/st")
	if cmd := m.syncAC(); cmd != nil {
		t.Error("opening the command popup should not spawn a cmd")
	}
	if !m.ac.open || m.ac.mode != acCmd {
		t.Fatal("command popup did not open")
	}
	if cmd := m.syncAC(); cmd != nil {
		t.Error("unchanged command query should be a no-op")
	}
}

// TestSyncACTrimsToSixFiles covers the >6 file trim in the @-search result,
// against a stand-in that over-fetches nine files plus a non-file resource.
func TestSyncACTrimsToSixFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Server{Handler: ws.Handler(func(c *ws.Conn) {
		for {
			var d []byte
			if err := ws.Message.Receive(c, &d); err != nil {
				return
			}
		}
	})})
	mux.HandleFunc("/api/resources", func(w http.ResponseWriter, r *http.Request) {
		rs := []client.Resource{{ID: "@sess:s1", Type: "session", Label: "s1"}}
		for i := 0; i < 9; i++ {
			rs = append(rs, client.Resource{
				ID: fmt.Sprintf("@f%d.go", i), Type: "file", Label: fmt.Sprintf("f%d.go", i),
			})
		}
		json.NewEncoder(w).Encode(rs)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cl, err := client.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", srv.URL, srv.URL, "")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	m := New(cl, Options{Model: "m"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.ta.SetValue("see @f")
	msg, ok := exec(m.syncAC()).(acResultMsg)
	if !ok {
		t.Fatal("syncAC did not return an acResultMsg")
	}
	if len(msg.items) != 6 {
		t.Errorf("@-search kept %d items, want 6 (files only, capped)", len(msg.items))
	}
}

// TestElapsedMinutes covers the ≥1-minute branch of the live run badge.
func TestElapsedMinutes(t *testing.T) {
	m := newTestModel()
	m.runStart = time.Now().Add(-65 * time.Second)
	if got := m.elapsed(); got != "1m05s" {
		t.Errorf("elapsed = %q, want 1m05s", got)
	}
}

// TestCapThinkingRuneBoundary covers the rune-count early return: a string
// over the byte cap but under the rune cap is left untouched.
func TestCapThinkingRuneBoundary(t *testing.T) {
	if got := capThinkingText("ééé", 4); got != "ééé" {
		t.Errorf("capThinkingText trimmed a rune-fitting string to %q", got)
	}
}

// TestCtxGaugeClamps covers the negative and >100% ratio clamps in the header
// gauge.
func TestCtxGaugeClamps(t *testing.T) {
	m := newTestModel()
	m.maxContext = 100

	m.winCtxTok = -5 // corrupt/negative accounting → clamp to 0%
	if out := plain(m.ctxGauge(true)); !strings.Contains(out, "0%") {
		t.Errorf("negative ratio gauge = %q, want 0%%", out)
	}

	m.winCtxTok = 250 // overrun → clamp to 100%
	if out := plain(m.ctxGauge(false)); !strings.Contains(out, "100%") {
		t.Errorf("overrun ratio gauge = %q, want 100%%", out)
	}
}

// TestGaugeColorBands covers all three pressure tints.
func TestGaugeColorBands(t *testing.T) {
	m := newTestModel()
	// Not a TTY in tests, so compare the style colors rather than the output.
	hot := m.gaugeColor(0.95).GetForeground()
	warm := m.gaugeColor(0.80).GetForeground()
	ok := m.gaugeColor(0.10).GetForeground()
	if hot == warm || warm == ok || hot == ok {
		t.Error("gauge bands should use distinct tints")
	}
}

// TestAcPopupCmdModeAndNarrow covers the command-mode label/empty-text arms
// and the inner-width floor on very narrow terminals.
func TestAcPopupCmdModeAndNarrow(t *testing.T) {
	m := newTestModel()
	m.width = 17 // innerW = 11 → clamped to 12
	m.ac = autocomplete{open: true, mode: acCmd, items: []client.Resource{
		{ID: "/help", Type: "command", Label: "/help", Detail: "show available commands"},
	}}
	if out := plain(m.acPopup()); !strings.Contains(out, "commands") {
		t.Error("command-mode popup missing its label")
	}

	m.ac.items = nil
	if out := plain(m.acPopup()); !strings.Contains(out, "no matching") {
		t.Error("empty command popup missing its placeholder")
	}
}

// TestFooterNarrowGap covers the gap-floor when the right-hand segments are
// wider than the terminal.
func TestFooterNarrowGap(t *testing.T) {
	m := newTestModel()
	m.width = 5
	m.lastLatency = 2.5
	m.turnStats = []turnStats{{outTok: 12}}
	_ = m.footer() // must not panic on a negative gap
}
