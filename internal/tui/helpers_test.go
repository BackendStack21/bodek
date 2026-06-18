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

func TestActiveRef(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		match bool
	}{
		{"explain @main", "main", true},
		{"@", "", true},
		{"look at @src/app.go", "src/app.go", true},
		{"no ref here", "", false},
		{"email a@b.com", "", false},  // '@' not at a token boundary
		{"@sess:abc done", "", false}, // ref already terminated by space
	}
	for _, c := range cases {
		got, ok := activeRef(c.in)
		if ok != c.match || (ok && got != c.want) {
			t.Errorf("activeRef(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.match)
		}
	}
}

func TestRefStart(t *testing.T) {
	in := "please read @internal/x"
	idx, ok := refStart(in)
	if !ok || in[idx] != '@' {
		t.Fatalf("refStart(%q) = %d,%v (char %q)", in, idx, ok, in[idx])
	}
	if in[:idx] != "please read " {
		t.Errorf("prefix = %q", in[:idx])
	}
}

func TestToolGlyph(t *testing.T) {
	if toolGlyph("shell") == toolGlyph("read_file") {
		t.Error("expected distinct glyphs for shell and read_file")
	}
	if toolGlyph("totally_unknown_tool") != "✦" {
		t.Errorf("unknown tool glyph = %q", toolGlyph("totally_unknown_tool"))
	}
}
