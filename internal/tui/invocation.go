package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Tool arguments can contain file bodies and prompts. Keep inspection bounded,
// and say so when the wire payload exceeds the limit.
const toolArgsLimit = 256 * 1024

func retainToolArgs(raw string) (string, bool) {
	if len(raw) <= toolArgsLimit {
		return raw, false
	}
	cut := toolArgsLimit
	for cut > 0 && !utf8.RuneStart(raw[cut]) {
		cut--
	}
	return raw[:cut], true
}

// visibleInvocation escapes terminal controls and invisible Unicode instead
// of silently deleting them. The inspector must show when a command contains
// bytes that cannot safely be sent to a terminal as display text.
func visibleInvocation(raw string) string {
	var b strings.Builder
	for len(raw) > 0 {
		r, size := utf8.DecodeRuneInString(raw)
		if r == utf8.RuneError && size == 1 {
			_, _ = fmt.Fprintf(&b, "\\x%02X", raw[0])
			raw = raw[1:]
			continue
		}
		raw = raw[size:]
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case isControl(r):
			_, _ = fmt.Fprintf(&b, "\\x%02X", r)
		case isInvisible(r):
			_, _ = fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	return sanitize(b.String())
}

// invocationText puts the command first for shell tools, followed by every
// other argument. Other tools show the full JSON arguments. The short step
// header remains separate; it must never stand in for this inspection text.
func invocationText(s step) string {
	if s.callArgs == "" && !s.argsOmitted {
		return ""
	}
	raw := s.callArgs
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) == nil && fields != nil {
		if isShellTool(s.name) {
			for _, key := range []string{"command", "cmd"} {
				var command string
				if json.Unmarshal(fields[key], &command) != nil || command == "" {
					continue
				}
				delete(fields, key)
				out := "invocation · command\n" + visibleInvocation(command)
				if len(fields) > 0 {
					if rest, err := json.MarshalIndent(fields, "", "  "); err == nil {
						out += "\nother arguments\n" + visibleInvocation(string(rest))
					}
				}
				return appendArgsLimit(out, s.argsOmitted)
			}
		}
		if pretty, err := json.MarshalIndent(fields, "", "  "); err == nil {
			return appendArgsLimit("invocation · arguments\n"+visibleInvocation(string(pretty)), s.argsOmitted)
		}
	}
	return appendArgsLimit("invocation · arguments\n"+visibleInvocation(raw), s.argsOmitted)
}

func appendArgsLimit(text string, omitted bool) string {
	if omitted {
		head, body, _ := strings.Cut(text, "\n")
		return head + " · limited to 256 KiB\n" + body +
			"\n… remaining invocation arguments omitted"
	}
	return text
}

func invocationDetailLines(s step, width int, th theme) []string {
	text := invocationText(s)
	if text == "" {
		return nil
	}
	var out []string
	for i, line := range strings.Split(ansi.Hardwrap(text, max(1, width), true), "\n") {
		style := th.stepRes
		if i == 0 || line == "other arguments" || strings.HasPrefix(line, "… remaining invocation") {
			style = th.stepArg
		}
		out = append(out, style.Render(line))
	}
	return out
}
