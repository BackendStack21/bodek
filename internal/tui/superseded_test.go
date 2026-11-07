package tui

import (
	"strings"
	"testing"

	"github.com/BackendStack21/bodek/internal/client"
)

// draftTurn streams a draft answer, supersedes it, then streams the
// replacement — the shape odek sends when it re-asks the model.
func draftTurn(m *Model, reason string) {
	busyTurn(m)
	for _, ev := range []client.Event{
		{Type: "tool_call", Name: "shell", Data: `{"command":"git log"}`},
		{Type: "tool_result", Name: "shell", Data: "abc123 fix"},
		{Type: "token", Content: "DRAFT says the branch has 14 commits."},
		{Type: "answer_superseded", Reason: reason, Cycle: 1},
		{Type: "token", Content: "FINAL says the branch has 15 commits."},
	} {
		m.handleEvent(ev)
	}
}

func itemKinds(msg message) (replies, drafts int) {
	for _, it := range msg.items {
		if it.reply {
			replies++
		}
		if it.draft {
			drafts++
		}
	}
	return replies, drafts
}

// A superseded draft folds to one row: the turn shows a single answer, and
// msg.content (export, copy, stats) carries only the replacement.
func TestAnswerSupersededFoldsDraft(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	draftTurn(m, "completion_nudge")
	m.handleEvent(client.Event{Type: "done"})
	m.refresh()

	msg := m.msgs[len(m.msgs)-1]
	if r, d := itemKinds(msg); r != 1 || d != 1 {
		t.Fatalf("items: %d replies, %d drafts; want 1 and 1", r, d)
	}
	if msg.content != "FINAL says the branch has 15 commits." {
		t.Fatalf("content = %q, want only the replacement answer", msg.content)
	}
	view := plain(m.View())
	if !strings.Contains(view, "⋯ draft revised · completion check") {
		t.Errorf("no folded draft row:\n%s", view)
	}
	if strings.Contains(view, "DRAFT says") {
		t.Errorf("draft text painted while folded:\n%s", view)
	}
	if strings.Count(view, "FINAL says") != 1 {
		t.Errorf("replacement answer should paint once:\n%s", view)
	}
	if got := foldTally(msg); got != "" && !strings.HasPrefix(got, "1 tool") {
		t.Errorf("foldTally = %q, a draft must not count as a tool", got)
	}
}

// Prose before the last tool call is a progress note, not the draft — only
// the reply streamed since the last tool call folds.
func TestAnswerSupersededKeepsEarlierNotes(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	for _, ev := range []client.Event{
		{Type: "token", Content: "Checking the log first."},
		{Type: "tool_call", Name: "shell", Data: `{"command":"git log"}`},
		{Type: "tool_result", Name: "shell", Data: "abc123 fix"},
		{Type: "token", Content: "DRAFT"},
		{Type: "answer_superseded", Reason: "verify_retry"},
		{Type: "token", Content: "FINAL"},
	} {
		m.handleEvent(ev)
	}
	msg := m.msgs[m.cur()]
	if msg.content != "Checking the log first.\n\nFINAL" {
		t.Fatalf("content = %q, want the note and the replacement", msg.content)
	}
	if r, d := itemKinds(msg); r != 2 || d != 1 {
		t.Fatalf("items: %d replies, %d drafts; want 2 and 1", r, d)
	}
}

// A draft opens like reasoning: ^E shows it, and inspection lands on it and
// expands it with Enter.
func TestDraftOpensOnDemand(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	draftTurn(m, "verify_retry")
	m.handleEvent(client.Event{Type: "done"})

	m.expandAll = true
	m.invalidateAllMsgBlocks()
	m.refresh()
	if !strings.Contains(plain(m.View()), "DRAFT says") {
		t.Fatalf("^E does not reveal the draft:\n%s", plain(m.View()))
	}
	m.expandAll = false
	m.invalidateAllMsgBlocks()

	var target *inspectTarget
	for _, p := range m.inspectTargets() {
		if p.itemIdx >= 0 && m.msgs[p.msgIdx].items[p.itemIdx].draft {
			p := p
			target = &p
		}
	}
	if target == nil {
		t.Fatal("draft is not an inspect target")
	}
	m.inspect = target
	if !m.validInspect() {
		t.Fatal("inspecting a draft is not valid")
	}
	m.Update(key("enter"))
	if !m.msgs[target.msgIdx].items[target.itemIdx].open {
		t.Fatal("Enter did not open the inspected draft")
	}
	if got := m.focusedCopyText(); !strings.Contains(got, "DRAFT says") {
		t.Fatalf("copying the inspected draft = %q", got)
	}
}

