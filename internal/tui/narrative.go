package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// ── thinking intent rail ────────────────────────────────────────────────────

const (
	thinkingExcerptSentences = 2
	thinkingLiveStem         = 72 // frozen opening clause until a sentence lands
)

type sentSpan struct {
	start, end int
	complete   bool // ended on .!? ; false = still-growing tail
}

// sentenceSpans splits s on `.!?` followed by whitespace or EOF.
func sentenceSpans(s string) []sentSpan {
	rs := []rune(s)
	var spans []sentSpan
	start := 0
	for i := 0; i < len(rs); i++ {
		if rs[i] != '.' && rs[i] != '!' && rs[i] != '?' {
			continue
		}
		if i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			continue // abbreviation / decimal
		}
		spans = append(spans, sentSpan{start, i + 1, true})
		start = i + 1
	}
	if start < len(rs) {
		if tail := strings.TrimSpace(string(rs[start:])); tail != "" {
			spans = append(spans, sentSpan{start, len(rs), false})
		}
	}
	return spans
}

func joinSpans(s string, spans []sentSpan) string {
	if len(spans) == 0 {
		return ""
	}
	rs := []rune(s)
	return strings.TrimSpace(string(rs[spans[0].start:spans[len(spans)-1].end]))
}

// lastSentences returns the last n sentences of s, preserving internal
// newlines inside each sentence. A sentence ends at `.!?` followed by
// whitespace or EOF. Fewer than n sentences returns s trimmed.
func lastSentences(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" || n <= 0 {
		return s
	}
	spans := sentenceSpans(s)
	if len(spans) <= n {
		return s
	}
	return joinSpans(s, spans[len(spans)-n:])
}

// lastCompleteSentences is lastSentences without the unfinished tail — the
// live rail holds these so a fast model cannot turn the excerpt into a ticker.
func lastCompleteSentences(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" || n <= 0 {
		return ""
	}
	var done []sentSpan
	for _, sp := range sentenceSpans(s) {
		if sp.complete {
			done = append(done, sp)
		}
	}
	if len(done) == 0 {
		return ""
	}
	if len(done) > n {
		done = done[len(done)-n:]
	}
	return joinSpans(s, done)
}

// thinkingExcerpt is the collapsed intent-rail body for a sealed or
// finalized block: the last two sentences, never flattened.
func thinkingExcerpt(s string) string {
	ex := lastSentences(s, thinkingExcerptSentences)
	return capThinkingTail(ex, maxThinkingLen)
}

// thinkingExcerptLive is the collapsed rail while a think cycle is still
// streaming: finished sentences only, held until the next one lands. Before
// the first period, a short opening stem freezes once it fills so the
// transcript does not chase tokens.
func thinkingExcerptLive(s string) string {
	if held := lastCompleteSentences(s, thinkingExcerptSentences); held != "" {
		return capThinkingTail(held, maxThinkingLen)
	}
	return capThinkingText(strings.TrimSpace(s), thinkingLiveStem)
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

// thinkingBeatIndex is this block's 1-based position among the turn's
// reasoning blocks (beat 2 of 3).
func thinkingBeatIndex(msg message, itemIdx int) int {
	k := 0
	for i := 0; i <= itemIdx && i < len(msg.items); i++ {
		if msg.items[i].thinking && strings.TrimSpace(msg.items[i].text) != "" {
			k++
		}
	}
	if k == 0 {
		return 1
	}
	return k
}

// sealThinking freezes the open reasoning block's elapsed time. A think
// cycle ends when the model acts (tool call or reply) — the clock must
// not keep running through the tool that follows.
func sealThinking(msg *message) {
	n := len(msg.items)
	if n == 0 {
		return
	}
	it := &msg.items[n-1]
	if it.thinking && it.dur == 0 && !it.started.IsZero() {
		it.dur = time.Since(it.started)
	}
}

// thinkingDur is the block's elapsed time: stamped when the cycle yields
// (tool / reply / finalize), live only while this block is still open.
// Zero when the block has no start (replayed history).
func thinkingDur(it turnItem, streaming bool) time.Duration {
	if it.dur > 0 {
		return it.dur
	}
	if streaming && !it.started.IsZero() {
		return time.Since(it.started)
	}
	return 0
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
