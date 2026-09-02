package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testClient wires a Client at srv with a fast shared client; the stop path
// gets its own timeout budget, asserted separately.
func testClient(srv *httptest.Server) *Client {
	return &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
}

func TestJobsListWireShape(t *testing.T) {
	var gotPath, gotSid, gotSessionTok, gotCSRF string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSid = r.URL.Query().Get("session_id")
		gotSessionTok = r.Header.Get("X-Session-Token")
		gotCSRF = r.Header.Get("X-Odek-Ws-Token")
		// The envelope odek v1.38 actually sends: {"jobs":[...]} — a bare
		// array would be a decode failure, which is exactly what this pins.
		w.Write([]byte(`{"jobs":[{"id":"bg_1a2b3c4d","command":"npm run dev","status":"running","runtime_s":12.5},` +
			`{"id":"bg_9f8e7d6c","command":"go test ./...","status":"exited","runtime_s":21.0,"exit_code":0}]}`))
	}))
	defer srv.Close()

	c := testClient(srv)
	c.serveToken = "csrf"
	jobs, err := c.Jobs("s1", "sess-tok")
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if gotPath != "/api/jobs" || gotSid != "s1" {
		t.Errorf("request line: path=%q session_id=%q", gotPath, gotSid)
	}
	if gotSessionTok != "sess-tok" || gotCSRF != "csrf" {
		t.Errorf("auth headers: session=%q csrf=%q", gotSessionTok, gotCSRF)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
	j := jobs[0]
	if j.ID != "bg_1a2b3c4d" || j.Command != "npm run dev" || j.Status != "running" || j.RuntimeS != 12.5 {
		t.Errorf("job[0] = %+v", j)
	}
	if j.ExitCode != nil {
		t.Errorf("running job exit_code = %v, want nil", *j.ExitCode)
	}
	if jobs[1].ExitCode == nil || *jobs[1].ExitCode != 0 {
		t.Errorf("exited job exit_code = %v, want 0", jobs[1].ExitCode)
	}
}

func TestJobsListUnavailableOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // odek < v1.38.0: the route does not exist
	}))
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.Jobs("s1", "tok"); !errors.Is(err, ErrJobsUnavailable) {
		t.Errorf("Jobs 404 err = %v, want ErrJobsUnavailable", err)
	}
}

func TestJobOutputWireShape(t *testing.T) {
	var gotPath, gotSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSince = r.URL.Query().Get("since")
		w.Write([]byte(`{"job_id":"bg_1a2b3c4d","output":"line one\nline two","next_cursor":2048}`))
	}))
	defer srv.Close()
	c := testClient(srv)

	out, err := c.JobOutput("s1", "tok", "bg_1a2b3c4d", 7)
	if err != nil {
		t.Fatalf("JobOutput: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/api/jobs/bg_1a2b3c4d/output") || gotSince != "7" {
		t.Errorf("request line: path=%q since=%q", gotPath, gotSince)
	}
	if out.JobID != "bg_1a2b3c4d" || out.Output != "line one\nline two" || out.NextCursor != 2048 {
		t.Errorf("output = %+v", out)
	}
}

func TestJobOutputUnknownOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"job_id":"bg_x","status":"unknown"}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.JobOutput("s1", "tok", "bg_x", 0); !errors.Is(err, ErrJobUnknown) {
		t.Errorf("JobOutput 404 err = %v, want ErrJobUnknown", err)
	}
}

func TestStopJobWireShape(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Write([]byte(`{"job_id":"bg_1a2b3c4d","status":"killed"}`))
	}))
	defer srv.Close()
	c := testClient(srv)

	status, err := c.StopJob("s1", "tok", "bg_1a2b3c4d")
	if err != nil {
		t.Fatalf("StopJob: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/api/jobs/bg_1a2b3c4d/stop") {
		t.Errorf("request line: %s %s", gotMethod, gotPath)
	}
	if status != "killed" {
		t.Errorf("status = %q, want killed", status)
	}
}

func TestStopJobUnknownOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"job_id":"bg_x","status":"unknown"}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.StopJob("s1", "tok", "bg_x"); !errors.Is(err, ErrJobUnknown) {
		t.Errorf("StopJob 404 err = %v, want ErrJobUnknown", err)
	}
}

// The stop endpoint blocks up to odek's SIGTERM grace (5s) + margin before
// answering (bgproc.Manager.Stop waits stopGrace+4s); the shared 3s client
// would abort a perfectly good stop. The stop path carries its own budget.
func TestStopJobTimeoutBudget(t *testing.T) {
	c := &Client{http: &http.Client{Timeout: time.Second}}
	if c.stopHTTP() == nil {
		t.Fatal("stopHTTP is nil")
	}
	if budget := c.stopHTTP().Timeout; budget < 10*time.Second {
		t.Errorf("stop timeout budget = %v, want ≥ 10s (odek stop blocks stopGrace+4s)", budget)
	}
}

func TestJobsRequestErrors(t *testing.T) {
	c := unreachableClient()
	if _, err := c.Jobs("s1", "tok"); err == nil {
		t.Error("Jobs should error on unreachable server")
	}
	if _, err := c.JobOutput("s1", "tok", "bg_x", 0); err == nil {
		t.Error("JobOutput should error on unreachable server")
	}
	if _, err := c.StopJob("s1", "tok", "bg_x"); err == nil {
		t.Error("StopJob should error on unreachable server")
	}
}