// done.verified == "fail" marks the turn head and its outcome row.
func TestVerifiedFailMarksTurn(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	draftTurn(m, "verify_retry")
	m.handleEvent(client.Event{Type: "done", Verified: "fail"})
	m.refresh()
	msg := m.msgs[len(m.msgs)-1]
	if !msg.unverified {
		t.Fatal("verified=fail did not mark the turn")
	}
	view := plain(m.View())
	if strings.Count(view, "✗ unverified") < 2 {
		t.Fatalf("want the chip on the head and the outcome row:\n%s", view)
	}

	m = newTestModel()
	m.resize(100, 30)
	draftTurn(m, "verify_retry")
	m.handleEvent(client.Event{Type: "done", Verified: "pass"})
	if m.msgs[len(m.msgs)-1].unverified || strings.Contains(plain(m.View()), "unverified") {
		t.Fatal("verified=pass must not mark the turn")
	}
}

// A bulk final answer carries odek's marker as text; the chip replaces it.
func TestBulkVerifyMarkerStripped(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "token", Content: verifyFailedMarker + "\n\nThe answer."})
	msg := m.msgs[m.cur()]
	if msg.content != "The answer." || !msg.unverified {
		t.Fatalf("content = %q unverified=%v; want marker stripped and flagged", msg.content, msg.unverified)
	}
}

// Verification progress rides the status line and clears with the turn.
func TestVerifyingStatusLabel(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "runtime_event", Runtime: &client.RuntimeEvent{Type: "verification_started"}})
	if !strings.Contains(plain(m.statusLine()), "verifying answer") {
		t.Fatalf("status line = %q, want verifying answer", plain(m.statusLine()))
	}
	m.handleEvent(client.Event{Type: "runtime_event", Runtime: &client.RuntimeEvent{Type: "verification_completed", Data: map[string]any{"verdict": "pass"}}})
	if m.verifying {
		t.Fatal("verification_completed left the label on")
	}
	m.handleEvent(client.Event{Type: "runtime_event", Runtime: &client.RuntimeEvent{Type: "verification_started"}})
	m.handleEvent(client.Event{Type: "done"})
	if m.verifying {
		t.Fatal("done left the verifying label on")
	}
	// Other runtime records have no transcript surface.
	m.handleEvent(client.Event{Type: "runtime_event", Runtime: &client.RuntimeEvent{Type: "iteration_completed"}})
	m.handleEvent(client.Event{Type: "runtime_event"})
}

// Replay folds persisted drafts and turns a persisted marker into the chip.
func TestReplayFoldsSupersededDraft(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	m.replayTranscript([]client.SessionMessage{
		{Role: "user", Content: "Describe branch changes?"},
		{Role: "assistant", Content: "DRAFT answer", Superseded: true, SupersededReason: "completion_nudge"},
		{Role: "assistant", Content: verifyFailedMarker + "\n\nFINAL answer"},
	})
	var msg message
	for _, mm := range m.msgs {
		if mm.role == roleAsst {
			msg = mm
		}
	}
	if r, d := itemKinds(msg); r != 1 || d != 1 {
		t.Fatalf("items: %d replies, %d drafts; want 1 and 1", r, d)
	}
	if msg.content != "FINAL answer" || !msg.unverified {
		t.Fatalf("content = %q unverified=%v", msg.content, msg.unverified)
	}
	m.refresh()
	view := plain(m.View())
	if !strings.Contains(view, "draft revised · completion check") || strings.Contains(view, "DRAFT answer") {
		t.Fatalf("replayed draft not folded:\n%s", view)
	}
}

