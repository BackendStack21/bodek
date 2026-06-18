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
		w.Write([]byte(`{"id":"s1"}`))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	_, tok, err := c.SessionDetail("s1", "passed-token")
	if err != nil || tok != "passed-token" {
		t.Fatalf("fallback token = %q, err=%v", tok, err)
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
