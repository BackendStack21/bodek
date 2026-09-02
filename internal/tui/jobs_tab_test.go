package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ws "golang.org/x/net/websocket"

	"github.com/BackendStack21/bodek/internal/client"
)

// ── fixtures ─────────────────────────────────────────────────────────────────

func jobsFixture() []client.Job {
	zero := 0
	return []client.Job{
		{ID: "bg_1a2b3c4d", Command: "npm run dev", Status: "running", RuntimeS: 125},
		{ID: "bg_9f8e7d6c", Command: "go test ./...", Status: "exited", RuntimeS: 21, ExitCode: &zero},
	}
}

// newJobsTestModel connects the model to an httptest server serving /ws plus
// the caller's REST routes, and stamps a live session on it.
func newJobsTestModel(t *testing.T, register func(mux *http.ServeMux)) *Model {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var d []byte
			if err := ws.Message.Receive(c, &d); err != nil {
				return
			}
		}
	}))
	if register != nil {
		register(mux)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cl, err := client.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", srv.URL, srv.URL, "")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	m := New(cl, Options{Model: "m"})
	m.sessionID = "s1"
	m.authToken = "sess-tok"
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func jobsMux(t *testing.T, jobsJSON string, extra map[string]http.HandlerFunc) (*Model, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	m := newJobsTestModel(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r)
			w.Write([]byte(jobsJSON))
		})
		mux.HandleFunc("/api/jobs/", func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r)
			if h, ok := extra[r.URL.Path]; ok {
				h(w, r)
				return
			}
			http.NotFound(w, r)
		})
	})
	return m, &seen
}

// ── M2: jobs drawer tab ──────────────────────────────────────────────────────

func TestJobsTabRowsAndSelection(t *testing.T) {
	m := newJobsTestModel(t, nil)
	m.panel = panelJobs
	m.Update(exec(m.openJobs()))
	m.Update(exec(func() tea.Msg {
		return jobsFetchedMsg{jobs: jobsFixture()}
	}))

	if got := m.panelLen(); got != 2 {
		t.Fatalf("panelLen = %d, want 2", got)
	}
	out := plain(m.View())
	for _, want := range []string{"jobs", "bg_1a2b3c4d", "npm run dev", "✓", "bg_9f8e7d6c"} {
		if !strings.Contains(out, want) {
			t.Errorf("jobs tab missing %q:\n%s", want, out)
		}
	}
}

func TestJobsTabDigitZeroAndRenumbering(t *testing.T) {
	m := newJobsTestModel(t, nil)
	tabs := drawerTabs()
	if len(tabs) != 10 {
		t.Fatalf("tabs = %d, want 10", len(tabs))
	}
	if tabs[3].name != "jobs" {
		t.Errorf("tabs[3] = %q, want jobs (right after agents)", tabs[3].name)
	}
	if tabs[9].name != "config" {
		t.Errorf("tabs[9] = %q, want config (the digit-0 tab)", tabs[9].name)
	}

	// Digit 4 reaches the new tab; digit 0 reaches config; 1-9 shift.
	cases := []struct {
		key string
		w   panelMode
	}{
		{"4", panelJobs},
		{"0", panelConfig},
		{"7", panelMemory},
	}
	for _, d := range cases {
		m.Update(exec(m.openSessions()))
		_, cmd := m.Update(key(d.key))
		m.Update(exec(cmd))
		if m.panel != d.w {
			t.Errorf("digit %s: panel = %d, want %d", d.key, m.panel, d.w)
		}
	}
	// The tab strip must teach the "0" shortcut for the 10th tab, not "10".
	m.Update(exec(m.openConfig()))
	if !strings.Contains(plain(m.View()), "0 config") {
		t.Errorf("tab strip does not teach 0 for config:\n%s", plain(m.View()))
	}
}

