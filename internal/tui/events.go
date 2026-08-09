package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// noticeTTL is how long transient info traces (skill / memory / signal /
// subagent) stay on screen before fading out.
const noticeTTL = 3 * time.Second

// noticeExpireMsg fires noticeTTL after a transient notice was added.
type noticeExpireMsg struct {
	seq int
}

func (m *Model) handleEvent(ev client.Event) (tea.Model, tea.Cmd) {
	prevSeq := m.noticeSeq
	stream := false // high-frequency event: coalesce the render (see queueRender)
	switch ev.Type {
	case "session":
		m.sessionID = ev.SessionID
		if ev.AuthToken != "" {
			m.authToken = ev.AuthToken
			m.tokens.Set(ev.SessionID, ev.AuthToken)
		}
		if ev.Model != "" {
			m.model = ev.Model
			m.resolveMaxContext()
		}
		m.sandbox = ev.Sandbox

	case "thinking":
		// Append to the open reasoning block (the last timeline item when it is
		// a thinking item), or start a new one after a tool call. The full text
		// is stored; only the rendered excerpt is capped (see maxThinkingLen),
		// so expandAll can unfold the complete block once the turn finalizes.
		if i := m.cur(); i >= 0 {
			msg := &m.msgs[i]
			if n := len(msg.items); n > 0 && msg.items[n-1].thinking {
				msg.items[n-1].text += sanitize(ev.Content)
			} else {
				msg.items = append(msg.items, turnItem{thinking: true,
					text: sanitize(ev.Content)})
			}
		}
		m.status = "thinking"
		stream = true

	case "token":
		if i := m.cur(); i >= 0 {
			m.msgs[i].content += sanitize(ev.Content)
			m.msgs[i].streaming = true
		}
		m.status = "responding"
		stream = true

	case "tool_call":
		arg := argPreview(ev.Data)
		if i := m.cur(); i >= 0 {
			m.msgs[i].steps = append(m.msgs[i].steps,
				step{name: ev.Name, arg: arg, subagent: isSubagent(ev.Name), started: time.Now()})
			m.msgs[i].items = append(m.msgs[i].items, turnItem{stepIdx: len(m.msgs[i].steps) - 1})
		}
		m.lastTool = ev.Name
		m.lastArg = arg
		m.status = "running " + ev.Name

	case "tool_result":
		if i := m.cur(); i >= 0 {
			steps := m.msgs[i].steps
			for j := len(steps) - 1; j >= 0; j-- {
				if steps[j].name == ev.Name && !steps[j].done {
					steps[j].done = true
					steps[j].result = resultPreview(ev.Data)
					steps[j].isErr = looksLikeError(steps[j].result)
					if !steps[j].started.IsZero() {
						steps[j].dur = time.Since(steps[j].started)
					}
					break
				}
			}
		}
		m.lastTool = ""
		m.lastArg = ""

	case "done":
		// Capture per-turn telemetry from the live message BEFORE finalize()
		// clears curIdx.
		if i := m.cur(); i >= 0 {
			wall := time.Duration(0)
			if !m.runStart.IsZero() {
				wall = time.Since(m.runStart)
			}
			thought := false
			for _, it := range m.msgs[i].items {
				if it.thinking {
					thought = true
					break
				}
			}
			ts := turnStats{
				latency:    ev.Latency,
				wall:       wall,
				ctxTok:     ev.ContextTokens,
				outTok:     ev.OutputTokens,
				toolCount:  len(m.msgs[i].steps),
				toolGlyphs: stepGlyphs(m.msgs[i].steps),
				thought:    thought,
			}
			m.msgs[i].stats = &ts
			m.turnStats = append(m.turnStats, ts)
			m.toolTotal += ts.toolCount
		}
		m.renderPending = false // the turn's final state renders now, not on a flush
		m.finalize()
		m.busy = false
		m.lastTool = ""
		m.lastArg = ""
		m.status = "ready"
		m.sessCtxTok = ev.SessionContextTokens
		m.sessOutTok = ev.SessionOutputTokens
		// ev.ContextTokens is cumulative for the run (sum of prompt tokens
		// across all LLM calls), so the live window fill is the delta against
		// the last report — the final request's prompt size.
		if fill := ev.ContextTokens - m.runCtxCum; ev.ContextTokens > 0 && fill > 0 {
			m.winCtxTok = fill
		}
		m.runCtxCum = 0 // run over — the next run's cumulative restarts at zero
		m.lastLatency = ev.Latency
		m.relayout() // the busy status line releases its row

	case "usage":
		// Per-iteration report from odek serve: keeps the header gauge live
		// during multi-turn runs instead of waiting for "done". contextTokens
		// is cumulative for the run, so the window fill is the delta against
		// the previous report — the last request's prompt size, which drops
		// again after odek trims history. A zero value means the provider
		// reported no usage — keep the last known fill rather than zeroing
		// the gauge.
		if ev.ContextTokens > 0 {
			if fill := ev.ContextTokens - m.runCtxCum; fill > 0 {
				m.winCtxTok = fill
			}
			m.runCtxCum = ev.ContextTokens
		}
		stream = true

	case "error":
		if i := m.cur(); i >= 0 && m.msgs[i].content == "" {
			m.msgs[i].content = "**Error:** " + ev.Message
		} else {
			m.addNote("error: " + ev.Message)
		}
		m.renderPending = false
		m.finalize()
		m.busy = false
		m.lastTool = ""
		m.lastArg = ""
		m.status = "error"
		m.relayout() // the busy status line releases its row

	case "approval_request":
		e := ev
		m.approval = &e
		m.apprSel = 0
		m.apprExpanded = false
		m.status = "approval required"
		m.relayout() // the panel is taller than the textarea — shrink the viewport

	case "skill_event":
		m.addTransientNote("skill · " + strings.TrimSpace(ev.SubType+" "+ev.SkillName) + eventTail(ev))
	case "memory_event":
		m.addTransientNote("memory · " + strings.TrimSpace(ev.SubType+" "+ev.Target) + eventTail(ev))
	case "agent_signal":
		m.addTransientNote("signal · " + strings.TrimSpace(ev.SubType+" "+ev.Detail) + eventTail(ev))
	case "subagent_log":
		line := strings.TrimSpace(ev.SubType + " " + ev.Name)
		if d := collapse(ev.Detail); d != "" {
			line = strings.TrimSpace(line + " · " + d)
		}
		line += eventTail(ev)
		// Nest the log under the in-flight sub-agent step when there is one;
		// otherwise (resumed turn, idle, or an unwrapped log) keep it as a notice.
		if i := m.cur(); i >= 0 && m.attachSubLog(i, line) {
			break
		}
		m.addTransientNote("subagent · " + line)

	case client.EventDisconnected:
		m.disconn = true
		m.busy = false
		m.renderPending = false
		// A turn in flight when the socket drops will never finish: close it
		// out with an interrupted marker instead of leaving it streaming
		// forever. Idempotent — with no open turn this is a no-op, so a
		// later resume is untouched.
		if i := m.cur(); i >= 0 {
			if m.msgs[i].content == "" {
				m.msgs[i].content = "**Interrupted:** connection lost"
			} else {
				m.msgs[i].content += "\n\n**Interrupted:** connection lost"
			}
		}
		m.finalize()
		m.relayout() // the busy status line is gone with the socket
		if cmd := m.scheduleReconnect(0); cmd != nil {
			m.status = "reconnecting…"
			m.addNote("connection lost — reconnecting…")
			m.refresh()
			return m, cmd
		}
		m.status = "disconnected"
		m.addNote("disconnected from odek serve")
		if m.opts.LogPath != "" {
			m.addNote("server log · " + m.opts.LogPath)
		}
		m.refresh()
		return m, nil
	}

	if stream {
		return m, tea.Batch(listen(m.events), m.noticeTimer(prevSeq), m.queueRender())
	}
	m.refresh()
	// A turn that just ended (done / error) drains the next queued prompt.
	return m, tea.Batch(listen(m.events), m.noticeTimer(prevSeq), m.sendQueued())
}

