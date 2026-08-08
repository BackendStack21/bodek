package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ws "golang.org/x/net/websocket"

	"github.com/BackendStack21/bodek/internal/client"
)

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0"},
		{0.000698, "$0.0007"},
		{0.0034, "$0.0034"},
		{0.016, "$0.016"},
		{0.1, "$0.10"}, // two-decimal floor pads trimmed zeros
		{0.5, "$0.50"},
		{0.9999, "$0.9999"},
		{1, "$1.00"},
		{1.234, "$1.23"},
		{42.5, "$42.50"},
	}
	for _, c := range cases {
		if got := formatUSD(c.in); got != c.want {
			t.Errorf("formatUSD(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLineCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"a\nb\nc\n", 3},
	}
	for _, c := range cases {
		if got := lineCount(c.in); got != c.want {
			t.Errorf("lineCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCostUSD(t *testing.T) {
	// 10k input @ $1/M + 2k output @ $3/M = $0.01 + $0.006.
	if got := costUSD(10_000, 2_000, 1.0, 3.0); got != 0.016 {
		t.Errorf("costUSD = %v, want 0.016", got)
	}
}

func TestLimitsMsgStoresPrices(t *testing.T) {
	m := newTestModel()
	m.Update(limitsMsg{resp: client.LimitsResponse{
		Model:  "deepseek-chat",
		Limits: client.Limits{InputCostPerMillionUSD: 0.27, OutputCostPerMillionUSD: 1.10},
	}})
	in, out := m.limits.ResolvePrices(m.model)
	if in != 0.27 || out != 1.10 {
		t.Errorf("prices = %v/%v", in, out)
	}
}

// A failed fetch must leave the zero Limits in place so cost stays hidden.
func TestLimitsMsgErrorKeepsCostHidden(t *testing.T) {
	m := newTestModel()
	m.Update(limitsMsg{err: errors.New("boom")})
	if in, out := m.limits.ResolvePrices(m.model); in != 0 || out != 0 {
		t.Errorf("prices = %v/%v, want 0/0", in, out)
	}
}

func TestTurnFooterShowsCost(t *testing.T) {
	m := newTestModel()
	m.limits = client.Limits{InputCostPerMillionUSD: 1.0, OutputCostPerMillionUSD: 3.0}
	drive := driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	out := plain(drive.View())
	if !strings.Contains(out, "$ $0.016") {
		t.Errorf("footer missing turn cost in:\n%s", out)
	}
}

// Per-model price overrides must apply per field, matching odek's
// budget.Limits.ResolvePrices.
func TestTurnFooterUsesModelOverride(t *testing.T) {
	m := newTestModel()
	m.model = "other"
	m.limits = client.Limits{
		InputCostPerMillionUSD:  1.0,
		OutputCostPerMillionUSD: 3.0,
		ModelPrices:             map[string]client.ModelPrice{"other": {InputCostPerMillionUSD: 2.0}},
	}
	drive := driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
	})
	out := plain(drive.View())
	// 10k input @ $2/M + 2k output @ $3/M = $0.02 + $0.006.
	if !strings.Contains(out, "$ $0.026") {
		t.Errorf("footer missing override cost in:\n%s", out)
	}
}

// Without configured prices no dollar figure may appear anywhere.
func TestTurnFooterHidesCostWithoutPrices(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	if out := plain(m.View()); strings.Contains(out, "$") {
		t.Errorf("footer must hide cost without prices, got:\n%s", out)
	}
}

func TestHeaderShowsSessionCost(t *testing.T) {
	m := newTestModel()
	m.limits = client.Limits{InputCostPerMillionUSD: 1.0, OutputCostPerMillionUSD: 3.0}
	m = driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	if out := plain(m.header()); !strings.Contains(out, "$0.016") {
		t.Errorf("header missing session cost in:\n%s", out)
	}
}

// Without configured prices the header must not show a dollar figure.
func TestHeaderHidesCostWithoutPrices(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	if out := plain(m.header()); strings.Contains(out, "$") {
		t.Errorf("header must hide cost without prices, got:\n%s", out)
	}
}

// fetchLimits against a live server yields a limitsMsg carrying the prices.
func TestFetchLimitsSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		for {
			var d []byte
			if ws.Message.Receive(c, &d) != nil {
				return
			}
		}
	}))
	mux.HandleFunc("/api/limits", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model":"m","limits":{"input_cost_per_million_usd":0.27,"output_cost_per_million_usd":1.1}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl, err := client.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", srv.URL, srv.URL, "t")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cl.Close()
	m := New(cl, Options{})
	msg := exec(m.fetchLimits())
	lm, ok := msg.(limitsMsg)
	if !ok || lm.err != nil {
		t.Fatalf("fetchLimits msg = %#v", msg)
	}
	if lm.resp.Limits.InputCostPerMillionUSD != 0.27 {
		t.Errorf("limits = %+v", lm.resp.Limits)
	}
}

// fetchLimits against a down server yields a limitsMsg carrying the error.
func TestFetchLimitsError(t *testing.T) {
	m := downModel(t)
	msg := exec(m.fetchLimits())
	if lm, ok := msg.(limitsMsg); !ok || lm.err == nil {
		t.Errorf("fetchLimits against a down server = %#v", msg)
	}
}

func TestStatsCardCostRow(t *testing.T) {
	m := newTestModel()
	m.limits = client.Limits{
		InputCostPerMillionUSD:  1.0,
		OutputCostPerMillionUSD: 3.0,
		MaxCostUSD:              5,
	}
	m = driveTurnWith(t, m, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	m.showStats()
	out := plain(m.View())
	if !strings.Contains(out, "cost") || !strings.Contains(out, "$0.016") {
		t.Errorf("stats card missing cost row in:\n%s", out)
	}
	if !strings.Contains(out, "cap $5.00") {
		t.Errorf("stats card missing budget cap in:\n%s", out)
	}
}

// Without prices the stats card must not grow a cost row.
func TestStatsCardHidesCostWithoutPrices(t *testing.T) {
	m := driveTurn(t, client.Event{
		Type: "done", Latency: 1,
		ContextTokens: 10_000, OutputTokens: 2_000,
		SessionContextTokens: 10_000, SessionOutputTokens: 2_000,
	})
	m.showStats()
	if out := plain(m.View()); strings.Contains(out, "cost") || strings.Contains(out, "$") {
		t.Errorf("stats card must hide cost without prices, got:\n%s", out)
	}
}
