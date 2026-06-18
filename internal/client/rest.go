package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SessionMessage is one turn in a saved session transcript.
type SessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Session is a saved conversation, as returned by the session API.
type Session struct {
	ID        string           `json:"id"`
	Model     string           `json:"model"`
	Turns     int              `json:"turns"`
	Task      string           `json:"task"`
	Sandbox   bool             `json:"sandbox"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Messages  []SessionMessage `json:"messages"`
}

// ModelInfo describes an available model from the models API.
type ModelInfo struct {
	ID          string `json:"id"`
	MaxContext  int    `json:"max_context"`
	Description string `json:"description"`
	Current     bool   `json:"current"`
}

// Sessions lists recent saved sessions (auth tokens are not included).
func (c *Client) Sessions() ([]Session, error) {
	var out []Session
	if err := c.getJSON(c.baseURL+"/api/sessions", "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SessionDetail loads a full session transcript. Pass the session's known
// auth token (empty is accepted for sessions that have never been tokened). It
// returns the effective token from the X-Session-Token response header, falling
// back to the token passed in.
func (c *Client) SessionDetail(id, token string) (Session, string, error) {
	var s Session
	resp, err := c.do(http.MethodGet, c.baseURL+"/api/sessions/"+url.PathEscape(id), token)
	if err != nil {
		return s, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return s, "", fmt.Errorf("session: status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return s, "", err
	}
	eff := resp.Header.Get("X-Session-Token")
	if eff == "" {
		eff = token
	}
	return s, eff, nil
}

// DeleteSession removes a saved session (requires its auth token).
func (c *Client) DeleteSession(id, token string) error {
	resp, err := c.do(http.MethodDelete, c.baseURL+"/api/sessions/"+url.PathEscape(id), token)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete: status %s", resp.Status)
	}
	return nil
}

// Models lists models advertised by the server.
func (c *Client) Models() ([]ModelInfo, error) {
	var out []ModelInfo
	if err := c.getJSON(c.baseURL+"/api/models", "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Cancel aborts the in-flight prompt for a session (requires its auth token).
func (c *Client) Cancel(sessionID, token string) error {
	u := c.baseURL + "/api/cancel?session_id=" + url.QueryEscape(sessionID)
	resp, err := c.do(http.MethodPost, u, token)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cancel: status %s", resp.Status)
	}
	return nil
}

// ── low-level helpers ────────────────────────────────────────────────────────

func (c *Client) do(method, u, sessionToken string) (*http.Response, error) {
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	if sessionToken != "" {
		req.Header.Set("X-Session-Token", sessionToken)
	}
	return c.http.Do(req)
}

func (c *Client) getJSON(u, sessionToken string, dst interface{}) error {
	resp, err := c.do(http.MethodGet, u, sessionToken)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
