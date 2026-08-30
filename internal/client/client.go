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
	"sync"
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

	// done — token economics for the turn and the session. ContextTokens is
	// cumulative prompt tokens across all LLM calls of the run (the live
	// window fill is the delta between consecutive reports); the Session*
	// pair are cumulative totals across runs that only grow. The cache trio
	// is provider cache activity for the turn (0 = not reported).
	Latency              float64 `json:"latency"`
	ContextTokens        int     `json:"contextTokens"`
	OutputTokens         int     `json:"outputTokens"`
	CacheCreationTokens  int     `json:"cacheCreationTokens"`
	CacheReadTokens      int     `json:"cacheReadTokens"`
	CachedTokens         int     `json:"cachedTokens"`
	SessionContextTokens int     `json:"sessionContextTokens"`
	SessionOutputTokens  int     `json:"sessionOutputTokens"`

	// approval_request. Friction is the approval-fatigue gate: the server
	// demands the literal word "approve" (no shortcut) and the UI must show
	// FrictionApprovals (same-class approvals inside the window).
	ID                string `json:"id"`
	Risk              string `json:"risk"`
	Command           string `json:"command"`
	Description       string `json:"description"`
	IsOperation       bool   `json:"is_operation"`
	AllowTrust        bool   `json:"allow_trust"`
	Friction          bool   `json:"friction"`
	FrictionApprovals int    `json:"friction_approvals"`

	// approval lifetime advertised per request (absent on current odek
	// frames; zero → the TUI falls back to the server's interactive
	// default instead of guessing).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// approval_ack (Action echoes the client's reply) / cancelled (Idle is
	// true when nothing was running for the target session).
	Action string `json:"action"`
	Idle   bool   `json:"idle"`

	// server_info / pong snapshot. T is the pong timestamp (unix ms); the
	// client measures round-trip latency from its own send clock instead.
	T             int64  `json:"t"`
	Version       string `json:"version"`
	Stream        bool   `json:"stream"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	WSConnections int64  `json:"ws_connections"`

	// skill_event / memory_event / agent_signal / subagent_log: the event
	// subtype (e.g. "loaded", "merge", "trim") plus a few shared details.
	// Status carries the child-reported log status on subagent_log frames.
	SubType   string `json:"event"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	Status    string `json:"status,omitempty"`
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
	conn       *ws.Conn
	wmu        sync.Mutex // serializes frame writes: heartbeat, prompts, and approvals race otherwise
	baseURL    string
	serveToken string
	http       *http.Client
	Events     chan Event
}

// Dial connects to an odek serve WebSocket. wsURL is the ws:// endpoint,
// origin is an http://localhost-based origin accepted by the server, baseURL is
// the http:// root (used for the REST APIs), and token is the per-instance
// CSRF token resolved by server.Connect (token URL, stderr banner, or legacy
// cookie). It is sent on the WS handshake and on every REST request.
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
		conn:       conn,
		baseURL:    baseURL,
		serveToken: token,
		http:       &http.Client{Timeout: 3 * time.Second},
		Events:     make(chan Event, 256),
	}
	go c.readLoop()
	return c, nil
}

// Resources queries the server's @-reference completion endpoint.
func (c *Client) Resources(query string, limit int) ([]Resource, error) {
	u := fmt.Sprintf("%s/api/resources?q=%s&limit=%d",
		c.baseURL, url.QueryEscape(query), limit)
	resp, err := c.do(http.MethodGet, u, "")
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
	Type        string       `json:"type"`
	Content     string       `json:"content"`
	Thinking    string       `json:"thinking,omitempty"`
	Model       string       `json:"model,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
	AuthToken   string       `json:"auth_token,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is a file inlined into a prompt. It crosses the trust boundary
// wrapped in the server's nonce'd untrusted-content envelope, exactly like a
// WebUI drag-and-drop attachment.
type Attachment struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// PromptOpts are optional parameters for a prompt turn.
type PromptOpts struct {
	Thinking    string       // "enabled" to force reasoning this turn, "" for default
	Model       string       // switch the active model when set
	SessionID   string       // resume/continue a specific session
	AuthToken   string       // session-scoped token, required when SessionID is set
	Attachments []Attachment // files inlined into the prompt (5 MB each / 10 MB total)
}

// SendPrompt submits a task. Session continuity is automatic on a single
// connection; SessionID+AuthToken resume a saved conversation.
func (c *Client) SendPrompt(content string, opts PromptOpts) error {
	return c.send(prompt{
		Type:        "prompt",
		Content:     content,
		Thinking:    opts.Thinking,
		Model:       opts.Model,
		SessionID:   opts.SessionID,
		AuthToken:   opts.AuthToken,
		Attachments: opts.Attachments,
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
	return c.send(approval{Type: "approval_response", ID: id, Action: action})
}

// Ping sends the application-level heartbeat. The server answers inline with
// a pong — even mid-run — which also refreshes the server snapshot fields.
func (c *Client) Ping() error {
	return c.send(struct {
		Type string `json:"type"`
	}{"ping"})
}

// SendCancel aborts the prompt running on a session over the WebSocket (same
// session-scoped auth as POST /api/cancel, which remains the REST fallback).
func (c *Client) SendCancel(sessionID, authToken string) error {
	return c.send(struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		AuthToken string `json:"auth_token,omitempty"`
	}{Type: "cancel", SessionID: sessionID, AuthToken: authToken})
}

// SessionSwitch adopts an existing session without sending a prompt: the
// connection's agent restores the session's memory buffer and the server
// emits the standard `session` event.
func (c *Client) SessionSwitch(sessionID, authToken string) error {
	return c.send(struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		AuthToken string `json:"auth_token,omitempty"`
	}{Type: "session_switch", SessionID: sessionID, AuthToken: authToken})
}

// SendSkillPromptResponse acknowledges a skill_event "suggested" card
// (save/skip). Persistence itself is handled server-side by auto-save.
func (c *Client) SendSkillPromptResponse(action, skillName string) error {
	return c.send(struct {
		Type      string `json:"type"`
		Action    string `json:"action"`
		SkillName string `json:"skill_name"`
	}{Type: "skill_prompt_response", Action: action, SkillName: skillName})
}

// send marshals v and writes it as one frame, serialized against other writers.
func (c *Client) send(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return ws.JSON.Send(c.conn, v)
}

// Close shuts the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
