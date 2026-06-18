package tui

import (
	"strings"
	"testing"
	"time"
)

func TestToolProgressBranches(t *testing.T) {
	cases := map[string]string{
		"read_file":      "reading",
		"write_file":     "writing",
		"list_dir":       "listing",
		"search_files":   "searching the code",
		"web_search":     "searching the web",
		"browser":        "browsing",
		"delegate_tasks": "sub-agent",
		"memory":         "recalling",
		"vision":         "media",
		"mystery_tool":   "running mystery_tool",
	}
	for tool, want := range cases {
		if got := toolProgress(tool, "x"); !strings.Contains(got, want) {
			t.Errorf("toolProgress(%q) = %q, want contains %q", tool, got, want)
		}
	}
}

func TestShellProgressBranches(t *testing.T) {
	cases := map[string]string{
		"":                    "running a command",
		"go test ./...":       "running tests",
		"git commit -m x":     "committing",
		"git push":            "pushing",
		"git clone url":       "cloning",
		"git pull":            "syncing",
		"git checkout main":   "switching",
		"git merge dev":       "merging",
		"git status":          "checking git",
		"golangci-lint run":   "linting",
		"go build ./...":      "building",
		"npm install":         "installing",
		"docker ps":           "containers",
		"curl https://x":      "fetching",
		"ls -la":              "looking around",
		"cat file.txt":        "inspecting",
		"rm -rf dir":          "managing files",
		"echo something else": "echo something else",
	}
	for cmd, want := range cases {
		if got := shellProgress(cmd); !strings.Contains(got, want) {
			t.Errorf("shellProgress(%q) = %q, want contains %q", cmd, got, want)
		}
	}
}

func TestBaseAndHelpers(t *testing.T) {
	if base("") != "a file" {
		t.Error("base empty")
	}
	if base("a/b/c.go") != "c.go" {
		t.Errorf("base = %q", base("a/b/c.go"))
	}
	if !has("hello world", "nope", "world") {
		t.Error("has should match")
	}
	if has("abc", "x", "y") {
		t.Error("has should not match")
	}
	if !prefixAny("cat x", "cat", "head") {
		t.Error("prefixAny should match 'cat x'")
	}
	if prefixAny("category", "cat") {
		t.Error("prefixAny should not match 'category'")
	}
}

func TestAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, "—"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "m ago"},
		{now.Add(-3 * time.Hour), "h ago"},
		{now.Add(-48 * time.Hour), "d ago"},
	}
	for _, c := range cases {
		if got := ago(c.in); !strings.Contains(got, strings.TrimPrefix(c.want, "")) {
			t.Errorf("ago(%v) = %q, want contains %q", c.in, got, c.want)
		}
	}
}

func TestShortID(t *testing.T) {
	if shortID("short") != "short" {
		t.Error("shortID short")
	}
	long := "20260618-bf2127d911081dd521e3dc"
	if got := shortID(long); !strings.HasSuffix(got, "…") {
		t.Errorf("shortID long = %q", got)
	}
}

func TestWindowRows(t *testing.T) {
	rows := []string{"a", "b", "c", "d", "e"}
	if got := windowRows(rows, 0, 10); len(got) != 5 {
		t.Errorf("windowRows fit = %d", len(got))
	}
	if got := windowRows(rows, 4, 3); len(got) != 3 || got[2] != "e" {
		t.Errorf("windowRows end = %v", got)
	}
	if got := windowRows(rows, 2, 3); got[1] != "c" {
		t.Errorf("windowRows mid = %v", got)
	}
}

func TestFormatDuration(t *testing.T) {
	if got := formatDuration(3500 * time.Millisecond); got != "3.5s" {
		t.Errorf("formatDuration short = %q", got)
	}
	if got := formatDuration(90 * time.Second); got != "1m30s" {
		t.Errorf("formatDuration long = %q", got)
	}
}