// stepGlyphs returns up to 4 deduped tool glyphs for a turn's steps, in
// first-seen order, for the per-turn stat line.
func stepGlyphs(steps []step) []string {
	const max = 4
	seen := make(map[string]bool, len(steps))
	out := make([]string, 0, max)
	for _, s := range steps {
		g := toolGlyph(s.name)
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
		if len(out) == max {
			break
		}
	}
	return out
}

// eventTail renders the optional ×count / #task-index suffix shared by the
// engine-event notices (skill / memory / signal / subagent).
func eventTail(ev client.Event) string {
	s := ""
	if ev.Count > 0 {
		s += fmt.Sprintf(" ×%d", ev.Count)
	}
	if ev.TaskIdx > 0 {
		s += fmt.Sprintf(" #%d", ev.TaskIdx)
	}
	return s
}

// normalizeToolResult prepares raw tool output for DISPLAY ONLY — the stored
// and forwarded data is never touched. It handles two render-noise shapes:
//  1. JSON envelopes (read_file, search_files, …): Go's encoding/json
//     HTML-escapes <, >, & in string values, so the odek prompt-injection
//     wrapper shows up as <untrusted_content_… — the envelope is
//     decoded and the real content rendered, with remaining scalar metadata
//     as a one-line footer.
//  2. untrusted_content wrappers, folded into a "⚠ untrusted: <source>"
//     badge line. Both the literal tag form (live stream events) and the
//     < escaped form (undecoded JSON envelopes) are recognized.
//
// Fail-safe: anything that does not match a known shape exactly is returned
// unchanged — normalization is never lossy.
func normalizeToolResult(data string) string {
	return foldUntrustedWrappers(decodeToolEnvelope(data))
}

