package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPromoteEpisodeContract pins the odek v2.34.0 promote contract: the
// body carries session_id and the required summary_sha256, a 200 body is
// decoded into PromoteResult, and failures map to typed errors that carry
// the HTTP status and the server's message.
func TestPromoteEpisodeContract(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		switch gotBody["summary_sha256"] {
		case "deadbeef01":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id": "s1", "summary": "fixed the login bug", "sources": []string{"browser"},
			})
		case "stalehash":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte("summary changed since listing"))
		case "rejecthash":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("unknown episode"))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("boom"))
		}
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}

	res, err := c.PromoteEpisode("s1", "deadbeef01")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.SessionID != "s1" || res.Summary != "fixed the login bug" || len(res.Sources) != 1 || res.Sources[0] != "browser" {
		t.Errorf("result = %+v", res)
	}
	if gotBody["session_id"] != "s1" || gotBody["summary_sha256"] != "deadbeef01" {
		t.Errorf("request body = %v", gotBody)
	}

	// Empty hash refuses locally — the server must never see a request.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("empty hash still reached the server")
	})
	c2 := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if _, err := c2.PromoteEpisode("s1", ""); err == nil {
		t.Fatal("empty hash accepted")
	}

	// 409, 400 and 500 are distinct typed errors carrying the message.
	for _, tc := range []struct {
		hash   string
		status int
		want   string
	}{
		{"stalehash", http.StatusConflict, "summary changed since listing"},
		{"rejecthash", http.StatusBadRequest, "unknown episode"},
		{"boomhash", http.StatusInternalServerError, "boom"},
	} {
		c3 := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			switch m["summary_sha256"] {
			case "stalehash":
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte("summary changed since listing"))
			case "rejecthash":
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("unknown episode"))
			default:
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("boom"))
			}
		})
		_, err := c3.PromoteEpisode("s1", tc.hash)
		if err == nil {
			t.Fatalf("%s: want error", tc.hash)
		}
		pe, ok := err.(*PromoteError)
		if !ok {
			t.Fatalf("%s: err %T not *PromoteError: %v", tc.hash, err, err)
		}
		if pe.Status != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.hash, pe.Status, tc.status)
		}
		if !strings.Contains(pe.Message, tc.want) {
			t.Errorf("%s: message = %q, want containing %q", tc.hash, pe.Message, tc.want)
		}
	}
}

// TestMemoryViewPendingFields pins the v2.34.0 /api/memory pending-episode
// fields: full summary, hash, turns, created_at, provenance sources.
func TestMemoryViewPendingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"facts": map[string][]string{"user": {"prefers vim"}},
			"episodes": map[string]any{"total": 1, "pending": []map[string]any{{
				"session_id":     "s1",
				"summary":        "full stored episode text, not truncated",
				"summary_sha256": "abc123",
				"turns":          7,
				"created_at":     "2026-10-10T08:00:00Z",
				"provenance":     map[string]any{"sources": []string{"browser", "mcp:fs:read"}},
			}}},
		})
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	v, err := c.Memory()
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	if len(v.Episodes.Pending) != 1 {
		t.Fatalf("pending = %d", len(v.Episodes.Pending))
	}
	e := v.Episodes.Pending[0]
	if e.SummarySHA256 != "abc123" || e.Turns != 7 || e.CreatedAt != "2026-10-10T08:00:00Z" {
		t.Errorf("pending episode = %+v", e)
	}
	if len(e.Provenance.Sources) != 2 || e.Provenance.Sources[1] != "mcp:fs:read" {
		t.Errorf("sources = %v", e.Provenance.Sources)
	}
}

// TestDiscardEpisode pins POST /api/memory/episodes/discard {session_id}.
func TestDiscardEpisode(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/memory/episodes/discard") {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: &http.Client{Timeout: time.Second}}
	if err := c.DiscardEpisode("s9"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if got["session_id"] != "s9" {
		t.Errorf("body = %v", got)
	}
}
