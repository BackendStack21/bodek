package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ── headless runs + observability (WEBUI.md "Building an external client") ──

// Run is one headless REST run (POST /api/prompt → poll GET /api/runs/{id}).
type Run struct {
	ID                string         `json:"id"`
	SessionID         string         `json:"session_id"`
	Model             string         `json:"model"`
	Status            string         `json:"status"` // running | waiting_approval | completed | failed | cancelled
	StartedAt         time.Time      `json:"started_at"`
	EndedAt           time.Time      `json:"ended_at,omitempty"`
	InputTokens       int64          `json:"input_tokens,omitempty"`
	OutputTokens      int64          `json:"output_tokens,omitempty"`
	Result            string         `json:"result,omitempty"`
	Error             string         `json:"error,omitempty"`
	PendingApprovals  []RunApproval  `json:"pending_approvals"`
}

// Terminal reports whether the run has settled (no further polling needed).
func (r Run) Terminal() bool {
	switch r.Status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

// RunApproval is one pending remote approval on a headless run. Friction
// semantics mirror the interactive approval_request.
type RunApproval struct {
	ID                string `json:"id"`
	Risk              string `json:"risk"`
	Command           string `json:"command"`
	Description       string `json:"description,omitempty"`
	AllowTrust        bool   `json:"allow_trust"`
	Friction          bool   `json:"friction"`
	FrictionApprovals int    `json:"friction_approvals,omitempty"`
}

// RuntimeEvent is one odek.event/v1 record from the /api/events ring.
// Payloads carry SHA-256 arg hashes and redacted fields only — by design.
type RuntimeEvent struct {
	Schema    string         `json:"schema"`
	Type      string         `json:"type"`
	RunID     string         `json:"run_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Iteration int            `json:"iteration,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// RunOpts are the optional parameters of a headless run.
type RunOpts struct {
	SessionID              string
	AuthToken              string
	Model                  string
	Thinking               string
	ApprovalTimeoutSeconds int
	Attachments            []Attachment
}

// StartRun submits a headless prompt (POST /api/prompt). The run executes
// the exact interactive prompt path server-side; poll RunDetail for status.
func (c *Client) StartRun(content string, opts RunOpts) (Run, error) {
	body := map[string]any{"content": content}
	if opts.SessionID != "" {
		body["session_id"] = opts.SessionID
	}
	if opts.AuthToken != "" {
		body["auth_token"] = opts.AuthToken
	}
	if opts.Model != "" {
		body["model"] = opts.Model
	}
	if opts.Thinking != "" {
		body["thinking"] = opts.Thinking
	}
	if opts.ApprovalTimeoutSeconds > 0 {
		body["approval_timeout_seconds"] = opts.ApprovalTimeoutSeconds
	}
	if len(opts.Attachments) > 0 {
		body["attachments"] = opts.Attachments
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Run{}, err
	}
	resp, err := c.postJSON(c.baseURL+"/api/prompt", opts.AuthToken, payload)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return Run{}, fmt.Errorf("start run: status %s", resp.Status)
	}
	var started struct {
		RunID     string `json:"run_id"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		return Run{}, err
	}
	return Run{ID: started.RunID, SessionID: started.SessionID, Status: started.Status}, nil
}

// Runs lists recent headless runs, newest first.
func (c *Client) Runs() ([]Run, error) {
	var out struct {
		Runs []Run `json:"runs"`
	}
	if err := c.getJSON(c.baseURL+"/api/runs", "", &out); err != nil {
		return nil, err
	}
	return out.Runs, nil
}

// RunDetail loads one run incl. pending approvals.
func (c *Client) RunDetail(id string) (Run, error) {
	var run Run
	if err := c.getJSON(c.baseURL+"/api/runs/"+url.PathEscape(id), "", &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

// CancelRun aborts a headless run (POST /api/runs/{id}/cancel — the DELETE
// twin reports idle the same way).
func (c *Client) CancelRun(id string) error {
	resp, err := c.do(http.MethodPost, c.baseURL+"/api/runs/"+url.PathEscape(id)+"/cancel", "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cancel run: status %s", resp.Status)
	}
	return nil
}

// AnswerRunApproval answers one pending remote approval (approve|deny|trust)
// through the same wsApprover path as the interactive surface.
func (c *Client) AnswerRunApproval(runID, approvalID, action string) error {
	payload, err := json.Marshal(map[string]string{"action": action})
	if err != nil {
		return err
	}
	resp, err := c.postJSON(c.baseURL+"/api/runs/"+url.PathEscape(runID)+"/approvals/"+url.PathEscape(approvalID), "", payload)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("answer approval: status %s", resp.Status)
	}
	return nil
}

// Events reads the recent runtime-event ring (oldest-first, filtered).
func (c *Client) RuntimeEvents(limit int) ([]RuntimeEvent, error) {
	u := fmt.Sprintf("%s/api/events?limit=%d", c.baseURL, limit)
	var out struct {
		Events []RuntimeEvent `json:"events"`
		Count  int            `json:"count"`
	}
	if err := c.getJSON(u, "", &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}
