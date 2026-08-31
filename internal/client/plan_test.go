package client

import (
	"net/http"
	"strings"
	"testing"

	ws "golang.org/x/net/websocket"
)

// Tests for GET /api/sessions/{id}/plan (odek serve contract):
// structured snapshot decoding, the found:false
// shape, HTTP error mapping, and malformed-body tolerance. The endpoint is
// read-only by engine contract; the client never sends anything but GET.

func TestSessionPlan_DecodesSnapshot(t *testing.T) {
	var gotPath, gotMethod string
	mux := newPlanTestMux(t, &gotPath, &gotMethod, `{
		"session_id": "s1", "version": 3, "found": true,
		"steps": [
			{"id": "p1", "title": "Scaffold command skeleton", "status": "done"},
			{"id": "p2", "title": "Wire flag parsing", "status": "in_progress", "note": "flag order matters"}
		]
	}`)
	cl, _ := newTestServer(t, mux)

	snap, err := cl.SessionPlan("s1", "tok")
	if err != nil {
		t.Fatalf("SessionPlan: %v", err)
	}
	if !strings.HasPrefix(gotPath, "/api/sessions/s1/plan") {
		t.Errorf("request path = %q, want prefix /api/sessions/s1/plan", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET only", gotMethod)
	}
	if snap.SessionID != "s1" || snap.Version != 3 || !snap.Found {
		t.Fatalf("snapshot header wrong: %+v", snap)
	}
	if len(snap.Steps) != 2 {
		t.Fatalf("steps = %+v, want 2", snap.Steps)
	}
	if snap.Steps[0].ID != "p1" || snap.Steps[0].Status != PlanDone {
		t.Errorf("step0 = %+v", snap.Steps[0])
	}
	if snap.Steps[1].Note != "flag order matters" {
		t.Errorf("step1 note = %q", snap.Steps[1].Note)
	}
}

func TestSessionPlan_FoundFalse(t *testing.T) {
	mux := newPlanTestMux(t, nil, nil,
		`{"session_id": "s9", "version": 0, "found": false}`)
	cl, _ := newTestServer(t, mux)

	snap, err := cl.SessionPlan("s9", "")
	if err != nil {
		t.Fatalf("found:false must still be a valid snapshot: %v", err)
	}
	if snap.Found || len(snap.Steps) != 0 {
		t.Errorf("snapshot = %+v, want not-found with no steps", snap)
	}
}

func TestSessionPlan_HTTP404(t *testing.T) {
	mux := newPlanTestMux(t, nil, nil, "", http.StatusNotFound)
	cl, _ := newTestServer(t, mux)

	if _, err := cl.SessionPlan("ghost", ""); err == nil {
		t.Fatal("expected error on 404")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status, got: %v", err)
	}
}

func TestSessionPlan_MalformedBody(t *testing.T) {
	mux := newPlanTestMux(t, nil, nil, `{"version": `)
	cl, _ := newTestServer(t, mux)

	if _, err := cl.SessionPlan("s1", ""); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

// newPlanTestMux builds a mux serving exactly one canned plan response under
// /api/sessions/…/plan and records the request path/method for assertions.
func newPlanTestMux(t *testing.T, path *string, method *string, body string, code ...int) *http.ServeMux {
	t.Helper()
	status := http.StatusOK
	if len(code) > 0 {
		status = code[0]
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {}))
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if path != nil {
			*path = r.URL.Path
		}
		if method != nil {
			*method = r.Method
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}
