package tui

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ── transcript specialization for plan steps ───────────────────────────────
//
// Plan mutations are ordinary tool_call events, so without help each status
// change renders as an opaque JSON preview row and long runs drown in them.
// We keep ingestion untouched (arrival order, LIFO pairing, stats) and swap
// ONLY the stored preview text for a one-line semantic summary; anything we
// cannot summarize falls back to the generic preview, never the other way.

// planUpdateEntry mirrors one entry of the update verb's updates array.
type planUpdateEntry struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// planArgs is the subset of the plan tool's argument surface that renders.
type planArgs struct {
	Verb    string            `json:"verb"`
	Steps   []json.RawMessage `json:"steps"`
	Updates []planUpdateEntry `json:"updates"`
	StepID  string            `json:"step_id"`
}

// planArgSummary condenses a plan tool_call payload into a short human line,
// e.g. "create · 5 steps" / "p3 → in_progress" / "complete p2".
// Everything model-authored passes through sanitize(); the result keeps the
// same width discipline as argPreview.
func planArgSummary(data string) string {
	var args planArgs
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &args); err != nil {
		return ""
	}
	switch args.Verb {
	case "create":
		if len(args.Steps) == 0 {
			return "create"
		}
		return sanitize(truncate("create · "+strconv.Itoa(len(args.Steps))+" steps", 72))
	case "update":
		parts := make([]string, 0, len(args.Updates))
		for _, u := range args.Updates {
			if u.ID == "" {
				continue
			}
			movements := u.ID + " → "
			if u.Status != "" {
				movements += u.Status
			} else {
				movements += "updated"
			}
			parts = append(parts, sanitize(collapse(movements)))
		}
		if len(parts) == 0 {
			return ""
		}
		return truncate(strings.Join(parts, " · "), 72)
	case "complete":
		if args.StepID == "" {
			return ""
		}
		return sanitize(collapse(args.StepID)) + " → done"
	case "get":
		return "state check"
	default:
		return ""
	}
}
