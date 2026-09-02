package tui

import "strings"

// ── provider-failure classification ─────────────────────────────────────────
//
// Turn failures arrive as raw provider-client text ("iteration 17: llm:
// stream idle for over 2m0s without an event"). Under parallel-agent load
// the dominant failure is a stalled stream — the provider accepted the
// prompt and then went silent until the watchdog aborted — not an HTTP
// error; raw 429s are absorbed by the client's retry budget and only
// surface when it is exhausted. The classifier sorts the wire strings into
// the handful of classes a user can act on, so the turn card explains what
// happened and how to recover instead of echoing an LLM-internal line.

type errClass uint8

const (
	errOther     errClass = iota // unrecognized — show the sanitized raw message
	errStall                     // provider accepted, then no stream events (watchdog abort)
	errRateLimit                 // HTTP 429 survived the client's retry budget
	errConnDrop                  // TCP connection dropped mid-stream
	errTimeout                   // deadline exceeded
)

// classifyErr maps a raw failure message to a class. Matching is on
// lowercased substrings of the stable provider-client phrasings; anything
// unrecognized stays errOther and renders verbatim.
func classifyErr(msg string) errClass {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "rate limit"), strings.Contains(l, "status 429"),
		strings.Contains(l, "http 429"), strings.Contains(l, "code 429"):
		return errRateLimit
	case strings.Contains(l, "stream idle"), strings.Contains(l, "without an event"):
		return errStall
	case strings.Contains(l, "connection reset"), strings.Contains(l, "broken pipe"):
		return errConnDrop
	case strings.Contains(l, "timed out"), strings.Contains(l, "deadline exceeded"):
		return errTimeout
	default:
		return errOther
	}
}

// errorCard renders a failed turn's marker in plain language. Known classes
// explain the failure and how to recover; unknown ones fall back to the
// sanitized raw message. The resend hint is appended only when a retry can
// actually fire — a preserved prompt exists (⏎ on the empty input, or
// /retry).
func (m *Model) errorCard(raw string) string {
	hint := ""
	if m.lastPrompt != "" {
		hint = " — ⏎ resends the prompt · /retry"
	}
	switch classifyErr(raw) {
	case errStall:
		return "**⚠ Provider stream stalled.** The provider accepted the prompt, then sent nothing for over 2m — usually throttling under parallel agent load" + hint + "."
	case errRateLimit:
		return "**⚠ Provider rate-limited (HTTP 429).** Concurrent agents exhausted the provider's stream budget" + hint + "."
	case errConnDrop:
		return "**⚠ Provider connection dropped** mid-stream" + hint + "."
	case errTimeout:
		return "**⚠ Provider request timed out.**" + hint
	default:
		return "**Error:** " + sanitize(raw)
	}
}
