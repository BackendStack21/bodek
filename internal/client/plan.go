package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ── structured plan view (GET /api/sessions/{id}/plan) ──────────────────────
//
// odek serve exposes the engine's plan state read-only: the newest parseable
// "[Current plan:" system message, parsed with the same strict extractor the
// restart-resume path uses. GET-only by contract.

// PlanStepStatus is one step's lifecycle state on the wire.
type PlanStepStatus string

const (
	PlanPending    PlanStepStatus = "pending"
	PlanInProgress PlanStepStatus = "in_progress"
	PlanDone       PlanStepStatus = "done"
	PlanBlocked    PlanStepStatus = "blocked"
)

// PlanStep is one row of the engine's task plan.
type PlanStep struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Status PlanStepStatus `json:"status"`
	Note   string         `json:"note,omitempty"` // omitted when empty
}

// PlanSnapshot is one /plan response. found=false means the transcript carries
// no parseable plan yet; a collapsed all-done plan still reports found=true
// with an empty Steps slice.
type PlanSnapshot struct {
	SessionID string     `json:"session_id"`
	Version   int        `json:"version"`
	Found     bool       `json:"found"`
	Steps     []PlanStep `json:"steps"`
}

// SessionPlan fetches the structured plan of a session. The token is the
// session-scoped auth token (same as cancel/resume); rate limiting and auth
// match every sibling session endpoint.
func (c *Client) SessionPlan(sessionID, sessionToken string) (PlanSnapshot, error) {
	resp, err := c.do(http.MethodGet,
		c.baseURL+"/api/sessions/"+url.PathEscape(sessionID)+"/plan", sessionToken)
	if err != nil {
		return PlanSnapshot{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return PlanSnapshot{}, fmt.Errorf("session plan: status %s", resp.Status)
	}
	var out PlanSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PlanSnapshot{}, fmt.Errorf("session plan: %w", err)
	}
	return out, nil
}
