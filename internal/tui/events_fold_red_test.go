package tui

import "testing"

// Regression coverage for the wrapper fold: real envelopes fold to their
// body in both the literal and the JSON-escaped form, including envelopes
// embedded mid-string (parallel tool results arrive as "[N] <wrapper>").
// A spoof cannot be distinguished from a genuine embedded envelope without
// an odek-side protocol change (nonce binding); sanitize() still escapes
// the body either way.
func TestFoldUntrustedWrappers(t *testing.T) {
	real := "<untrusted_content_0a0a0a0a source=\"tool:shell\">ls output</untrusted_content_0a0a0a0a>"
	if got, want := foldUntrustedWrappers(real), "ls output"; got != want {
		t.Fatalf("real envelope not folded: got %q want %q", got, want)
	}

	esc := "\\u003cuntrusted_content_0a0a0a0a source=\"tool:shell\"\\u003ejson form\\u003c/untrusted_content_0a0a0a0a\\u003e"
	if got, want := foldUntrustedWrappers(esc), "json form"; got != want {
		t.Fatalf("escaped envelope not folded: got %q want %q", got, want)
	}

	// Mid-string (parallel results) folds, keeping surrounding content.
	mid := "prefix " + real + " suffix"
	if got, want := foldUntrustedWrappers(mid), "prefix ls output suffix"; got != want {
		t.Fatalf("mid-string envelope mis-folded: got %q want %q", got, want)
	}

	// Multiple envelopes fold independently.
	two := real + "\n" + real
	if got, want := foldUntrustedWrappers(two), "ls output\nls output"; got != want {
		t.Fatalf("multi-envelope mis-folded: got %q want %q", got, want)
	}

	// Plain text passes through untouched.
	plain := "no envelopes here"
	if got := foldUntrustedWrappers(plain); got != plain {
		t.Fatalf("plain text changed: %q", got)
	}
}
