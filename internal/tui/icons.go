package tui

import "strings"

// toolGlyph returns a tasteful monochrome glyph for a tool, so the activity
// feed reads at a glance. Matching is by substring to cover odek's native
// tools, MCP tools (server__tool), and sub-agent variants.
func toolGlyph(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "shell"), strings.Contains(n, "bash"), strings.Contains(n, "exec"):
		return "❯"
	case strings.Contains(n, "write"), strings.Contains(n, "patch"), strings.Contains(n, "edit"):
		return "✎"
	case strings.Contains(n, "read"):
		return "◰"
	case strings.Contains(n, "list"), strings.Contains(n, "dir"), strings.Contains(n, "ls"):
		return "▤"
	case strings.Contains(n, "search"), strings.Contains(n, "grep"), strings.Contains(n, "find"):
		return "⌕"
	case strings.Contains(n, "browser"), strings.Contains(n, "http"), strings.Contains(n, "fetch"), strings.Contains(n, "web"):
		return "◉"
	case strings.Contains(n, "delegate"), strings.Contains(n, "subagent"), strings.Contains(n, "task"):
		return "⑂"
	case strings.Contains(n, "memory"), strings.Contains(n, "recall"):
		return "❖"
	case strings.Contains(n, "vision"), strings.Contains(n, "image"), strings.Contains(n, "transcribe"):
		return "◎"
	default:
		return "✦"
	}
}

// resourceGlyph returns a glyph for an @-reference result type.
func resourceGlyph(typ string) string {
	switch typ {
	case "command":
		return "/"
	case "session":
		return "⟳"
	case "skill":
		return "✦"
	default: // file
		return "≡"
	}
}
