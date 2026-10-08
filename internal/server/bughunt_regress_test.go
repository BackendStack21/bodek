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
