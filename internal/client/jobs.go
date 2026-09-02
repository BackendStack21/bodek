package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ── background jobs (odek v1.38+, /api/jobs surface) ─────────────────────────
//
// Every route takes the caller's session id plus its session auth token —
// the exact /api/cancel contract — so a session can only ever see and stop
// its own jobs. Unknown and foreign ids get the same {"status":"unknown"}
// answer; ErrJobUnknown collapses those cases for the caller.

// Job is one background command from GET /api/jobs. Command is a bounded
// head the server renders (never the full string); ExitCode appears only on
// finished jobs.
type Job struct {
	ID       string  `json:"id"`
	Command  string  `json:"command"`
	Status   string  `json:"status"` // running | exited | failed | timeout | killed
	RuntimeS float64 `json:"runtime_s"`
	ExitCode *int    `json:"exit_code,omitempty"`
}

// JobOutput is one chunk of a job's captured output (32 KiB default).
// NextCursor is the absolute byte cursor for the next chunk; 0 = no more.
type JobOutput struct {
	JobID      string `json:"job_id"`
	Output     string `json:"output"`
	NextCursor int    `json:"next_cursor"`
}

var (
	// ErrJobsUnavailable reports the /api/jobs surface is absent — the
	// connected odek predates v1.38.0 (or a proxy stripped the route).
	ErrJobsUnavailable = errors.New("background commands unavailable (odek ≥ v1.38.0 required)")
	// ErrJobUnknown reports a job id the server cannot see: finished and
	// reaped, owned by another session, or the background manager disabled.
	ErrJobUnknown = errors.New("unknown job")
)

// jobsEnvelope is the GET /api/jobs body.
type jobsEnvelope struct {
	Jobs []Job `json:"jobs"`
}

// Jobs lists the session's background jobs, oldest first.
func (c *Client) Jobs(sessionID, token string) ([]Job, error) {
	resp, err := c.do(http.MethodGet, c.baseURL+"/api/jobs?session_id="+url.QueryEscape(sessionID), token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrJobsUnavailable
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("jobs: status %s", resp.Status)
	}
	var env jobsEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	return env.Jobs, nil
}

// JobOutput fetches one output chunk; since is the previous NextCursor
// (0 = from the top).
func (c *Client) JobOutput(sessionID, token, jobID string, since int) (JobOutput, error) {
	var out JobOutput
	u := fmt.Sprintf("%s/api/jobs/%s/output?session_id=%s&since=%d",
		c.baseURL, url.PathEscape(jobID), url.QueryEscape(sessionID), since)
	resp, err := c.do(http.MethodGet, u, token)
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return out, ErrJobUnknown
	case resp.StatusCode != http.StatusOK:
		return out, fmt.Errorf("job output: status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// stopHTTP lazily builds the stop-path client. odek's stop endpoint answers
// only after the job reaped or hit its SIGTERM grace (stopGrace + 4s — see
// bgproc.Manager.Stop), which the shared 3s client would abort mid-kill.
func (c *Client) stopHTTP() *http.Client {
	c.stopOnce.Do(func() {
		c.stopClient = &http.Client{Timeout: 12 * time.Second}
	})
	return c.stopClient
}

// StopJob kills the job (SIGTERM to the process group, SIGKILL after odek's
// grace). Stopping an already-finished job reports its terminal state;
// unknown or foreign ids return ErrJobUnknown.
func (c *Client) StopJob(sessionID, token, jobID string) (string, error) {
	u := fmt.Sprintf("%s/api/jobs/%s/stop?session_id=%s",
		c.baseURL, url.PathEscape(jobID), url.QueryEscape(sessionID))
	req, err := http.NewRequest(http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	c.authHeaders(req, token)
	resp, err := c.stopHTTP().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrJobUnknown
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("job stop: status %s", resp.Status)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Status, nil
}