// Linear mode cannot unprint: the draft prints once, a note says it was
// revised, and the replacement prints whole.
func TestPlainModeSupersededDraft(t *testing.T) {
	m := newTestModel()
	m.plain = true
	m.resize(100, 30)
	busyTurn(m)
	var out []string
	feed := func(ev client.Event) {
		m.handleEvent(ev)
		out = append(out, m.plainEventLines(ev)...)
	}
	feed(client.Event{Type: "token", Content: "DRAFT answer"})
	feed(client.Event{Type: "answer_superseded", Reason: "verify_retry"})
	feed(client.Event{Type: "token", Content: "FINAL answer"})
	feed(client.Event{Type: "done", Verified: "fail"})
	log := strings.Join(out, "\n")
	for _, want := range []string{"DRAFT answer", "[draft revised · verification]", "FINAL answer", "✗ unverified"} {
		if !strings.Contains(log, want) {
			t.Errorf("plain log missing %q:\n%s", want, log)
		}
	}
	if strings.Count(log, "DRAFT answer") != 1 || strings.Count(log, "FINAL answer") != 1 {
		t.Errorf("each answer must print exactly once:\n%s", log)
	}
	if strings.Index(log, "DRAFT answer") > strings.Index(log, "[draft revised") {
		t.Errorf("revision note must follow the draft:\n%s", log)
	}
}

// An answer_superseded with no reply since the last tool call (a buffered
// draft never reached the client) folds nothing and leaves content alone.
func TestAnswerSupersededWithoutDraftIsNoop(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "token", Content: "note"})
	m.handleEvent(client.Event{Type: "tool_call", Name: "shell", Data: `{"command":"ls"}`})
	m.handleEvent(client.Event{Type: "answer_superseded", Reason: "verify_retry"})
	msg := m.msgs[m.cur()]
	if _, d := itemKinds(msg); d != 0 || msg.content != "note" {
		t.Fatalf("drafts=%d content=%q; want no fold before the tool call", d, msg.content)
	}
}

// Unknown reasons still read as a revision rather than an empty label.
func TestDraftReasonLabelFallback(t *testing.T) {
	if got := draftReasonLabel("future_reason"); got != "re-asked" {
		t.Fatalf("label = %q, want re-asked", got)
	}
}

// A selected but folded draft teaches Enter, and an opened draft becomes
// the copy span for alt+y.
func TestDraftSelectedHintAndCopySpan(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	draftTurn(m, "completion_nudge")
	m.handleEvent(client.Event{Type: "done"})
	i := len(m.msgs) - 1
	j := -1
	for k, it := range m.msgs[i].items {
		if it.draft {
			j = k
		}
	}
	m.inspect = &inspectTarget{msgIdx: i, stepIdx: -1, itemIdx: j}
	if got := plain(m.renderDraft(m.msgs[i].items[j], j, i)); !strings.Contains(got, "› ⋯ draft revised") || !strings.Contains(got, "Enter expand") {
		t.Fatalf("selected folded draft = %q", got)
	}
	m.inspect = nil
	m.msgs[i].items[j].open = true
	m.focusIdx = i
	span := m.currentCopySpan()
	if !span.set || span.kind != copyThink || span.idx != j {
		t.Fatalf("copy span = %+v, want the open draft", span)
	}
}

// Replay shows the prompt as typed: odek stores attachments and
// @-resources inlined (wrapped in untrusted markers) in content and the
// typed text in principal_prompt.
func TestReplayPrincipalPrompt(t *testing.T) {
	typed, empty := "summarize notes.txt", ""
	expanded := "<untrusted_content_ab12 source=\"attachment:notes.txt\">\nATTACHMENT-BODY\n</untrusted_content_ab12>\n\nsummarize notes.txt"
	cases := []struct {
		name string
		mm   client.SessionMessage
		want string
	}{
		{"typed", client.SessionMessage{Role: "user", Content: expanded, PrincipalPrompt: &typed}, typed},
		{"attachment only", client.SessionMessage{Role: "user", Content: expanded, PrincipalPrompt: &empty}, "(attached files)"},
		{"legacy record", client.SessionMessage{Role: "user", Content: "plain"}, "plain"},
	}
	for _, tc := range cases {
		if got := replayPrompt(tc.mm); got != tc.want {
			t.Errorf("%s: replayPrompt = %q, want %q", tc.name, got, tc.want)
		}
	}
	m := newTestModel()
	m.resize(100, 30)
	m.replayTranscript([]client.SessionMessage{cases[0].mm, {Role: "assistant", Content: "done"}})
	m.refresh()
	view := plain(m.View())
	if !strings.Contains(view, typed) || strings.Contains(view, "ATTACHMENT-BODY") || strings.Contains(view, "untrusted_content") {
		t.Fatalf("replayed prompt leaks the expanded content:\n%s", view)
	}
}

