package server

import (
	"strings"
)

// Kind classifies a Connect failure so the launcher can print one card
// instead of a raw spawn dump.
type Kind int

const (
	KindUnknown Kind = iota
	KindMissingBinary
	KindNotReady
	KindStartFail
)

// Classify maps a Connect error to a diagnosis kind.
func Classify(err error) Kind {
	if err == nil {
		return KindUnknown
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "cannot find"), strings.Contains(s, "executable file not found"):
		return KindMissingBinary
	case strings.Contains(s, "did not become ready"):
		return KindNotReady
	case strings.Contains(s, "start odek serve"):
		return KindStartFail
	default:
		return KindUnknown
	}
}

// Diagnose formats a one-card next action for a Connect failure.
func Diagnose(err error, missingProvider bool) string {
	if err == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("bodek could not start odek serve\n")
	switch Classify(err) {
	case KindMissingBinary:
		b.WriteString("  odek is not on PATH.\n")
		b.WriteString("  next: install odek, or pass --odek-bin, or --url to attach.\n")
	case KindNotReady:
		b.WriteString("  odek started but never became ready.\n")
		b.WriteString("  next: check the serve log, then retry.\n")
	case KindStartFail:
		b.WriteString("  odek serve failed to spawn.\n")
		b.WriteString("  next: check the binary and retry.\n")
	default:
		b.WriteString("  " + err.Error() + "\n")
		b.WriteString("  next: retry, or attach with --url.\n")
	}
	if missingProvider {
		b.WriteString("  no provider key in the environment (DEEPSEEK_API_KEY / OPENAI_API_KEY / …).\n")
	}
	b.WriteString("  retry: ⏎   quit: q")
	return b.String()
}
