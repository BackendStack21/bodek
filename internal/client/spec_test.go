package client

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

// TestClientV2Messages verifies the wire shape of every client→server message
// added by protocol v2: ping, cancel, session_switch, skill_prompt_response,
// and prompt attachments.
func TestClientV2Messages(t *testing.T) {
	var mu sync.Mutex
	var frames []map[string]any
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var data []byte
			if err := ws.Message.Receive(c, &data); err != nil {
				return
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			mu.Lock()
			frames = append(frames, m)
			mu.Unlock()
		}
	}))
	cl, _ := newTestServer(t, mux)

	if err := cl.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := cl.SendCancel("s1", "a1"); err != nil {
		t.Fatalf("SendCancel: %v", err)
	}
	if err := cl.SessionSwitch("s1", "a1"); err != nil {
		t.Fatalf("SessionSwitch: %v", err)
	}
	if err := cl.SendSkillPromptResponse("save", "deploy-helper"); err != nil {
		t.Fatalf("SendSkillPromptResponse: %v", err)
	}
	if err := cl.SendPrompt("see attached", PromptOpts{
		SessionID: "s1", AuthToken: "a1",
		Attachments: []Attachment{{Name: "f.txt", Content: "data"}},
	}); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(frames)
		mu.Unlock()
		if n >= 5 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d frames arrived", n)
		case <-time.After(20 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	byType := map[string]map[string]any{}
	for _, f := range frames {
		byType[f["type"].(string)] = f
	}
	if _, ok := byType["ping"]; !ok {
		t.Errorf("ping frame missing: %v", frames)
	}
	if c := byType["cancel"]; c == nil || c["session_id"] != "s1" || c["auth_token"] != "a1" {
		t.Errorf("cancel frame = %v", c)
	}
	if s := byType["session_switch"]; s == nil || s["session_id"] != "s1" || s["auth_token"] != "a1" {
		t.Errorf("session_switch frame = %v", s)
	}
	if k := byType["skill_prompt_response"]; k == nil || k["action"] != "save" || k["skill_name"] != "deploy-helper" {
		t.Errorf("skill_prompt_response frame = %v", k)
	}
	p := byType["prompt"]
	if p == nil {
		t.Fatalf("prompt frame missing: %v", frames)
	}
	atts, ok := p["attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("prompt attachments = %v", p["attachments"])
	}
	att, _ := atts[0].(map[string]any)
	if att["name"] != "f.txt" || att["content"] != "data" {
		t.Errorf("attachment = %v", att)
	}
}

// TestSearchSessionsEnvelope covers the paginated /api/sessions contract:
// params present → envelope, plus the pin/rename POST and export/models/
// health endpoints bodek now speaks.
func TestSearchSessionsEnvelope(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {}))
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if !q.Has("q") || !q.Has("limit") || !q.Has("offset") {
			t.Errorf("missing pagination params: %s", r.URL.RawQuery)
		}
		if q.Get("q") != "deploy" || q.Get("limit") != "50" || q.Get("offset") != "50" {
			t.Errorf("unexpected params: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(SessionsPage{
			Sessions: []Session{{ID: "s2", Task: "deploy again", Pinned: true, InputTokens: 10, OutputTokens: 5}},
			Offset:   50, Limit: 50, Count: 1, Query: "deploy",
		})
	})
	cl, _ := newTestServer(t, mux)

	page, err := cl.SearchSessions("deploy", 50, 50)
	if err != nil {
		t.Fatalf("SearchSessions: %v", err)
	}
	if page.Count != 1 || page.Query != "deploy" || len(page.Sessions) != 1 {
		t.Fatalf("page = %+v", page)
	}
	s := page.Sessions[0]
	if !s.Pinned || s.InputTokens != 10 || s.OutputTokens != 5 {
		t.Errorf("session pinned/tokens not decoded: %+v", s)
	}
}

func TestUpdateSessionShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {}))
	mux.HandleFunc("/api/sessions/s1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("X-Session-Token") != "tok" || r.Header.Get("X-Odek-Ws-Token") != "test-token" {
			t.Errorf("missing auth headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["name"] != "renamed" || body["pinned"] != true {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(http.StatusOK)
	})
	cl, _ := newTestServer(t, mux)

	name, pin := "renamed", true
	if err := cl.UpdateSession("s1", "tok", &name, &pin); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
}

func TestExportModelsHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {}))
	mux.HandleFunc("/api/sessions/s1/export", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "md" {
			t.Errorf("format = %q", r.URL.Query().Get("format"))
		}
		_, _ = io.WriteString(w, "# transcript\n")
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]ModelInfo{
			{ID: "glm-5.3", MaxContext: 1000000, Current: true},
			{ID: "glm-4.7", MaxContext: 200000},
		})
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "version": "1.24.0", "uptime_seconds": 90,
			"model": "glm-5.3", "sandbox": true, "stream": true, "ws_connections": 2,
		})
	})
	cl, _ := newTestServer(t, mux)

	data, err := cl.ExportSession("s1", "tok", "md")
	if err != nil || string(data) != "# transcript\n" {
		t.Fatalf("ExportSession = %q, %v", data, err)
	}
	models, err := cl.Models()
	if err != nil || len(models) != 2 || models[0].MaxContext != 1000000 || !models[0].Current {
		t.Fatalf("Models = %+v, %v", models, err)
	}
	h, err := cl.Health()
	if err != nil || h.Version != "1.24.0" || !h.Stream || h.WSConnections != 2 {
		t.Fatalf("Health = %+v, %v", h, err)
	}
}

// TestPricesFor pins the spec's price-resolution rule: effective_prices for
// the server's configured model, model_prices + flat fallback for others.
func TestPricesFor(t *testing.T) {
	resp := LimitsResponse{
		Model: "deepseek-v4-flash",
		Limits: Limits{
			InputCostPerMillionUSD:  2.0,
			OutputCostPerMillionUSD: 8.0,
			ModelPrices: map[string]ModelPrice{
				"glm-5.3": {InputCostPerMillionUSD: 0.14, OutputCostPerMillionUSD: 0.28},
				"half":    {InputCostPerMillionUSD: 1.0}, // per-field fallback
			},
		},
		EffectivePrices: ModelPrice{InputCostPerMillionUSD: 0.14, OutputCostPerMillionUSD: 0.28},
	}

	// The server's configured model uses effective_prices directly.
	in, out := resp.PricesFor("deepseek-v4-flash")
	if in != 0.14 || out != 0.28 {
		t.Errorf("configured model prices = %v/%v", in, out)
	}
	// Another model resolves through model_prices.
	in, out = resp.PricesFor("glm-5.3")
	if in != 0.14 || out != 0.28 {
		t.Errorf("glm prices = %v/%v", in, out)
	}
	// Per-field fallback to the flat pair for partially-configured models.
	in, out = resp.PricesFor("half")
	if in != 1.0 || out != 8.0 {
		t.Errorf("half prices = %v/%v", in, out)
	}
	// Unknown model: flat pair.
	in, out = resp.PricesFor("other")
	if in != 2.0 || out != 8.0 {
		t.Errorf("other prices = %v/%v", in, out)
	}
	// Zero effective prices (server without configured prices) fall through
	// to resolution rather than reporting 0.
	noPrices := resp
	noPrices.EffectivePrices = ModelPrice{}
	in, out = noPrices.PricesFor("deepseek-v4-flash")
	if in != 2.0 || out != 8.0 {
		t.Errorf("no-effective prices = %v/%v", in, out)
	}
}