func verifyEv(typ, verdict, skipped string) client.Event {
	rt := &client.RuntimeEvent{Type: typ}
	if verdict != "" {
		rt.Data = map[string]any{"verdict": verdict}
		if skipped != "" {
			rt.Data["skipped_reason"] = skipped
		}
	}
	return client.Event{Type: "runtime_event", Runtime: rt}
}

// The candidate answer never paints before it is final, so a re-ask never
// shows an answer, folds it, and streams another (the blink). Every frame
// of the wire sequence is checked against the real view.
func TestSupersededDraftNeverPaints(t *testing.T) {
	cases := []struct {
		name, reason, row string
		evs               []client.Event
	}{
		{"verify retry", "verify_retry", "⋯ draft revised · verification", []client.Event{
			{Type: "thinking_delta", Content: "plan"},
			{Type: "token_delta", Content: "DRAFT says "},
			{Type: "token_delta", Content: "14 commits."},
			verifyEv("verification_started", "", ""),
			verifyEv("verification_completed", "fail", ""),
			{Type: "answer_superseded", Reason: "verify_retry", Cycle: 1},
			{Type: "thinking_delta", Content: "recount"},
			{Type: "token_delta", Content: "FINAL says "},
			{Type: "token_delta", Content: "15 commits."},
			verifyEv("verification_started", "", ""),
		}},
		{"completion check", "completion_nudge", "⋯ draft revised · completion check", []client.Event{
			{Type: "tool_call", Name: "shell", Data: `{"command":"git log"}`},
			{Type: "tool_result", Name: "shell", Data: "abc123 fix"},
			{Type: "token_delta", Content: "DRAFT says 14 commits."},
			{Type: "answer_superseded", Reason: "completion_nudge", Cycle: 1},
			{Type: "token_delta", Content: "FINAL says 15 commits."},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.resize(100, 30)
			busyTurn(m)
			for _, ev := range tc.evs {
				m.handleEvent(ev)
				m.refreshStreamingReplies()
				m.refresh()
				view := plain(m.View())
				if strings.Contains(view, "DRAFT") || strings.Contains(view, "FINAL") {
					t.Fatalf("candidate answer painted after %s:\n%s", ev.Type, view)
				}
			}
			if view := plain(m.View()); !strings.Contains(view, tc.row) {
				t.Fatalf("no folded draft row:\n%s", view)
			}
			if tc.reason == "verify_retry" {
				// The passing verdict commits the replacement before done.
				m.handleEvent(verifyEv("verification_completed", "pass", ""))
				m.refresh()
				if view := plain(m.View()); strings.Count(view, "FINAL says 15 commits.") != 1 {
					t.Fatalf("verified answer should paint once before done:\n%s", view)
				}
			}
			m.handleEvent(client.Event{Type: "done"})
			m.refresh()
			view := plain(m.View())
			if strings.Contains(view, "DRAFT") || strings.Count(view, "FINAL says 15 commits.") != 1 {
				t.Fatalf("sealed turn must show only the replacement, once:\n%s", view)
			}
		})
	}
}

// Every way a turn ends paints the held answer — a path that left it
// streaming would hide the reply forever. A failed verdict (or exhausted
// cycles) keeps holding until the turn seals, so the reply lands together
// with its ✗ unverified chip.
func TestHeldAnswerPaintsOnEveryTurnEnd(t *testing.T) {
	ends := []struct {
		name       string
		end        func(m *Model)
		unverified bool
	}{
		{"done", func(m *Model) { m.handleEvent(client.Event{Type: "done"}) }, false},
		{"error", func(m *Model) { m.handleEvent(client.Event{Type: "error", Message: "boom"}) }, false},
		{"cancel", func(m *Model) {
			m.handleEvent(client.Event{Type: "cancelled"})
			m.handleEvent(client.Event{Type: "error", Message: "context canceled"})
		}, false},
		{"disconnect", func(m *Model) { m.handleEvent(client.Event{Type: client.EventDisconnected}) }, false},
		{"send failure", func(m *Model) { m.Update(errMsg{err: errTest{}}) }, false},
		{"strict fail", func(m *Model) {
			m.handleEvent(verifyEv("verification_completed", "fail", ""))
			assertHeld(t, m, "strict fail")
			m.handleEvent(client.Event{Type: "done", Verified: "fail"})
		}, true},
		{"cycles exhausted", func(m *Model) {
			m.handleEvent(verifyEv("verification_completed", "skipped", "exhausted"))
			assertHeld(t, m, "cycles exhausted")
			m.handleEvent(client.Event{Type: "done", Verified: "fail"})
		}, true},
	}
	for _, tc := range ends {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.resize(100, 30)
			busyTurn(m)
			m.handleEvent(client.Event{Type: "token_delta", Content: "ANSWER text."})
			assertHeld(t, m, "stream")
			tc.end(m)
			m.refresh()
			view := plain(m.View())
			if !strings.Contains(view, "ANSWER text.") {
				t.Fatalf("turn end left the answer hidden:\n%s", view)
			}
			if got := strings.Contains(view, "✗ unverified"); got != tc.unverified {
				t.Fatalf("unverified chip = %v, want %v:\n%s", got, tc.unverified, view)
			}
		})
	}
}