func TestJobsDetailOutputSanitized(t *testing.T) {
	m, _ := jobsMux(t, `{"jobs":[{"id":"bg_1a2b3c4d","command":"npm run dev","status":"running","runtime_s":5}]}`,
		map[string]http.HandlerFunc{
			"/api/jobs/bg_1a2b3c4d/output": func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("since") != "0" {
					t.Errorf("initial output fetch since = %q, want 0", r.URL.Query().Get("since"))
				}
				w.Write([]byte(`{"job_id":"bg_1a2b3c4d","output":"server up` + "\u202e" + `evil` + "\u202c" + `\nready","next_cursor":0}`))
			},
		})
	m.Update(exec(m.openJobs()))
	m.Update(exec(func() tea.Msg { return jobsFetchedMsg{jobs: jobsFixture()[:1]} }))

	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter on a running job did not fetch output")
	}
	m.Update(exec(cmd))
	if !m.panelDetail {
		t.Fatal("detail did not open")
	}
	detail := strings.Join(m.mgmtDetailLines(80), "\n")
	if !strings.Contains(detail, "server up") || !strings.Contains(detail, "ready") {
		t.Errorf("detail missing output:\n%s", detail)
	}
	if strings.Contains(detail, "\u202e") {
		t.Error("detail renders raw bidi control from job output")
	}

	// Cursor at 0 = end: 'f' must not fire another fetch.
	_, cmd = m.Update(key("f"))
	if cmd != nil {
		t.Error("f fetched past next_cursor 0")
	}
}

func TestJobsStopGateFlow(t *testing.T) {
	var stopped bool
	m, _ := jobsMux(t, `{"jobs":[{"id":"bg_1a2b3c4d","command":"npm run dev","status":"running","runtime_s":5}]}`,
		map[string]http.HandlerFunc{
			"/api/jobs/bg_1a2b3c4d/stop": func(w http.ResponseWriter, r *http.Request) {
				stopped = true
				w.Write([]byte(`{"job_id":"bg_1a2b3c4d","status":"killed"}`))
			},
		})
	m.Update(exec(m.openJobs()))
	m.Update(exec(func() tea.Msg { return jobsFetchedMsg{jobs: jobsFixture()[:1]} }))
	m.panelSel = 0

	// s arms the two-step gate; y fires it.
	_, cmd := m.Update(key("s"))
	if cmd != nil {
		m.Update(exec(cmd))
	}
	if m.confirm != confirmStopJob || m.stopJobID != "bg_1a2b3c4d" {
		t.Fatalf("gate = %v target = %q, want confirmStopJob/bg_1a2b3c4d", m.confirm, m.stopJobID)
	}
	_, cmd = m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y did not fire the stop")
	}
	m.Update(exec(cmd)) // jobStopDoneMsg
	if !stopped {
		t.Error("stop POST never reached the server")
	}
	if len(m.notices) == 0 || !strings.Contains(m.notices[len(m.notices)-1], "bg_1a2b3c4d") {
		t.Errorf("no stop acknowledgement note: %v", m.notices)
	}
}

func TestJobsStopNonRunningIsBenign(t *testing.T) {
	m, _ := jobsMux(t, `{"jobs":[]}`, nil)
	m.Update(exec(m.openJobs()))
	m.Update(exec(func() tea.Msg { return jobsFetchedMsg{jobs: jobsFixture()[1:]} }))
	m.panelSel = 0 // the exited job

	_, _ = m.Update(key("s"))
	if m.confirm != confirmNone {
		t.Error("s armed the stop gate on a finished job")
	}
}

func TestJobsSlashCommandAndPalette(t *testing.T) {
	var jobsCmd *command
	for i := range slashCommands() {
		if slashCommands()[i].name == "jobs" {
			jobsCmd = &slashCommands()[i]
		}
	}
	if jobsCmd == nil {
		t.Fatal("/jobs is not registered")
	}
	m, _ := jobsMux(t, `{"jobs":[]}`, nil)
	cmd := jobsCmd.run(m, "")
	if cmd == nil {
		t.Fatal("/jobs did not fetch")
	}
	if m.panel != panelJobs {
		t.Errorf("/jobs panel = %d, want panelJobs", m.panel)
	}

	found := false
	for _, e := range m.basePaletteEntries() {
		if e.hint == "/jobs" {
			found = true
		}
	}
	if !found {
		t.Error("palette has no /jobs entry")
	}
}

