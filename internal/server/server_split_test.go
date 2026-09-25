package server

import (
	"testing"
)

// Regression: splitTokenURL cleared the entire RawQuery, so an attach URL
// with extra parameters (?token=x&profile=y) silently lost them.
func TestSplitTokenURLKeepsOtherParams(t *testing.T) {
	base, token := splitTokenURL("http://127.0.0.1:8080/?token=abc&profile=dev")
	if token != "abc" {
		t.Fatalf("token = %q, want abc", token)
	}
	if want := "http://127.0.0.1:8080/?profile=dev"; base != want {
		t.Fatalf("base = %q, want %q", base, want)
	}
	// A token-only URL still strips cleanly.
	base, token = splitTokenURL("http://127.0.0.1:8080/?token=abc")
	if token != "abc" || base != "http://127.0.0.1:8080/" {
		t.Fatalf("token-only URL: base=%q token=%q", base, token)
	}
	// Fragments are stripped too.
	base, token = splitTokenURL("http://127.0.0.1:8080/?token=abc#frag")
	if token != "abc" || base != "http://127.0.0.1:8080/" {
		t.Fatalf("fragment not stripped: base=%q token=%q", base, token)
	}
}
