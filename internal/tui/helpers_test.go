package tui

import "testing"

func TestArgPreview(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"command":"go test ./..."}`, "go test ./..."},
		{`{"path":"main.go"}`, "main.go"},
		{`{"pattern":"func\\s+\\w+"}`, `func\s+\w+`},
		{``, ""},
		{`not json`, "not json"},
	}
	for _, c := range cases {
		if got := argPreview(c.in); got != c.want {
			t.Errorf("argPreview(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHuman(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{1234, "1.2k"},
		{2_500_000, "2.5M"},
	}
	for _, c := range cases {
		if got := human(c.in); got != c.want {
			t.Errorf("human(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("no-truncate: got %q", got)
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("truncate: got %q", got)
	}
}

func TestCollapse(t *testing.T) {
	if got := collapse("a\n  b\t c"); got != "a b c" {
		t.Errorf("collapse: got %q", got)
	}
}