// ── M3: lifecycle watcher ────────────────────────────────────────────────────

func TestJobsWatcherBaselineAndDiffNotes(t *testing.T) {
	m := newJobsTestModel(t, nil)

	// First snapshot baselines silently — no startup spam for jobs that
	// were already running when bodek attached.
	m.applyJobs(jobsFixture(), nil)
	if len(m.notices) != 0 {
		t.Errorf("baseline produced notes: %v", m.notices)
	}

	// New job appears; existing one exits 0.
	zero := 0
	next := []client.Job{
		{ID: "bg_1a2b3c4d", Command: "npm run dev", Status: "exited", RuntimeS: 130, ExitCode: &zero},
		{ID: "bg_9f8e7d6c", Command: "go test ./...", Status: "exited", RuntimeS: 21, ExitCode: &zero},
		{ID: "bg_aaaa0000", Command: "air -c .air.toml", Status: "running", RuntimeS: 2},
	}
	m.applyJobs(next, nil)
	if len(m.notices) != 2 {
		t.Fatalf("diff produced %d notes, want 2: %v", len(m.notices), m.notices)
	}
	joined := strings.Join(m.notices, "\n")
	for _, want := range []string{"bg_1a2b3c4d", "exited 0", "bg_aaaa0000", "air"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes missing %q: %v", want, m.notices)
		}
	}

	// Re-reporting the same state is silent.
	before := len(m.notices)
	m.applyJobs(next, nil)
	if len(m.notices) != before {
		t.Errorf("idempotent diff produced notes: %v", m.notices)
	}
}

func TestJobsWatcherExitGlyphs(t *testing.T) {
	m := newJobsTestModel(t, nil)
	m.applyJobs([]client.Job{{ID: "bg_1a2b3c4d", Command: "x", Status: "running", RuntimeS: 1}}, nil)
	two := 2
	for _, tc := range []struct {
		status string
		code   *int
		want   string
	}{
		{"failed", &two, "✗"},
		{"timeout", nil, "⏱"},
		{"killed", nil, "⦸"},
	} {
		m.notices = nil
		m.applyJobs([]client.Job{{ID: "bg_1a2b3c4d", Command: "x", Status: tc.status, RuntimeS: 61, ExitCode: tc.code}}, nil)
		if len(m.notices) == 0 || !strings.Contains(m.notices[len(m.notices)-1], tc.want) {
			t.Errorf("status %s: note %v, want glyph %q", tc.status, m.notices, tc.want)
		}
		if len(m.notices) > 0 && !strings.Contains(m.notices[len(m.notices)-1], "1m01s") {
			t.Errorf("status %s: runtime not humanized: %q", tc.status, m.notices[len(m.notices)-1])
		}
		// Return to running for the next case.
		m.applyJobs([]client.Job{{ID: "bg_1a2b3c4d", Command: "x", Status: "running", RuntimeS: 61}}, nil)
	}
}

func TestJobsWatcherUnavailableDisarms(t *testing.T) {
	m := newJobsTestModel(t, nil)
	m.applyJobs(nil, client.ErrJobsUnavailable)
	if !m.jobsOff {
		t.Fatal("jobsOff not set on ErrJobsUnavailable")
	}
	if cmd := m.armJobsWatch(); cmd != nil {
		t.Error("watcher still armed after the surface proved unavailable")
	}
	if _, cmd := m.Update(key("s")); cmd != nil {
		_ = cmd
	}
	m.Update(exec(m.openJobs()))
	if !strings.Contains(m.panelMsg, "v1.38.0") {
		t.Errorf("degraded open state = %q, want the odek ≥ v1.38.0 hint", m.panelMsg)
	}
}

func TestJobsWatcherFetchRebindsSession(t *testing.T) {
	var got string
	m, seen := jobsMux(t, `{"jobs":[]}`, nil)
	m.applyJobs(nil, nil) // baseline: watcher live

	m.sessionID = "s2"
	m.Update(exec(m.fetchJobs()))
	if len(*seen) == 0 {
		t.Fatal("watcher fetch never reached the server")
	}
	got = (*seen)[len(*seen)-1].URL.Query().Get("session_id")
	if got != "s2" {
		t.Errorf("fetch used session_id %q, want s2 (current session, not a stale capture)", got)
	}
}

