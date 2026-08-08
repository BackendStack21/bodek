package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SessionMessage is one message in a saved session transcript.
type SessionMessage struct {
	Role             string            `json:"role"`
	Content          string            `json:"content"`
	Name             string            `json:"name,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ToolCalls        []SessionToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
}

// SessionToolCall is one persisted tool invocation (OpenAI wire format).
type SessionToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
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

// ModelPrice holds per-model token prices (USD per million tokens). A zero
// field falls back to the flat price in Limits.
type ModelPrice struct {
	InputCostPerMillionUSD  float64 `json:"input_cost_per_million_usd,omitempty"`
	OutputCostPerMillionUSD float64 `json:"output_cost_per_million_usd,omitempty"`
}

// Limits mirrors odek's budget.Limits as served by /api/limits. odek never
// hard-codes provider prices, so a zero price means "not configured" — cost
// display must stay hidden rather than report $0.
type Limits struct {
	MaxRuntimeSeconds       int64                 `json:"max_runtime_seconds,omitempty"`
	MaxToolCalls            int64                 `json:"max_tool_calls,omitempty"`
	MaxInputTokens          int64                 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens         int64                 `json:"max_output_tokens,omitempty"`
	MaxCostUSD              float64               `json:"max_cost_usd,omitempty"`
	InputCostPerMillionUSD  float64               `json:"input_cost_per_million_usd,omitempty"`
	OutputCostPerMillionUSD float64               `json:"output_cost_per_million_usd,omitempty"`
	ModelPrices             map[string]ModelPrice `json:"model_prices,omitempty"`
}

// ResolvePrices returns the effective per-million token prices for a model:
// exact per-model overrides win per field over the flat pair, mirroring
// odek's budget.Limits.ResolvePrices.
func (l Limits) ResolvePrices(model string) (inputPerMillion, outputPerMillion float64) {
	inputPerMillion, outputPerMillion = l.InputCostPerMillionUSD, l.OutputCostPerMillionUSD
	if p, ok := l.ModelPrices[model]; ok {
		if p.InputCostPerMillionUSD > 0 {
			inputPerMillion = p.InputCostPerMillionUSD
		}
		if p.OutputCostPerMillionUSD > 0 {
			outputPerMillion = p.OutputCostPerMillionUSD
		}
	}
	return inputPerMillion, outputPerMillion
}

// LimitsResponse is the /api/limits payload.
type LimitsResponse struct {
	Model  string `json:"model"`
	Limits Limits `json:"limits"`
}

// Limits fetches the server's budget limits and configured token prices.
func (c *Client) Limits() (LimitsResponse, error) {
	var out LimitsResponse
	if err := c.getJSON(c.baseURL+"/api/limits", "", &out); err != nil {
		return LimitsResponse{}, err
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
	if c.serveToken != "" {
		req.Header.Set("X-Odek-Ws-Token", c.serveToken)
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