// decodeToolEnvelope unwraps a JSON object with a string "content" field,
// returning the decoded content plus a footer line of the remaining scalar
// metadata (e.g. "total_lines: 430"). Nested objects/arrays, missing
// content, or invalid JSON mean an unknown shape — returned unchanged.
func decodeToolEnvelope(data string) string {
	t := strings.TrimSpace(data)
	if len(t) < 2 || t[0] != '{' {
		return data
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(t), &env); err != nil {
		return data
	}
	content, ok := env["content"].(string)
	if !ok {
		return data
	}
	meta := make(map[string]string, len(env)-1)
	for k, v := range env {
		if k == "content" || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			meta[k] = x
		case float64:
			meta[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			meta[k] = strconv.FormatBool(x)
		default:
			return data // nested value: not a known envelope shape
		}
	}
	if len(meta) == 0 {
		return content
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+meta[k])
	}
	return content + "\n(" + strings.Join(parts, " · ") + ")"
}

// untrustedWrapperRe matches an odek prompt-injection wrapper in either its
// literal form or the < escaped form produced by encoding/json.
// Capture groups: 1 = source attribute, 2 = wrapped body.
var untrustedWrapperRe = regexp.MustCompile(
	`(?s)(?:<|\\u003c)untrusted_content_[0-9a-f]+ source=\\?"([^"]*)\\?"(?:>|\\u003e)\n?(.*?)\n?(?:<|\\u003c)/untrusted_content_[0-9a-f]+(?:>|\\u003e)`)

// foldUntrustedWrappers replaces each untrusted_content wrapper with a badge
// line naming the source, followed by the wrapped body.
func foldUntrustedWrappers(s string) string {
	if !strings.Contains(s, "untrusted_content_") {
		return s
	}
	return untrustedWrapperRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := untrustedWrapperRe.FindStringSubmatch(m)
		badge := "⚠ untrusted: " + sub[1]
		if body := sub[2]; body != "" {
			return badge + "\n" + body
		}
		return badge
	})
}

// cur returns the index of the active streaming assistant message, or -1.
func (m *Model) cur() int {
	if m.curIdx >= 0 && m.curIdx < len(m.msgs) {
		return m.curIdx
	}
	return -1
}

// finalize closes out the streaming assistant message, rendering its markdown.
func (m *Model) finalize() {
	if i := m.cur(); i >= 0 {
		m.msgs[i].streaming = false
		m.msgs[i].rendered = m.render(m.msgs[i].content)
		// Keep the turn's reasoning concatenated on the message for
		// compatibility; the timeline (items) drives the actual rendering.
		var thoughts []string
		for _, it := range m.msgs[i].items {
			if it.thinking {
				thoughts = append(thoughts, it.text)
			}
		}
		m.msgs[i].thinking = strings.Join(thoughts, "\n")
	}
	m.curIdx = -1
}

// addNote appends a sticky notice (errors, disconnects) that stays until
// pushed out by newer ones.
func (m *Model) addNote(s string) {
	m.pushNote(s, time.Time{})
}

// addTransientNote appends an info trace that fades after noticeTTL.
func (m *Model) addTransientNote(s string) {
	m.pushNote(s, time.Now().Add(noticeTTL))
	m.noticeSeq++
}

// transientNoteCmd adds a transient note and returns the cmd that sweeps it
// after noticeTTL. handleEvent arms the sweep itself; every other caller
// (key handlers, async results) must batch this cmd or the note only fades
// on the next unrelated render.
func (m *Model) transientNoteCmd(s string) tea.Cmd {
	prev := m.noticeSeq
	m.addTransientNote(s)
	return m.noticeTimer(prev)
}

func (m *Model) pushNote(s string, exp time.Time) {
	m.notices = append(m.notices, sanitize(s))
	m.noticeExp = append(m.noticeExp, exp)
	if len(m.notices) > 6 {
		m.notices = m.notices[len(m.notices)-6:]
		m.noticeExp = m.noticeExp[len(m.noticeExp)-6:]
	}
}