// Only a pass or uncertain verdict ships the answer before done; replies
// streamed after that commit are held again.
func TestShippingVerdictsCommitAnswer(t *testing.T) {
	for _, v := range []string{"pass", "uncertain"} {
		m := newTestModel()
		m.resize(100, 30)
		busyTurn(m)
		m.handleEvent(client.Event{Type: "token_delta", Content: "ANSWER text."})
		m.handleEvent(verifyEv("verification_completed", v, ""))
		m.refresh()
		if !strings.Contains(plain(m.View()), "ANSWER text.") {
			t.Fatalf("%s verdict did not commit the answer:\n%s", v, plain(m.View()))
		}
		m.handleEvent(client.Event{Type: "thinking_delta", Content: "x"})
		m.handleEvent(client.Event{Type: "token_delta", Content: "AFTER"})
		m.refresh()
		if strings.Contains(plain(m.View()), "AFTER") {
			t.Fatalf("%s: reply after the commit painted early:\n%s", v, plain(m.View()))
		}
	}
	// No open turn: a stray verdict opens nothing.
	m := newTestModel()
	m.handleEvent(verifyEv("verification_completed", "pass", ""))
	if len(m.msgs) != 0 || m.cur() >= 0 {
		t.Fatalf("stray verdict touched the transcript: %+v", m.msgs)
	}
}

// Every other verdict fails safe: skips (which odek emits again as
// exhausted after a fail), unknown verdicts, and frames without data hold
// the answer until the turn seals.
func TestNonShippingVerdictsHold(t *testing.T) {
	frames := []client.Event{
		verifyEv("verification_completed", "skipped", "budget"),
		verifyEv("verification_completed", "skipped", "error"),
		verifyEv("verification_completed", "fail_soft", ""),
		verifyEv("verification_completed", "", ""),
	}
	for _, ev := range frames {
		m := newTestModel()
		m.resize(100, 30)
		busyTurn(m)
		m.handleEvent(client.Event{Type: "token_delta", Content: "ANSWER text."})
		m.handleEvent(ev)
		assertHeld(t, m, "non-shipping verdict")
	}
}

// odek's corrective cycle that runs out of budget: fail → retry → skipped
// (budget) → skipped (exhausted) → done{verified: fail}. The replacement
// stays held until done, so it lands with its ✗ unverified chip.
func TestRetrySkippedAfterFailLandsWithChip(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	for _, ev := range []client.Event{
		{Type: "token_delta", Content: "DRAFT says 14."},
		verifyEv("verification_started", "", ""),
		verifyEv("verification_completed", "fail", ""),
		{Type: "answer_superseded", Reason: "verify_retry", Cycle: 1},
		{Type: "token_delta", Content: "ANSWER says 15."},
		verifyEv("verification_completed", "skipped", "budget"),
		verifyEv("verification_completed", "skipped", "exhausted"),
	} {
		m.handleEvent(ev)
		assertHeld(t, m, ev.Type)
		if strings.Contains(plain(m.View()), "DRAFT says") {
			t.Fatalf("draft painted after %s", ev.Type)
		}
	}
	m.handleEvent(client.Event{Type: "done", Verified: "fail"})
	m.refresh()
	view := plain(m.View())
	if !strings.Contains(view, "ANSWER says 15.") || !strings.Contains(view, "✗ unverified") {
		t.Fatalf("answer should land with its chip:\n%s", view)
	}
}

