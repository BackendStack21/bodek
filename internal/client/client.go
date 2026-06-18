// Package client speaks odek serve's WebSocket protocol.
//
// bodek does not re-implement any agent logic. It connects to a running
// `odek serve` instance and renders the events it streams — tokens, tool
// calls, approvals, skills, memory — so the full odek engine (tools, danger
// gating, sandbox, skills, memory, sessions) is reused as-is. This file is the
// thin transport + event-decoding layer between that engine and the TUI.
package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	ws "golang.org/x/net/websocket"
)

// Event is a decoded server→client message. It is a union of every event the
// odek serve protocol emits; only the fields relevant to Type are populated.
type Event struct {
	Type string `json:"type"`

	// tool_call / tool_result / subagent_log
	Name string `json:"name"`
	Data string `json:"data"`

	// token / thinking
	Content string `json:"content"`

	// error
	Message string `json:"message"`

	// session
	SessionID string `json:"session_id"`
	AuthToken string `json:"auth_token"`
	Model     string `json:"model"`
	Sandbox   bool   `json:"sandbox"`

	// done — token economics for the turn and the session
	Latency              float64 `json:"latency"`
	ContextTokens        int     `json:"contextTokens"`
	OutputTokens         int     `json:"outputTokens"`
	SessionContextTokens int     `json:"sessionContextTokens"`
	SessionOutputTokens  int     `json:"sessionOutputTokens"`

	// approval_request
	ID          string `json:"id"`
	Risk        string `json:"risk"`
	Command     string `json:"command"`
	Description string `json:"description"`
	IsOperation bool   `json:"is_operation"`
	AllowTrust  bool   `json:"allow_trust"`

	// skill_event / memory_event / agent_signal / subagent_log: the event
	// subtype (e.g. "loaded", "merge", "trim") plus a few shared details.
	SubType   string `json:"event"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	SkillName string `json:"skill_name"`
	Untrusted bool   `json:"untrusted"`
	Count     int    `json:"count"`
	TaskIdx   int    `json:"task_idx"`
}

// EventDisconnected is a synthetic Type emitted on the Events channel when the
// socket closes, so the TUI can react instead of hanging.
const EventDisconnected = "_disconnected"

// Resource is a single @-reference completion candidate from /api/resources.
type Resource struct {
	ID     string `json:"id"`     // full reference, e.g. "@src/main.go"
	Type   string `json:"type"`   // "file" | "session" | "skill"
	Label  string `json:"label"`  // display label
	Detail string `json:"detail"` // one-line description
}

// Client is a connected odek serve session.
type Client struct {
	conn    *ws.Conn
	baseURL string
	http    *http.Client
	Events  chan Event
}

// Dial connects to an odek serve WebSocket. wsURL is the ws:// endpoint,
// origin is an http://localhost-based origin accepted by the server, baseURL is
// the http:// root (used for the resource-search API), and token is the
// per-instance CSRF token (obtained from a GET / Set-Cookie header).
func Dial(wsURL, origin, baseURL, token string) (*Client, error) {
	cfg, err := ws.NewConfig(wsURL, origin)
	if err != nil {
		return nil, fmt.Errorf("ws config: %w", err)
	}
	cfg.Header.Set("X-Odek-Ws-Token", token)

	conn, err := ws.DialConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}

	c := &Client{
		conn:    conn,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 3 * time.Second},
		Events:  make(chan Event, 256),
	}
	go c.readLoop()
	return c, nil
}

// Resources queries the server's @-reference completion endpoint.
func (c *Client) Resources(query string, limit int) ([]Resource, error) {
	u := fmt.Sprintf("%s/api/resources?q=%s&limit=%d",
		c.baseURL, url.QueryEscape(query), limit)
	resp, err := c.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resources: status %s", resp.Status)
	}
	var out []Resource
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// readLoop decodes frames into Events until the socket closes.
func (c *Client) readLoop() {
	defer close(c.Events)
	for {
		var data []byte
		if err := ws.Message.Receive(c.conn, &data); err != nil {
			c.Events <- Event{Type: EventDisconnected}
			return
		}
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // ignore malformed frames
		}
		c.Events <- ev
	}
}

// prompt is the client→server prompt message.
type prompt struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking,omitempty"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	AuthToken string `json:"auth_token,omitempty"`
}

// PromptOpts are optional parameters for a prompt turn.
type PromptOpts struct {
	Thinking  string // "enabled" to force reasoning this turn, "" for default
	Model     string // switch the active model when set
	SessionID string // resume/continue a specific session
	AuthToken string // session-scoped token, required when SessionID is set
}

// SendPrompt submits a task. Session continuity is automatic on a single
// connection; SessionID+AuthToken resume a saved conversation.
func (c *Client) SendPrompt(content string, opts PromptOpts) error {
	return ws.JSON.Send(c.conn, prompt{
		Type:      "prompt",
		Content:   content,
		Thinking:  opts.Thinking,
		Model:     opts.Model,
		SessionID: opts.SessionID,
		AuthToken: opts.AuthToken,
	})
}

// approval is the client→server response to an approval_request.
type approval struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Action string `json:"action"` // "approve" | "deny" | "trust"
}

// SendApproval answers a pending approval_request.
func (c *Client) SendApproval(id, action string) error {
	return ws.JSON.Send(c.conn, approval{Type: "approval_response", ID: id, Action: action})
}

// Close shuts the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
