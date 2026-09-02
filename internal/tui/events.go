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

// alertTTL is how long alert-tier notices (errors, warnings, disconnects,
// shutdown / upgrade hints) dwell before fading — longer than the info
// traces so a glance away doesn't miss them, but bounded like everything
// else in the strip. Durable state (disconnected, server shut down) lives
// in the header badge, not here.
const alertTTL = 10 * time.Second

// noticeExpireMsg fires when the earliest pending notice expiry passes; the
// handler prunes expired notices and re-arms the sweep while any remain.
type noticeExpireMsg struct{}

func (m *Model) handleEvent(ev client.Event) (tea.Model, tea.Cmd) {
	stream := false  // high-frequency event: coalesce the render (see queueRender)
	var attn tea.Cmd // terminal attention (title/bell/notify); joined into the return batch
	switch ev.Type {
	case "session":
		prevSession := m.sessionID
		m.sessionID = ev.SessionID
		if ev.AuthToken != "" {
			m.authToken = ev.AuthToken
			m.tokens.Set(ev.SessionID, ev.AuthToken)
		}
		if prevSession != "" && ev.SessionID != prevSession {
			m.planResetPending = true // switch/attach: drop + refetch at the tail
		}
		if ev.Model != "" {
			m.model = collapse(ev.Model)
			m.resolveMaxContext()
		}
		m.sandbox = ev.Sandbox

	case "thinking", "thinking_delta":
		// Bulk reasoning and live streamed fragments (streaming on) share one
		// path: append to the open reasoning block (the last timeline item
		// when it is a thinking item), or start a new one after a tool call.
		// The full text is stored; only the rendered excerpt is capped (see
		// maxThinkingLen), so expandAll can unfold the complete block once the
		// turn finalizes.
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

	case "token", "token_delta":
		// token_delta is the live fragment stream (server --stream); with
		// fragments delivered the server suppresses the bulk token re-send,
		// so both event types must accumulate the same way. Prose lands on
		// the timeline as its own reply segment — appended to the open one,
		// or opened fresh after reasoning/tools — so each think→reply cycle
		// renders independently (appendReply keeps msg.content in sync).
		if i := m.cur(); i >= 0 {
			appendReply(&m.msgs[i], sanitize(ev.Content))
			m.msgs[i].streaming = true
		}
		m.status = "responding"
		stream = true

	case "tool_call":
		arg := argPreview(ev.Data)
		if ev.Name == "plan" {
			if s := planArgSummary(ev.Data); s != "" {
				arg = s // semantic one-liner replaces the JSON blob (docs §4A)
			}
		}
		nm := collapse(ev.Name) // tool names are wire-borne; collapse before anything renders them
		if i := m.cur(); i >= 0 {
			m.msgs[i].steps = append(m.msgs[i].steps,
				step{name: nm, arg: arg, subagent: isSubagent(nm), started: time.Now()})
			last := len(m.msgs[i].steps) - 1
			if m.msgs[i].steps[last].subagent {
				// Per-task identity (goals, profiles) lives in the parent's
				// argument, not on the subagent_state frames.
				m.msgs[i].steps[last].manifest = parseDelegateManifest(ev.Data)
			}
			m.msgs[i].items = append(m.msgs[i].items, turnItem{stepIdx: last})
		}
		m.lastTool = nm
		m.lastArg = arg
		if ev.Name == "plan" {
			// Every engine plan mutation rides an ordinary tool_call: schedule
			// the debounced structured-view refresh (see plan.go).
			m.planTrig = true
		}
		m.status = "running " + nm

	case "tool_result":
		if i := m.cur(); i >= 0 {
			steps := m.msgs[i].steps
			for j := len(steps) - 1; j >= 0; j-- {
				if steps[j].name == ev.Name && !steps[j].done {
					steps[j].done = true
					steps[j].result = resultPreview(ev.Data)
					steps[j].isErr = looksLikeError(steps[j].result)
					if steps[j].subagent {
						steps[j].resultCard = parseAgentResult(ev.Data)
						if rc := steps[j].resultCard; rc != nil && rc.denialsTotal > 0 {
							note := fmt.Sprintf("sub-agent · %d denied op", rc.denialsTotal)
							if rc.denialsTotal > 1 {
								note += "s"
							}
							m.addTransientNote(note + " — see the result card")
						}
					}
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
				cacheWrite: ev.CacheCreationTokens,
				cacheRead:  ev.CacheReadTokens,
				cachedTok:  ev.CachedTokens,
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
		attn = m.attentionCmd(m.attentionFor(attentionDone))

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

	case "server_info", "pong":
		// The connect hello and every heartbeat reply carry the same server
		// snapshot. Only the pong closes a round trip.
		if ev.Type == "pong" && !m.pingSentAt.IsZero() {
			m.rtt = time.Since(m.pingSentAt)
			m.pingSentAt = time.Time{}
		}
		if ev.Version != "" {
			m.odekVersion = collapse(ev.Version)
		}
		if ev.Model != "" && m.model == "" {
			m.model = collapse(ev.Model) // only until the first session/prompt reports the live one
			m.resolveMaxContext()
		}
		m.sandbox = ev.Sandbox
		m.serverStream = ev.Stream
		m.srvUptime = time.Duration(ev.UptimeSeconds) * time.Second
		m.srvConns = ev.WSConnections

	case "cancelled":
		// The server honored a cancel for a session on this connection. The
		// aborted run follows with an error event ("context canceled") —
		// flagged here so that trailing event renders as a clean cancel
		// marker instead of a scary error bubble.
		if ev.Idle {
			m.addTransientNote("cancel: nothing was running")
			break
		}
		m.cancelAck = true
		m.status = "cancelling"

	case "approval_ack":
		// The panel already closed when the answer was sent; the ack needs no
		// UI beyond keeping the transcript quiet.

	case "error":
		// A cancel shows up here: the aborted run returns ctx.Err(). When we
		// saw the cancelled event (or the message is unambiguous context
		// cancellation — e.g. a REST cancel from elsewhere), close the turn
		// as cancelled rather than as a failure.
		cancelled := m.cancelAck || isContextCanceled(ev.Message)
		m.cancelAck = false
		if i := m.cur(); i >= 0 {
			if cancelled {
				markCancel(&m.msgs[i])
			} else if m.msgs[i].content == "" {
				setTurnMarker(&m.msgs[i], "**Error:** "+sanitize(ev.Message))
			} else {
				m.addNote("error: " + ev.Message)
			}
		} else if !cancelled {
			m.addNote("error: " + ev.Message)
		}
		m.renderPending = false
		m.finalize()
		m.busy = false
		m.lastTool = ""
		m.lastArg = ""
		if cancelled {
			m.status = "ready"
		} else {
			m.status = "error"
		}
		m.relayout() // the busy status line releases its row

	case "approval_request":
		// odek runs parallel tools, so several requests can be in flight at
		// once — queue them FIFO; the panel answers the head and shows the
		// remaining count. Input state resets only when the head changes.
		if len(m.approvals) == 0 {
			m.resetApprovalInput()
		}
		m.approvals = append(m.approvals, ev)
		m.stampApprovalDeadline(ev) // the engine expires the request server-side — track its clock
		m.status = "approval required"
		m.relayout() // the panel is taller than the textarea — shrink the viewport
		attn = m.attentionCmd(m.attentionFor(attentionApproval))

	case "skill_event":
		if ev.SubType == "suggested" {
			e := ev
			e.SkillName = collapse(e.SkillName) // model-controlled wire text renders on the card
			e.Detail = collapse(e.Detail)
			m.skillSuggest = &e // the card shows until answered or the next prompt
			m.relayout()
		}
		m.addTransientNote("skill · " + strings.TrimSpace(ev.SubType+" "+ev.SkillName) + eventTail(ev))
	case "memory_event":
		m.addTransientNote("memory · " + strings.TrimSpace(ev.SubType+" "+ev.Target) + eventTail(ev))
	case "agent_signal":
		if ev.SubType == "trim" || ev.SubType == "tool_running" {
			// Engine housekeeping: context trimming and tool-running
			// heartbeats duplicate what the transcript already shows
			// (the in-flight step spinner) — never reach the strip.
			break
		}
		m.addTransientNote("signal · " + strings.TrimSpace(ev.SubType+" "+ev.Detail) + eventTail(ev))
	case "subagent_log":
		line := strings.TrimSpace(ev.SubType + " " + ev.Name)
		// The relay delivers the payload in data (Detail is only a legacy
		// fallback) and the child's log status in status — surface both,
		// capped like every other one-line preview.
		if d := collapse(ev.Data); d != "" {
			line = strings.TrimSpace(line + " · " + truncate(d, 72))
		} else if d := collapse(ev.Detail); d != "" {
			line = strings.TrimSpace(line + " · " + truncate(d, 72))
		}
		if ev.Status != "" {
			line = strings.TrimSpace(line + " · " + ev.Status)
		}
		line += eventTail(ev)
		// Nest the log under the in-flight sub-agent step when there is one;
		// otherwise (resumed turn, idle, or an unwrapped log) keep it as a notice.
		if i := m.cur(); i >= 0 && m.attachSubLog(i, line) {
			break
		}
		m.addTransientNote("subagent · " + line)

	case "subagent_state":
		// Per-task lifecycle telemetry (odek v1.30+): attach to the
		// in-flight sub-agent step; strays (resumed turn, idle, late
		// frames) fall back to a notice so nothing vanishes silently.
		// The finished frame's final cost banks first — spent is spent
		// even when the frame has no step left to attach to.
		m.recordSubCost(ev)
		if i := m.cur(); i >= 0 && m.attachSubState(i, ev) {
			stream = true // coalesce redraws — state frames arrive in bursts
			m.subagentTerminalNote(ev)
			break
		}
		m.addTransientNote("subagent · " + stateNoticeLine(ev))

	case "subagent_cancelled":
		// Stop ack. accepted:false is a benign race — the task already
		// finished. The card's terminal state comes exclusively from the
		// subagent_state frame; the ack never flips UI state.
		if !ev.Accepted {
			m.addTransientNote("stop declined · sub-agent already finished")
		}

	case client.EventDisconnected:
		m.disconn = true
		m.busy = false
		m.renderPending = false
		m.cancelAck = false // stale: the error it muted died with the socket
		m.skillSuggest = nil
		if m.shutdownReq {
			// The user asked for this drop: no reconnect spiral, just the
			// fresh-start affordance (⏎ respawns in spawn mode).
			m.shutdownReq = false
			m.finalize()
			m.relayout()
			m.status = "server shut down"
			m.addNote("server shut down · ⏎ starts a fresh instance")
			m.refresh()
			return m, m.noticeSweep()
		}
		// A turn in flight when the socket drops will never finish: close it
		// out with an interrupted marker instead of leaving it streaming
		// forever. Idempotent — with no open turn this is a no-op, so a
		// later resume is untouched.
		if i := m.cur(); i >= 0 {
			setTurnMarker(&m.msgs[i], "**Interrupted:** connection lost")
		}
		if n := m.loseLiveAgents(); n > 0 {
			// In-flight cards just became unknowable — say so once instead of
			// leaving spinners that will never settle.
			m.addNote("sub-agent state lost on disconnect")
		}
		m.finalize()
		m.relayout() // the busy status line is gone with the socket
		if cmd := m.scheduleReconnect(0); cmd != nil {
			m.status = "reconnecting…"
			if m.freshStart {
				m.addTransientNote("starting a fresh session…")
			} else {
				m.addTransientNote("connection lost — reconnecting…")
			}
			m.refresh()
			// The interim note fades via the sweep; the reconnect outcome
			// (success or the ⏎-retry hint) replaces it within seconds.
			return m, tea.Batch(cmd, m.noticeSweep())
		}
		m.status = "disconnected"
		m.addNote("disconnected from odek serve")
		if m.opts.LogPath != "" {
			m.addNote("server log · " + m.opts.LogPath)
		}
		m.refresh()
		return m, m.noticeSweep()
	}

	if stream {
		return m, tea.Batch(listen(m.events), m.noticeSweep(), m.approvalSweep(), m.queueRender())
	}
	m.refresh()
	// A turn that just ended (done / error) drains the next queued prompt.
	return m, tea.Batch(listen(m.events), m.noticeSweep(), m.approvalSweep(), m.sendQueued(), m.planFollowup(), attn)
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

// appendReply appends answer text to the turn's open reply segment, or
// starts a new one after a reasoning block or tool call — so every
// think→reply cycle renders independently. msg.content stays in sync for
// export, stats, and hand-built messages without a timeline; distinct
// cycles join it with a blank line so copied/exported prose stays readable.
func appendReply(msg *message, s string) {
	if s == "" {
		return
	}
	if n := len(msg.items); n > 0 && msg.items[n-1].reply {
		msg.items[n-1].text += s
		msg.content += s
		return
	}
	if msg.content != "" {
		msg.content += "\n\n"
	}
	msg.content += s
	msg.items = append(msg.items, turnItem{reply: true, text: s})
}

// setTurnMarker closes out a turn with a bold status line ("**Cancelled.**",
// "**Interrupted:** …", "**Error:** …"): below the existing reply when the
// turn already produced prose, or as its only reply segment otherwise — the
// marker always renders attached to the final card.
func setTurnMarker(msg *message, marker string) {
	if msg.content == "" {
		appendReply(msg, marker)
		return
	}
	msg.content += "\n\n" + marker
	if n := len(msg.items); n > 0 && msg.items[n-1].reply {
		msg.items[n-1].text += "\n\n" + marker
		return
	}
	msg.items = append(msg.items, turnItem{reply: true, text: marker})
}

// isContextCanceled reports whether an error message is the abort a run's
// context returns once a cancel is honored ("context canceled", possibly
// wrapped by the provider client on its way out).
func isContextCanceled(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "context cancel")
}

// markCancel closes out a streaming turn as cancelled — the deliberate
// sibling of the interrupted marker a disconnect leaves behind.
func markCancel(msg *message) {
	setTurnMarker(msg, "**Cancelled.**")
}

// finalize closes out the streaming assistant message and drops the cursor:
// the swarm verdict (when the turn delegated sub-agents) lands as the turn
// marker, and sticky sub-agent failure notices retire — the verdict line
// supersedes them.
func (m *Model) finalize() {
	if i := m.cur(); i >= 0 {
		if v := m.swarmVerdict(&m.msgs[i]); v != "" {
			setTurnMarker(&m.msgs[i], v)
		}
		m.closeTurn(&m.msgs[i])
	}
	m.curIdx = -1
	m.clearStickyNotes()
}

// swarmVerdict summarizes a turn's sub-agent outcomes as a turn marker —
// "**sub-agents: 5 ✓ · 1 ✗ — #4 error**" — so a failure in a multi-agent turn
// can't scroll by uncounted. "" when the turn delegated nothing.
func (m *Model) swarmVerdict(msg *message) string {
	var ok, partial, failed, cancelled, timed, live, lostN int
	var bad []string
	for j := range msg.steps {
		if !msg.steps[j].subagent {
			continue
		}
		for _, a := range msg.steps[j].agents {
			switch {
			case !a.finished():
				if a.lost {
					lostN++ // orphaned by a disconnect: neither live nor terminal
				} else {
					live++
				}
			case a.status == "success":
				ok++
			case a.status == "partial", a.status == "budget_exhausted":
				partial++
			case a.status == "error":
				failed++
				bad = append(bad, fmt.Sprintf("#%d %s", a.idx+1, a.status))
			case a.status == "timeout":
				timed++
				bad = append(bad, fmt.Sprintf("#%d %s", a.idx+1, a.status))
			case a.status == "cancelled":
				cancelled++
				bad = append(bad, fmt.Sprintf("#%d %s", a.idx+1, a.status))
			}
		}
	}
	total := ok + partial + failed + cancelled + timed + live + lostN
	if total == 0 {
		return ""
	}
	parts := make([]string, 0, 6)
	if ok > 0 {
		parts = append(parts, fmt.Sprintf("%d ✓", ok))
	}
	if partial > 0 {
		parts = append(parts, fmt.Sprintf("%d ◐", partial))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d ✗", failed))
	}
	if timed > 0 {
		parts = append(parts, fmt.Sprintf("%d ⏱", timed))
	}
	if cancelled > 0 {
		parts = append(parts, fmt.Sprintf("%d ⊘", cancelled))
	}
	if lostN > 0 {
		parts = append(parts, fmt.Sprintf("%d lost", lostN))
	}
	if live > 0 {
		parts = append(parts, fmt.Sprintf("%d live", live))
	}
	line := "sub-agents: " + strings.Join(parts, " · ")
	if len(bad) > 0 {
		line += " — " + strings.Join(bad, ", ")
	}
	return "**" + line + "**"
}

// closeTurn renders finalized markdown for one assistant turn: each reply
// segment goes through glamour individually (cached on the item, re-rendered
// only on resize), reasoning folds into msg.thinking, and reasoning blocks
// the renderer auto-opened for the live stream collapse here (the WebUI's
// accordion rule): the next turn starts with history folded, and only blocks
// the user opened themselves stay open. Shared by finalize() and the
// session-replay flush.
func (m *Model) closeTurn(msg *message) {
	msg.streaming = false
	var thoughts []string
	for j := range msg.items {
		switch {
		case msg.items[j].thinking:
			thoughts = append(thoughts, msg.items[j].text)
			msg.items[j].open = false
		case msg.items[j].reply:
			msg.items[j].rendered = m.render(msg.items[j].text)
		}
	}
	// Keep the turn's reasoning concatenated on the message for
	// compatibility; the timeline (items) drives the actual rendering.
	msg.thinking = strings.Join(thoughts, "\n")
	msg.rendered = m.render(msg.content)
}

// addNote appends an alert-tier notice (errors, warnings, disconnects) that
// dwells for alertTTL before fading — long enough to read, bounded like
// every notice in the strip.
func (m *Model) addNote(s string) {
	m.pushNote(s, time.Now().Add(alertTTL))
}

// addTransientNote appends an info trace that fades after noticeTTL.
func (m *Model) addTransientNote(s string) {
	m.pushNote(s, time.Now().Add(noticeTTL))
}

// transientNoteCmd adds a transient note and returns the sweep cmd. Every
// caller outside handleEvent must batch this cmd or the note only fades on
// the next unrelated render.
func (m *Model) transientNoteCmd(s string) tea.Cmd {
	m.addTransientNote(s)
	return m.noticeSweep()
}

func (m *Model) pushNote(s string, exp time.Time) {
	// Provider errors routinely embed full 4xx bodies — cap the size so one
	// verbose notice cannot flood the transcript tail (count caps below).
	m.notices = append(m.notices, truncate(sanitize(s), 400))
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

// clearStickyNotes retires zero-expiry notes (sticky sub-agent failures) —
// used when the context that made them sticky is gone (turn finalized).
func (m *Model) clearStickyNotes() {
	kept := m.notices[:0]
	keptExp := m.noticeExp[:0]
	for i, n := range m.notices {
		if !m.noticeExp[i].IsZero() {
			kept = append(kept, n)
			keptExp = append(keptExp, m.noticeExp[i])
		}
	}
	m.notices = kept
	m.noticeExp = keptExp
}

// noticeSweep schedules the next expiry sweep at the earliest pending
// notice expiry; nil when the strip has nothing pending. The tick handler
// prunes and re-arms, so expired notes disappear even on an idle TUI and
// the timer chain stops itself once the strip is clean.
func (m *Model) noticeSweep() tea.Cmd {
	var earliest time.Time
	for _, exp := range m.noticeExp {
		if earliest.IsZero() || exp.Before(earliest) {
			earliest = exp
		}
	}
	if earliest.IsZero() {
		return nil
	}
	d := time.Until(earliest)
	if d < 0 {
		d = 0
	}
	return tea.Tick(d, func(time.Time) tea.Msg {
		return noticeExpireMsg{}
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

// attachSubLog nests a sub-agent activity line under the most recent
// sub-agent step in message i, reporting whether one was found. The log
// mutates in place — newest line first (DESC), capped at maxSubLogs with the
// oldest rolling off — so a long-running agent's card always shows its
// latest activity rather than freezing on its first frames.
func (m *Model) attachSubLog(i int, line string) bool {
	const maxSubLogs = 8
	steps := m.msgs[i].steps
	for j := len(steps) - 1; j >= 0; j-- {
		if !steps[j].subagent {
			continue
		}
		steps[j].logs = append([]string{sanitize(line)}, steps[j].logs...)
		if len(steps[j].logs) > maxSubLogs {
			steps[j].logs = steps[j].logs[:maxSubLogs]
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