// An engine that never supersedes (odek < v2.33) streams the answer live:
// holding it would only delay it. Unknown and current engines hold.
func TestHoldFollowsEngineVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		live    bool
	}{{"v2.32.9", true}, {"v2.33.0", false}, {"", false}, {"dev", false}} {
		m := newTestModel()
		m.resize(100, 30)
		m.odekVersion = tc.version
		m.handleEvent(client.Event{Type: "turn_started", Initiated: "operator"})
		m.handleEvent(client.Event{Type: "token_delta", Content: "ANSWER text."})
		m.refresh()
		if got := strings.Contains(plain(m.View()), "ANSWER text."); got != tc.live {
			t.Fatalf("odek %q: painted live = %v, want %v", tc.version, got, tc.live)
		}
	}
}

// Held text is not on screen: copy and find must not reach it, and an
// empty bulk frame must not release it.
func TestHeldAnswerStaysOutOfCopyAndFind(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	m.msgs = append(m.msgs, message{role: roleAsst, content: "previous answer"})
	streamingTurn(m)
	m.handleEvent(client.Event{Type: "token_delta", Content: "ANSWER needle."})
	m.handleEvent(client.Event{Type: "token", Content: verifyFailedMarker})
	assertHeld(t, m, "empty bulk frame")
	live := m.cur()
	m.focusIdx = live
	if got := m.focusedReply(); got != "" {
		t.Fatalf("focused live turn copied %q, want nothing", got)
	}
	if got := m.focusedCopyText(); got != "" {
		t.Fatalf("focused copy text = %q, want nothing", got)
	}
	if findMsgMatch(m.msgs[live], "needle") {
		t.Fatal("find matched a held answer")
	}
	m.handleEvent(client.Event{Type: "done"})
	if !findMsgMatch(m.msgs[live], "needle") || m.focusedReply() != "ANSWER needle." {
		t.Fatalf("sealed answer should be findable and copyable: %q", m.focusedReply())
	}
}

func assertHeld(t *testing.T, m *Model, when string) {
	t.Helper()
	m.refresh()
	if view := plain(m.View()); strings.Contains(view, "ANSWER") {
		t.Fatalf("answer painted early (%s):\n%s", when, view)
	}
}

// A streamed note is held until its step lands; a blank segment must not
// break the residual check and repaint the held answer as a stray card.
func TestStreamedNotePaintsWhenItsStepLands(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "token_delta", Content: "\n\n"})
	m.handleEvent(client.Event{Type: "token_delta", Content: "NOTE before the read."})
	assertNotPainted(t, m, "NOTE", "streamed note before its step")
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"a.go"}`})
	m.refresh()
	if view := plain(m.View()); !strings.Contains(view, "NOTE before the read.") {
		t.Fatalf("note did not paint once its step landed:\n%s", view)
	}
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "ok"})
	m.handleEvent(client.Event{Type: "token_delta", Content: "\n\n"})
	m.handleEvent(client.Event{Type: "tool_call", Name: "read_file", Data: `{"path":"b.go"}`})
	m.handleEvent(client.Event{Type: "tool_result", Name: "read_file", Data: "ok"})
	m.handleEvent(client.Event{Type: "token_delta", Content: "ANSWER text."})
	assertNotPainted(t, m, "ANSWER", "after a blank segment")
	m.handleEvent(client.Event{Type: "done"})
	m.refresh()
	if view := plain(m.View()); strings.Count(view, "ANSWER text.") != 1 || strings.Count(view, "NOTE before the read.") != 1 {
		t.Fatalf("sealed turn should paint the note and the answer once each:\n%s", view)
	}
}

// Bulk text is final on arrival — odek supersedes only streamed drafts and
// sends a buffered answer after verification — so it paints before done.
func TestBulkAnswerPaintsBeforeDone(t *testing.T) {
	m := newTestModel()
	m.resize(100, 30)
	busyTurn(m)
	m.handleEvent(verifyEv("verification_started", "", ""))
	m.handleEvent(verifyEv("verification_completed", "pass", ""))
	m.handleEvent(client.Event{Type: "token", Content: "ANSWER text."})
	m.refresh()
	if view := plain(m.View()); !strings.Contains(view, "ANSWER text.") {
		t.Fatalf("buffered final answer waited for done:\n%s", view)
	}
}

func assertNotPainted(t *testing.T, m *Model, text, when string) {
	t.Helper()
	m.refreshStreamingReplies()
	m.refresh()
	if view := plain(m.View()); strings.Contains(view, text) {
		t.Fatalf("%s painted early (%s):\n%s", text, when, view)
	}
}
