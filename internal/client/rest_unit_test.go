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
	if _, err := c.Limits(); err == nil {
		t.Error("Limits should error")
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

func TestLimitsDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/limits" {
			t.Errorf("path = %q, want /api/limits", r.URL.Path)
		}
		w.Write([]byte(`{"model":"deepseek-chat","limits":{` +
			`"max_cost_usd":5,` +
			`"input_cost_per_million_usd":0.27,` +
			`"output_cost_per_million_usd":1.10,` +
			`"model_prices":{"other-model":{"input_cost_per_million_usd":0.5}}` +
			`}}`))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	resp, err := c.Limits()
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	if resp.Model != "deepseek-chat" || resp.Limits.MaxCostUSD != 5 {
		t.Errorf("response = %+v", resp)
	}
	// Flat prices apply to models without an override.
	in, out := resp.Limits.ResolvePrices("deepseek-chat")
	if in != 0.27 || out != 1.10 {
		t.Errorf("flat prices = %v/%v", in, out)
	}
	// Per-model overrides win per field; a missing field falls back to flat.
	in, out = resp.Limits.ResolvePrices("other-model")
	if in != 0.5 || out != 1.10 {
		t.Errorf("override prices = %v/%v", in, out)
	}
}

func TestResolvePricesOverrideBranches(t *testing.T) {
	l := Limits{
		InputCostPerMillionUSD:  1,
		OutputCostPerMillionUSD: 2,
		ModelPrices: map[string]ModelPrice{
			"out-only": {OutputCostPerMillionUSD: 9},
			"both":     {InputCostPerMillionUSD: 7, OutputCostPerMillionUSD: 9},
		},
	}
	// Output-only override keeps the flat input price.
	if in, out := l.ResolvePrices("out-only"); in != 1 || out != 9 {
		t.Errorf("out-only = %v/%v, want 1/9", in, out)
	}
	// Both fields overridden.
	if in, out := l.ResolvePrices("both"); in != 7 || out != 9 {
		t.Errorf("both = %v/%v, want 7/9", in, out)
	}
}

func TestLimitsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if _, err := c.Limits(); err == nil {
		t.Error("Limits should error on 500")
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

func TestDeleteMemoryFactSetsJSONContentType(t *testing.T) {
	var gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		if r.Method != http.MethodDelete || r.URL.Path != "/api/memory/facts" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if gotType != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if err := c.DeleteMemoryFact("user", "old fact"); err != nil {
		t.Fatalf("DeleteMemoryFact: %v", err)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
}
