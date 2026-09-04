package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	ID           string           `json:"id"`
	Model        string           `json:"model"`
	Turns        int              `json:"turns"`
	Task         string           `json:"task"`
	Sandbox      bool             `json:"sandbox"`
	Pinned       bool             `json:"pinned"`
	InputTokens  int64            `json:"input_tokens"`
	OutputTokens int64            `json:"output_tokens"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Messages     []SessionMessage `json:"messages"`
}

// SessionsPage is the /api/sessions envelope returned whenever q, limit, or
// offset is present (a bare GET keeps the legacy array shape).
type SessionsPage struct {
	Sessions []Session `json:"sessions"`
	Offset   int       `json:"offset"`
	Limit    int       `json:"limit"`
	Count    int       `json:"count"`
	Query    string    `json:"query"`
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

// SearchSessions runs the server-side session search: case-insensitive
// substring match over task, model, and id, pinned sessions first. An empty
// query with offset 0 still uses the envelope (limit present → envelope
// shape by contract). count < limit means the listing is exhausted.
func (c *Client) SearchSessions(query string, limit, offset int) (SessionsPage, error) {
	u := fmt.Sprintf("%s/api/sessions?q=%s&limit=%d&offset=%d",
		c.baseURL, url.QueryEscape(query), limit, offset)
	var out SessionsPage
	if err := c.getJSON(u, "", &out); err != nil {
		return SessionsPage{}, err
	}
	return out, nil
}

// UpdateSession renames and/or pins a session via POST /api/sessions/{id}.
// nil leaves a field unchanged; both nil is a 400 server-side.
func (c *Client) UpdateSession(id, token string, name *string, pinned *bool) error {
	body := map[string]any{}
	if name != nil {
		body["name"] = *name
	}
	if pinned != nil {
		body["pinned"] = *pinned
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.postJSON(c.baseURL+"/api/sessions/"+url.PathEscape(id), token, payload)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("update session: status %s", resp.Status)
	}
	return nil
}

// ExportSession downloads a session transcript ("md" renders a standalone
// markdown document, "json" the raw session record).
func (c *Client) ExportSession(id, token, format string) ([]byte, error) {
	u := fmt.Sprintf("%s/api/sessions/%s/export?format=%s",
		c.baseURL, url.PathEscape(id), url.QueryEscape(format))
	resp, err := c.do(http.MethodGet, u, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("export: status %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxExportBytes))
}

// maxExportBytes bounds an exported transcript read into memory.
const maxExportBytes = 64 << 20

// Health is the /api/health server snapshot (never carries secrets).
type Health struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	Model         string    `json:"model"`
	Sandbox       bool      `json:"sandbox"`
	Stream        bool      `json:"stream"`
	WSConnections int64     `json:"ws_connections"`
}

// Health fetches the server snapshot.
func (c *Client) Health() (Health, error) {
	var out Health
	if err := c.getJSON(c.baseURL+"/api/health", "", &out); err != nil {
		return Health{}, err
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

// Models lists the server's model catalog (configured model marked current,
// plus provider ListModels entries). Same shape as odek GET /api/models.
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
	Model           string     `json:"model"`
	Limits          Limits     `json:"limits"`
	EffectivePrices ModelPrice `json:"effective_prices"`
}

// PricesFor returns the effective per-million prices for a model per the
// spec: the server's effective_prices apply to its configured model; any
// other model resolves through model_prices with the flat pair as per-field
// fallback (the client-side twin of odek's limits.ResolvePrices). Zero
// prices mean "costs unavailable" — cost display stays hidden.
func (r LimitsResponse) PricesFor(model string) (inputPerMillion, outputPerMillion float64) {
	if model != "" && model == r.Model {
		if in, out := r.EffectivePrices.InputCostPerMillionUSD,
			r.EffectivePrices.OutputCostPerMillionUSD; in > 0 || out > 0 {
			return in, out
		}
	}
	return r.Limits.ResolvePrices(model)
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

// SubagentEntry is one delegated task's lifecycle record from the serve
// registry snapshot (GET /api/subagents).
type SubagentEntry struct {
	TaskID          string    `json:"task_id"`
	RunKey          string    `json:"run_key"`
	Goal            string    `json:"goal,omitempty"`
	Status          string    `json:"status,omitempty"`
	Phase           string    `json:"phase"`
	PID             int       `json:"pid,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	Iterations      int       `json:"iterations,omitempty"`
	Step            int       `json:"step,omitempty"`
	LastTool        string    `json:"last_tool,omitempty"`
	DurationSeconds float64   `json:"duration_seconds,omitempty"`
	TokensUsed      int       `json:"tokens_used,omitempty"`

	// wire v2 — omitted when the engine predates it
	Profile          string          `json:"profile,omitempty"`
	MaxRisk          string          `json:"max_risk,omitempty"`
	BudgetSeconds    int             `json:"budget_seconds,omitempty"`
	BudgetIterations int             `json:"budget_iterations,omitempty"`
	CostUSD          float64         `json:"cost_usd,omitempty"`
	BudgetCostUSD    float64         `json:"budget_cost_usd,omitempty"`
	Artifacts        []StateArtifact `json:"artifacts,omitempty"`
}

// Subagents fetches the sub-agent registry snapshot, optionally filtered by
// run key. Auth mirrors the other instance-level GETs.
func (c *Client) Subagents(runKey string) ([]SubagentEntry, error) {
	u := c.baseURL + "/api/subagents"
	if runKey != "" {
		u += "?key=" + url.QueryEscape(runKey)
	}
	var out struct {
		Entries []SubagentEntry `json:"entries"`
		Count   int             `json:"count"`
	}
	if err := c.getJSON(u, "", &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

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

// postJSON issues a JSON POST with the standard token headers attached.
func (c *Client) postJSON(u, sessionToken string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authHeaders(req, sessionToken)
	return c.http.Do(req)
}

// jsonBody wraps a JSON payload for arbitrary-method request bodies.
func jsonBody(payload []byte) *bytes.Reader {
	return bytes.NewReader(payload)
}

// authHeaders stamps the instance (and optional session) tokens on a request.
func (c *Client) authHeaders(req *http.Request, sessionToken string) {
	if c.serveToken != "" {
		req.Header.Set("X-Odek-Ws-Token", c.serveToken)
	}
	if sessionToken != "" {
		req.Header.Set("X-Session-Token", sessionToken)
	}
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
