package tui

import (
	"path/filepath"
	"strings"
)

// thinkingPhrases cycle in the status badge while the model reasons before it
// acts, so a pause always feels alive.
var thinkingPhrases = []string{
	"🧠 thinking",
	"🔮 reasoning it through",
	"🧩 connecting the dots",
	"💭 mulling it over",
	"✨ planning the approach",
	"📐 weighing the options",
}

// toolProgress returns a playful, context-aware status line for a running tool,
// derived from the tool name and its argument preview.
func toolProgress(name, arg string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "shell"), strings.Contains(n, "bash"), strings.Contains(n, "exec"):
		return shellProgress(arg)
	case strings.Contains(n, "web_search"):
		return "🔎 searching the web"
	case strings.Contains(n, "search"), strings.Contains(n, "grep"), strings.Contains(n, "find"):
		return "🔎 searching the code"
	case strings.Contains(n, "browser"), strings.Contains(n, "http"), strings.Contains(n, "fetch"), strings.Contains(n, "web"):
		return "🌐 browsing the web"
	case strings.Contains(n, "read"):
		return "📖 reading " + base(arg)
	case strings.Contains(n, "write"), strings.Contains(n, "patch"), strings.Contains(n, "edit"):
		return "📝 writing " + base(arg)
	case strings.Contains(n, "list"), strings.Contains(n, "dir"):
		return "📂 listing " + base(arg)
	case strings.Contains(n, "delegate"), strings.Contains(n, "subagent"), strings.Contains(n, "task"):
		return "🤝 delegating to a sub-agent"
	case strings.Contains(n, "memory"), strings.Contains(n, "recall"):
		return "🧠 recalling from memory"
	case strings.Contains(n, "vision"), strings.Contains(n, "image"), strings.Contains(n, "transcribe"):
		return "🎬 examining media"
	default:
		return "🔧 running " + name
	}
}

// shellProgress reads intent from a shell command.
func shellProgress(cmd string) string {
	c := strings.ToLower(strings.TrimSpace(cmd))
	switch {
	case c == "":
		return "❯ running a command"
	case has(c, "go test", "npm test", "pytest", "cargo test", "jest"), strings.HasPrefix(c, "test "):
		return "🧪 running tests"
	case strings.HasPrefix(c, "git "):
		return gitProgress(c)
	case has(c, "lint", "vet", "gofmt", "prettier", "ruff"):
		return "🧹 linting"
	case has(c, "build", "compile"), strings.HasPrefix(c, "make"), strings.HasPrefix(c, "cargo b"):
		return "🔨 building"
	case has(c, "install", "go mod", "npm i", "yarn", "pip ", "apt", "brew"):
		return "📦 installing dependencies"
	case has(c, "docker", "kubectl", "helm"):
		return "🐳 working with containers"
	case strings.HasPrefix(c, "curl"), strings.HasPrefix(c, "wget"):
		return "🌐 fetching"
	case strings.HasPrefix(c, "ls"), strings.HasPrefix(c, "find"), strings.HasPrefix(c, "tree"):
		return "📂 looking around"
	case has(c, "grep"), prefixAny(c, "cat", "head", "tail", "less", "wc"):
		return "🔎 inspecting output"
	case prefixAny(c, "rm", "mv", "cp", "mkdir", "touch", "chmod"):
		return "🗂 managing files"
	default:
		return "❯ " + truncate(collapse(cmd), 28)
	}
}

// gitProgress reads intent from a git subcommand.
func gitProgress(c string) string {
	switch {
	case strings.Contains(c, "commit"):
		return "📌 committing"
	case strings.Contains(c, "push"):
		return "🚀 pushing"
	case strings.Contains(c, "clone"):
		return "📥 cloning"
	case strings.Contains(c, "pull"), strings.Contains(c, "fetch"):
		return "🔄 syncing with remote"
	case strings.Contains(c, "checkout"), strings.Contains(c, "switch"), strings.Contains(c, "branch"):
		return "🌿 switching branches"
	case strings.Contains(c, "merge"), strings.Contains(c, "rebase"):
		return "🔀 merging"
	default:
		return "🔀 checking git"
	}
}

// base returns a friendly basename for a path-like argument.
func base(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "a file"
	}
	return truncate(filepath.Base(p), 28)
}

// has reports whether s contains any of the substrings.
func has(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// prefixAny reports whether s starts with any of the given commands (followed
// by a space or end, so "cat" matches "cat x" but not "category").
func prefixAny(s string, cmds ...string) bool {
	for _, cmd := range cmds {
		if s == cmd || strings.HasPrefix(s, cmd+" ") {
			return true
		}
	}
	return false
}