// pruneNotices drops transient notices whose expiry has passed.
func (m *Model) pruneNotices(now time.Time) {
	kept := m.notices[:0]
	keptExp := m.noticeExp[:0]
	for i, n := range m.notices {
		if exp := m.noticeExp[i]; exp.IsZero() || now.Before(exp) {
			kept = append(kept, n)
			keptExp = append(keptExp, exp)
		}
	}
	m.notices = kept
	m.noticeExp = keptExp
}

// noticeTimer schedules the expiry sweep when a transient notice was added
// since prevSeq; otherwise it returns nil.
func (m *Model) noticeTimer(prevSeq int) tea.Cmd {
	if m.noticeSeq == prevSeq {
		return nil
	}
	seq := m.noticeSeq
	return tea.Tick(noticeTTL, func(time.Time) tea.Msg {
		return noticeExpireMsg{seq: seq}
	})
}

// argPreview extracts a short, human-friendly summary from a tool's JSON args.
func argPreview(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return truncate(collapse(data), 72)
	}
	for _, key := range []string{
		"command", "cmd", "path", "file", "pattern", "query", "url",
		"prompt", "task", "description", "instruction",
	} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncate(collapse(s), 72)
			}
		}
	}
	parts := make([]string, 0, len(m))
	for _, v := range m {
		if s, ok := v.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return truncate(collapse(strings.Join(parts, " ")), 72)
}

// resultPreview sanitizes tool output and caps it to a generous number of
// lines, so the transcript can show a useful excerpt (rendered by renderSteps)
// without retaining the unbounded output of a chatty tool.
func resultPreview(data string) string {
	s := sanitize(normalizeToolResult(data))
	lines := strings.Split(s, "\n")
	const cap = 200
	if len(lines) > cap {
		lines = lines[:cap]
	}
	return strings.Join(lines, "\n")
}

// isSubagent reports whether a tool name denotes a sub-agent delegation. The
// substrings mirror toolGlyph / toolProgress so the three stay consistent.
func isSubagent(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "delegate") ||
		strings.Contains(n, "subagent") ||
		strings.Contains(n, "task")
}

// looksLikeError reports whether a tool result reads as a failure. It is
// deliberately conservative — keyed off leading error tokens and a couple of
// unambiguous shell phrases — so ordinary output that merely mentions "error"
// is not tinted red.
func looksLikeError(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(t, "error"),
		strings.HasPrefix(t, "fatal"),
		strings.HasPrefix(t, "panic:"),
		strings.HasPrefix(t, "traceback"),
		strings.HasPrefix(t, "exception"),
		strings.HasPrefix(t, "exit status"):
		return true
	}
	return strings.Contains(t, "command not found") ||
		strings.Contains(t, "no such file or directory")
}

// attachSubLog appends a sub-agent activity line to the most recent sub-agent
// step in message i, reporting whether one was found.
func (m *Model) attachSubLog(i int, line string) bool {
	const maxSubLogs = 8
	steps := m.msgs[i].steps
	for j := len(steps) - 1; j >= 0; j-- {
		if !steps[j].subagent {
			continue
		}
		if len(steps[j].logs) < maxSubLogs {
			steps[j].logs = append(steps[j].logs, sanitize(line))
		}
		return true
	}
	return false
}

// maxThinkingLen caps the rendered "thinking…" excerpt so a verbose reasoning
// block does not push the transcript off-screen. The full text stays stored on
// the turn item; expandAll renders it whole (for finalized turns).
const maxThinkingLen = 240

// capThinkingText trims s to its first n runes, backing off to the last
// whitespace so the visible excerpt does not stop mid-word. Showing the head
// orients the reader at the thought's beginning, not its end.
func capThinkingText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	r = r[:n]
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsSpace(r[i]) {
			r = r[:i]
			break
		}
	}
	return string(r)
}

func collapse(s string) string {
	return strings.Join(strings.Fields(sanitize(s)), " ")
}

// stripToolResultFrame unwraps the delimiter frame odek adds around persisted
// tool results (a "┌── TOOL RESULT: …" header line and a matching
// "└── END TOOL RESULT: …" footer) for prompt-injection safety, returning the
// raw inner output. Live tool_result events carry the unframed output, so
// resume strips the frame to render both identically. Unframed input is
// returned unchanged.
func stripToolResultFrame(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) >= 3 &&
		strings.HasPrefix(lines[0], "┌── TOOL RESULT:") &&
		strings.HasPrefix(lines[len(lines)-1], "└── END TOOL RESULT:") {
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	return s
}
