package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ── agent-state management REST (memory, skills, tools, config, usage) ─────

// MemoryView is the /api/memory payload: facts by target, their caps, and
// the pending-review episode queue (tainted episodes never auto-replay).
type MemoryView struct {
	Facts map[string][]string `json:"facts"`
	// Episodes.Pending entries carry the stored episode; the promote action
	// only needs the session id.
	Episodes struct {
		Total   int              `json:"total"`
		Pending []PendingEpisode `json:"pending"`
	} `json:"episodes"`
}

// PendingEpisode is one tainted episode awaiting human promotion.
type PendingEpisode struct {
	SessionID string `json:"session_id"`
	Summary   string `json:"summary"`
}

// Memory fetches the memory view.
func (c *Client) Memory() (MemoryView, error) {
	var out MemoryView
	if err := c.getJSON(c.baseURL+"/api/memory", "", &out); err != nil {
		return MemoryView{}, err
	}
	return out, nil
}

// AddMemoryFact appends a fact through the same MemoryManager path the
// agent's memory tool uses (including the unsafe-content filter).
func (c *Client) AddMemoryFact(target, content string) error {
	return c.postAction("/api/memory/facts", map[string]string{"target": target, "content": content})
}

// DeleteMemoryFact removes the matching entry.
func (c *Client) DeleteMemoryFact(target, oldText string) error {
	payload, err := json.Marshal(map[string]string{"target": target, "old_text": oldText})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/api/memory/facts", jsonBody(payload))
	if err != nil {
		return err
	}
	c.authHeaders(req, "")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete fact: status %s", resp.Status)
	}
	return nil
}

// PromoteEpisode promotes a tainted episode to recallable (the human gate).
func (c *Client) PromoteEpisode(sessionID string) error {
	return c.postAction("/api/memory/episodes/promote", map[string]string{"session_id": sessionID})
}

// ConsolidateMemory merges similar facts through the LLM.
func (c *Client) ConsolidateMemory(target string) error {
	return c.postAction("/api/memory/consolidate", map[string]string{"target": target})
}

// Skill is one discovered skill with its provenance state.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AutoLoad    bool   `json:"auto_load"`
	UsageCount  int    `json:"usage_count"`
	Source      string `json:"source"`
	NeedsReview bool   `json:"needs_review"`
	Untrusted   bool   `json:"untrusted"`
}

// Skills lists discovered skills (bodies omitted server-side).
func (c *Client) Skills() ([]Skill, error) {
	var out struct {
		Skills []Skill `json:"skills"`
	}
	if err := c.getJSON(c.baseURL+"/api/skills", "", &out); err != nil {
		return nil, err
	}
	return out.Skills, nil
}

// PromoteSkill clears NeedsReview so a skill can auto-load; tainted skills
// still require force.
func (c *Client) PromoteSkill(name string, force bool) error {
	return c.postAction("/api/skills/promote", map[string]any{"name": name, "force": force})
}

// Tool is one registry entry with its resolved enabled state.
type Tool struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Tools lists the built-in tool registry plus the MCP server count.
func (c *Client) Tools() ([]Tool, int, error) {
	var out struct {
		Tools []Tool `json:"tools"`
		MCP   int    `json:"mcp_servers"`
	}
	if err := c.getJSON(c.baseURL+"/api/tools", "", &out); err != nil {
		return nil, 0, err
	}
	return out.Tools, out.MCP, nil
}

// MCPServer is one configured MCP server with its extension limits (env
// values withheld server-side — they may carry credentials).
type MCPServer struct {
	Name             string   `json:"name"`
	Command          string   `json:"command"`
	Args             []string `json:"args,omitempty"`
	Project          bool     `json:"project,omitempty"`
	AutoApprove      bool     `json:"auto_approve,omitempty"`
	TimeoutSeconds   int      `json:"timeout_seconds,omitempty"`
	MaxResponseBytes int64    `json:"max_response_bytes,omitempty"`
	MaxResultChars   int      `json:"max_result_chars,omitempty"`
}

// MCPServers lists configured MCP servers.
func (c *Client) MCPServers() ([]MCPServer, error) {
	var out struct {
		Servers []MCPServer `json:"servers"`
	}
	if err := c.getJSON(c.baseURL+"/api/mcp", "", &out); err != nil {
		return nil, err
	}
	return out.Servers, nil
}

// Usage is the server-lifetime aggregate.
type Usage struct {
	PromptsStarted   int64   `json:"prompts_started"`
	PromptsCompleted int64   `json:"prompts_completed"`
	PromptsFailed    int64   `json:"prompts_failed"`
	TokensIn         int64   `json:"tokens_in"`
	TokensOut        int64   `json:"tokens_out"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	PricesConfigured bool    `json:"prices_configured"`
	Model            string  `json:"model"`
	WSConnections    int64   `json:"ws_connections"`
	RunsActive       int     `json:"runs_active"`
}

// Usage fetches the lifetime aggregate.
func (c *Client) Usage() (Usage, error) {
	var out Usage
	if err := c.getJSON(c.baseURL+"/api/usage", "", &out); err != nil {
		return Usage{}, err
	}
	return out, nil
}

// Connection is one live WebSocket connection.
type Connection struct {
	ID          string    `json:"id"`
	RemoteAddr  string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	Prompts     int64     `json:"prompts"`
	SessionID   string    `json:"session_id"`
	Model       string    `json:"model"`
	Busy        bool      `json:"busy"`
}

// Connections lists live connections.
func (c *Client) Connections() ([]Connection, error) {
	var out struct {
		Connections []Connection `json:"connections"`
	}
	if err := c.getJSON(c.baseURL+"/api/connections", "", &out); err != nil {
		return nil, err
	}
	return out.Connections, nil
}

// KickConnection closes a connection by id (the handler's defers tear the
// agent and sandbox down cleanly).
func (c *Client) KickConnection(id string) error {
	resp, err := c.do(http.MethodDelete, c.baseURL+"/api/connections/"+url.PathEscape(id), "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("kick: status %s", resp.Status)
	}
	return nil
}

// ConfigView fetches the sanitized resolved-config map (never secrets).
func (c *Client) ConfigView() (map[string]any, error) {
	var out map[string]any
	if err := c.getJSON(c.baseURL+"/api/config", "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Shutdown triggers the server's graceful drain (stop accepting → close
// WebSockets → sandbox cleanup). The socket bodek rides drops moments later.
func (c *Client) Shutdown() error {
	return c.postAction("/api/shutdown", map[string]string{})
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (c *Client) postAction(path string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.postJSON(c.baseURL+path, "", payload)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s: status %s", path, resp.Status)
	}
	return nil
}
