package tui

import (
	"encoding/json"
	"fmt"
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
		// a thinking item), or start a new one after a tool call.
		if i := m.cur(); i >= 0 {
			msg := &m.msgs[i]
			if n := len(msg.items); n > 0 && msg.items[n-1].thinking {
				msg.items[n-1].text = capThinkingText(msg.items[n-1].text+sanitize(ev.Content), maxThinkingLen)
			} else {
				msg.items = append(msg.items, turnItem{thinking: true,
					text: capThinkingText(sanitize(ev.Content), maxThinkingLen)})
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
		m.winCtxTok = ev.ContextTokens
		m.lastLatency = ev.Latency

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

	case "approval_request":
		e := ev
		m.approval = &e
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
	return m, tea.Batch(listen(m.events), m.noticeTimer(prevSeq))
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
	s := sanitize(data)
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

// maxThinkingLen caps the live "thinking…" excerpt so a verbose reasoning
// stream does not push the transcript off-screen.
const maxThinkingLen = 240

// capThinkingText trims s to at most n runes, starting at the next whitespace
// so the visible excerpt does not begin mid-word.
func capThinkingText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	r = r[len(r)-n:]
	for i, c := range r {
		if unicode.IsSpace(c) {
			r = r[i+1:]
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