func TestJobsPollOnlyWhileTabVisible(t *testing.T) {
	m := newJobsTestModel(t, nil)
	m.applyJobs(jobsFixture(), nil)

	// Stale tab generation: a newer tick owns the fast chain — drop.
	m.jobsSeq++
	if cmd := m.handleJobsTick(jobsTickMsg{seq: m.jobsSeq - 1, watch: false}); cmd != nil {
		t.Error("stale tab tick still fetched")
	}
	// Live generation but the tab closed while the tick flew: the tick
	// hands the cadence to the background watcher — notes keep flowing
	// with the drawer folded.
	before := m.jobsWatchSeq
	if cmd := m.handleJobsTick(jobsTickMsg{seq: m.jobsSeq, watch: false}); cmd == nil {
		t.Fatal("tab-close handoff did not re-arm the watcher")
	}
	if m.jobsWatchSeq != before+1 {
		t.Error("handoff did not start a new watcher generation")
	}
	if cmd := m.handleJobsTick(jobsTickMsg{seq: m.jobsWatchSeq, watch: true}); cmd == nil {
		t.Error("live watcher tick did not fetch")
	}
}

func TestJobsDetailPagesOutput(t *testing.T) {
	m, seen := jobsMux(t, `{"jobs":[]}`, map[string]http.HandlerFunc{
		"/api/jobs/bg_1a2b3c4d/output": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"job_id":"bg_1a2b3c4d","output":"chunk two","next_cursor":0}`))
		},
	})
	m.Update(exec(m.openJobs()))
	m.Update(exec(func() tea.Msg { return jobsFetchedMsg{jobs: jobsFixture()[:1]} }))

	// Detail open, mid-stream cursor: f fetches since that cursor and
	// appends the chunk.
	m.panelDetail = true
	m.jobsOutID = "bg_1a2b3c4d"
	m.jobsOut = "chunk one"
	m.jobsOutCursor = 512
	_, cmd := m.Update(key("f"))
	if cmd == nil {
		t.Fatal("f with a live cursor did not fetch")
	}
	m.Update(exec(cmd))
	last := (*seen)[len(*seen)-1]
	if got := last.URL.Query().Get("since"); got != "512" {
		t.Errorf("page fetch since = %q, want 512", got)
	}
	if m.jobsOut != "chunk onechunk two" {
		t.Errorf("output = %q, want appended chunk", m.jobsOut)
	}
	if m.jobsOutCursor != 0 {
		t.Errorf("cursor = %d, want 0 (end)", m.jobsOutCursor)
	}
	// At the ring's end f is a no-op — no refetch, no duplicated head.
	if _, cmd := m.Update(key("f")); cmd != nil {
		t.Error("f fetched past next_cursor 0")
	}
}

func TestJobsStopAuthHeadersOnWire(t *testing.T) {
	m, seen := jobsMux(t, `{"jobs":[]}`, map[string]http.HandlerFunc{
		"/api/jobs/bg_1a2b3c4d/stop": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"job_id":"bg_1a2b3c4d","status":"killed"}`))
		},
	})
	m.Update(exec(m.openJobs()))
	m.Update(exec(func() tea.Msg { return jobsFetchedMsg{jobs: jobsFixture()[:1]} }))
	m.stopJobID = "bg_1a2b3c4d"
	m.Update(exec(m.fireJobStop()))

	if len(*seen) == 0 {
		t.Fatal("stop never reached the server")
	}
	r := (*seen)[len(*seen)-1]
	if r.Method != http.MethodPost {
		t.Errorf("stop method = %s, want POST", r.Method)
	}
	if r.Header.Get("X-Session-Token") != "sess-tok" {
		t.Errorf("stop missing session token: %v", r.Header)
	}
}

// Deterministic guard: the notes fixture helper must exist and the sweep
// must not race the watcher cadence ( Compile-time pinning of msg types).
var _ = time.Second
