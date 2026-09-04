package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// ── thinking intent rail ────────────────────────────────────────────────────

const thinkingExcerptSentences = 2

// lastSentences returns the last n sentences of s, preserving internal
// newlines inside each sentence. A sentence ends at `.!?` followed by
// whitespace or EOF. Fewer than n sentences returns s trimmed.
func lastSentences(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" || n <= 0 {
		return s
	}
	rs := []rune(s)
	type span struct{ start, end int }
	var spans []span
	start := 0
	for i := 0; i < len(rs); i++ {
		if rs[i] != '.' && rs[i] != '!' && rs[i] != '?' {
			continue
		}
		if i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			continue // abbreviation / decimal
		}
		spans = append(spans, span{start, i + 1})
		start = i + 1
	}
	if start < len(rs) {
		if tail := strings.TrimSpace(string(rs[start:])); tail != "" {
			spans = append(spans, span{start, len(rs)})
		}
	}
	if len(spans) <= n {
		return s
	}
	kept := spans[len(spans)-n:]
	return strings.TrimSpace(string(rs[kept[0].start:kept[len(kept)-1].end]))
}

// thinkingExcerpt is the collapsed intent-rail body: the last two sentences,
// never flattened. A runaway sentence is tail-capped so the rail stays short.
func thinkingExcerpt(s string) string {
	ex := lastSentences(s, thinkingExcerptSentences)
	return capThinkingTail(ex, maxThinkingLen)
}

// capThinkingTail keeps the last n runes of s, snapping forward to a
// whitespace so the visible excerpt starts on a word boundary.
func capThinkingTail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	r = r[len(r)-n:]
	for i, c := range r {
		if unicode.IsSpace(c) {
			return strings.TrimSpace(string(r[i+1:]))
		}
	}
	return string(r)
}

// thinkingBeats counts reasoning blocks on the turn — each think→tool/reply
// cycle is one beat of the intent rail.
func thinkingBeats(msg message) int {
	n := 0
	for _, it := range msg.items {
		if it.thinking && strings.TrimSpace(it.text) != "" {
			n++
		}
	}
	if n == 0 && strings.TrimSpace(msg.thinking) != "" {
		return 1
	}
	return n
}

// thinkingDur is the block's elapsed time: stamped on finalize, live
// otherwise. Zero when the block has no start (replayed history).
func thinkingDur(it turnItem, streaming bool) time.Duration {
	if it.dur > 0 {
		return it.dur
	}
	if streaming && !it.started.IsZero() {
		return time.Since(it.started)
	}
	return 0
}

// ── step peek ───────────────────────────────────────────────────────────────

const peekLines = 2

// stepPeek returns the first 1–2 typed-renderer lines of a finished step —
// the glanceable outcome that sits under the head without a full expand.
func stepPeek(name, result string, width int, th theme) []string {
	if strings.TrimSpace(result) == "" {
		return nil
	}
	details := stepDetail(name, result, width, th)
	if len(details) > peekLines {
		return details[:peekLines]
	}
	return details
}

// ── turn receipt ────────────────────────────────────────────────────────────

// receipt is the coding-agent scan line for a finalized turn: files written,
// diff volume, test verdict. Empty fields stay off the string.
type receipt struct {
	files   int
	adds    int
	dels    int
	hasDiff bool
	tests   string // "✓" / "✗" / ""
}

func scanReceipt(msg message) receipt {
	files := map[string]struct{}{}
	var r receipt
	for _, s := range msg.steps {
		if p := touchedPath(s.name, s.arg); p != "" {
			files[p] = struct{}{}
		}
		if a, d, ok := diffStatOf(s.result); ok {
			r.adds += a
			r.dels += d
			r.hasDiff = true
		}
		if !isShellTool(s.name) {
			continue
		}
		if sum, ok := testSummary(s.result); ok {
			if strings.HasPrefix(sum, "✗") || s.isErr {
				r.tests = "✗"
			} else if r.tests != "✗" {
				r.tests = "✓"
			}
		}
	}
	r.files = len(files)
	return r
}

func formatReceipt(r receipt) string {
	var parts []string
	if r.files > 0 {
		parts = append(parts, fmt.Sprintf("touched %d", r.files))
	}
	if r.hasDiff {
		parts = append(parts, fmt.Sprintf("+%d −%d", r.adds, r.dels))
	}
	if r.tests != "" {
		parts = append(parts, "tests "+r.tests)
	}
	return strings.Join(parts, " · ")
}

// touchedPath is the file a write/patch/edit step named. Reads do not
// count — the receipt is what the turn changed, not what it looked at.
func touchedPath(name, arg string) string {
	n := strings.ToLower(name)
	if !strings.Contains(n, "write") && !strings.Contains(n, "patch") && !strings.Contains(n, "edit") {
		return ""
	}
	p := strings.TrimSpace(arg)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func isShellTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "shell") || strings.Contains(n, "bash") || strings.Contains(n, "exec")
}

// ── parallel swarm ──────────────────────────────────────────────────────────

// swarmRun is a consecutive run of unfinished tool items in a live turn.
type swarmRun struct {
	start, end int // inclusive item indices
	earliest   time.Time
}

// nextSwarm, from item index `from`, finds a consecutive unfinished-step
// run of length ≥ 2. Replay / finalized turns never swarm.
func nextSwarm(items []turnItem, steps []step, from int, streaming bool) (swarmRun, bool) {
	if !streaming {
		return swarmRun{}, false
	}
	var run swarmRun
	n := 0
	for i := from; i < len(items); i++ {
		it := items[i]
		if it.thinking || it.reply {
			break
		}
		if it.stepIdx < 0 || it.stepIdx >= len(steps) || steps[it.stepIdx].done {
			break
		}
		if n == 0 {
			run.start = i
			run.earliest = steps[it.stepIdx].started
		}
		run.end = i
		if st := steps[it.stepIdx].started; !st.IsZero() && (run.earliest.IsZero() || st.Before(run.earliest)) {
			run.earliest = st
		}
		n++
	}
	if n < 2 {
		return swarmRun{}, false
	}
	return run, true
}

func swarmLabel(run swarmRun, now time.Time) string {
	n := run.end - run.start + 1
	label := fmt.Sprintf("⬡ parallel · %d running", n)
	if !run.earliest.IsZero() && !now.Before(run.earliest) {
		label += " · " + formatStepDur(now.Sub(run.earliest))
	}
	return label
}
