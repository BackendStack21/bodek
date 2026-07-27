package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// unreachableClient points at a closed port so every request errors.
func unreachableClient() *Client {
	return &Client{baseURL: "http://127.0.0.1:1", http: &http.Client{Timeout: 200 * time.Millisecond}}
}

func TestRESTRequestErrors(t *testing.T) {
	c := unreachableClient()
	if _, err := c.Sessions(); err == nil {
		t.Error("Sessions should error")
	}
	if _, err := c.Models(); err == nil {
		t.Error("Models should error")
	}
	if _, err := c.Resources("q", 5); err == nil {
		t.Error("Resources should error")
	}
	if _, _, err := c.SessionDetail("id", "tok"); err == nil {
		t.Error("SessionDetail should error")
	}
	if err := c.DeleteSession("id", "tok"); err == nil {
		t.Error("DeleteSession should error")
	}
	if err := c.Cancel("id", "tok"); err == nil {
		t.Error("Cancel should error")
	}
}

func TestRESTBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if _, err := c.Sessions(); err == nil {
		t.Error("Sessions should error on bad JSON")
	}
	if _, _, err := c.SessionDetail("id", "tok"); err == nil {
		t.Error("SessionDetail should error on bad JSON")
	}
}

func TestResourcesBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if _, err := c.Resources("q", 5); err == nil {
		t.Error("Resources should error on bad JSON")
	}
}

func TestResourcesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if _, err := c.Resources("q", 5); err == nil {
		t.Error("Resources should error on 500")
	}
}

func TestSessionDetailFallbackToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No X-Session-Token header → SessionDetail falls back to the passed token.
		w.Write([]byte(`{"id":"s1","messages":[` +
			`{"role":"assistant","content":"done","reasoning_content":"thought",` +
			`"tool_calls":[{"id":"c1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"ls\"}"}}]},` +
			`{"role":"tool","name":"shell","tool_call_id":"c1","content":"out"}` +
			`]}`))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	sess, tok, err := c.SessionDetail("s1", "passed-token")
	if err != nil || tok != "passed-token" {
		t.Fatalf("fallback token = %q, err=%v", tok, err)
	}
	// The full OpenAI-style transcript decodes: reasoning, tool calls, and the
	// tool result's name / tool_call_id pairing.
	if len(sess.Messages) != 2 {
		t.Fatalf("messages = %+v", sess.Messages)
	}
	a, tr := sess.Messages[0], sess.Messages[1]
	if a.ReasoningContent != "thought" || len(a.ToolCalls) != 1 {
		t.Errorf("assistant message = %+v", a)
	}
	tc := a.ToolCalls[0]
	if tc.ID != "c1" || tc.Type != "function" || tc.Function.Name != "shell" || tc.Function.Arguments != `{"cmd":"ls"}` {
		t.Errorf("tool call = %+v", tc)
	}
	if tr.Name != "shell" || tr.ToolCallID != "c1" || tr.Content != "out" {
		t.Errorf("tool result = %+v", tr)
	}
}

func TestDialBadURL(t *testing.T) {
	if _, err := Dial("://bad", "://bad", "x", "t"); err == nil {
		t.Error("Dial should error on a malformed ws URL")
	}
}

func TestDoRequestBuildError(t *testing.T) {
	// A control character in the base URL makes http.NewRequest fail, exercising
	// the do() error path.
	c := &Client{baseURL: "http://\x7f", http: &http.Client{Timeout: time.Second}}
	if _, err := c.Sessions(); err == nil {
		t.Error("expected request-build error")
	}
}
