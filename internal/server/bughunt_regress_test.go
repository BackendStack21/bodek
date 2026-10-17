package server

import (
	"net/url"
	"testing"
)

func TestRegressAttachEndpointsKeepUsablePaths(t *testing.T) {
	base, tok := splitTokenURL("http://127.0.0.1:8080/?token=abc&foo=1")
	if tok != "abc" {
		t.Fatalf("token = %q", tok)
	}
	baseURL, wsURL := attachEndpoints(base)
	if baseURL != "http://127.0.0.1:8080" {
		t.Fatalf("REST base = %q", baseURL)
	}
	u, err := url.Parse(wsURL)
	if err != nil || u.Path != "/ws" || u.Scheme != "ws" || u.Query().Get("foo") != "1" {
		t.Fatalf("ws url %q parsed to path %q scheme %q (err %v)", wsURL, u.Path, u.Scheme, err)
	}
	_, wss := attachEndpoints("https://h.example/odek/")
	if wss != "wss://h.example/odek/ws" {
		t.Fatalf("wss url = %q", wss)
	}
}

func TestRegressAttachURLNormalizesAndRejects(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8080":         "http://127.0.0.1:8080",
		"localhost:8080":         "http://localhost:8080",
		"https://h.example/odek": "https://h.example/odek",
	} {
		got, err := attachURL(in)
		if err != nil || got != want {
			t.Errorf("attachURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://h:1", "http://", "http://h:1/%zz"} {
		if _, err := attachURL(bad); err == nil {
			t.Errorf("attachURL(%q) accepted", bad)
		}
	}
	if _, err := Connect(Options{URL: "ftp://127.0.0.1:1"}); err == nil {
		t.Error("Connect accepted a non-http attach URL")
	}
}

func TestRegressProbeReadyRejectsUnbuildableRequest(t *testing.T) {
	// Previously a nil request reached client.Do and panicked.
	if probeReady("127.0.0.1:8080") {
		t.Error("probeReady succeeded on a scheme-less URL")
	}
}
