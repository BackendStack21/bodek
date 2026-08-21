package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// streamingDeltas mirrors streamingTurn from stream_test.go for the v2
// fragment events.
func TestDeltaEventsAccumulate(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)

	m.handleEvent(client.Event{Type: "thinking_delta", Content: "let me "})
	m.handleEvent(client.Event{Type: "thinking_delta", Content: "look"})
	m.handleEvent(client.Event{Type: "token_delta", Content: "Hel"})
	m.handleEvent(client.Event{Type: "token_delta", Content: "lo"})
	m.handleEvent(client.Event{Type: "done", Latency: 1})

	i := m.cur()
	if i >= 0 {
		t.Fatal("turn still open after done")
	}
	last := m.msgs[len(m.msgs)-1]
	if last.content != "Hello" {
		t.Errorf("streamed content = %q", last.content)
	}
	// The reasoning timeline captured the thinking_delta fragments.
	found := false
	for _, it := range last.items {
		if it.thinking && it.text == "let me look" {
			found = true
		}
	}
	if !found {
		t.Errorf("thinking items = %+v", last.items)
	}
	if !last.stats.thought {
		t.Error("done did not record the thinking flag")
	}
}

func TestDoneCacheMetrics(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)
	m.handleEvent(client.Event{Type: "done", Latency: 2,
		CacheCreationTokens: 800, CacheReadTokens: 40, CachedTokens: 10})

	ts := m.turnStats[len(m.turnStats)-1]
	if ts.cacheWrite != 800 || ts.cacheRead != 40 || ts.cachedTok != 10 {
		t.Errorf("cache stats = %+v", ts)
	}
	// The stat line renders the cache segment only when reported.
	streamingTurn(m)
	m.handleEvent(client.Event{Type: "done", Latency: 1})
	if got := plain(m.statLine(m.turnStats[len(m.turnStats)-1])); strings.Contains(got, "⛁") {
		t.Errorf("cache segment rendered for a zero-cache turn: %q", got)
	}
	if got := plain(m.statLine(ts)); !strings.Contains(got, "⛁") {
		t.Errorf("cache segment missing: %q", got)
	}
}

func TestServerInfoAndPong(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "server_info", Version: "1.24.0", Model: "glm-5.3",
		Sandbox: true, Stream: true, UptimeSeconds: 90, WSConnections: 2})
	if m.odekVersion != "1.24.0" || !m.serverStream || !m.sandbox {
		t.Fatalf("server_info not applied: v=%q stream=%v sandbox=%v", m.odekVersion, m.serverStream, m.sandbox)
	}
	if m.srvConns != 2 || m.srvUptime != 90*time.Second {
		t.Errorf("snapshot = conns %d uptime %v", m.srvConns, m.srvUptime)
	}
	if m.model != "glm-5.3" {
		t.Errorf("model not adopted from hello: %q", m.model)
	}

	m.pingSentAt = time.Now().Add(-50 * time.Millisecond)
	// The pong snapshot refreshes every field — stream must be re-stated.
	m.handleEvent(client.Event{Type: "pong", T: time.Now().UnixMilli(), Stream: true,
		WSConnections: 1, UptimeSeconds: 91})
	if m.rtt <= 0 {
		t.Error("pong did not measure RTT")
	}
	if !m.pingSentAt.IsZero() {
		t.Error("ping clock not reset")
	}
	// The ⚡ badge marks a streaming server in the header.
	if !strings.Contains(plain(m.header()), "⚡") {
		t.Error("header missing stream badge")
	}
}

func TestCancelledTurnClosesCleanly(t *testing.T) {
	m := newTestModel()
	streamingTurn(m)

	m.handleEvent(client.Event{Type: "cancelled", SessionID: "s1"})
	if !m.cancelAck {
		t.Fatal("cancelled did not arm the ack")
	}
	m.handleEvent(client.Event{Type: "error", Message: "context canceled"})

	if m.busy {
		t.Error("still busy after cancelled turn")
	}
	if m.status != "ready" {
		t.Errorf("status = %q", m.status)
	}
	last := m.msgs[len(m.msgs)-1]
	if !strings.Contains(last.content, "Cancelled") {
		t.Errorf("content = %q", last.content)
	}
	for _, n := range m.notices {
		if strings.Contains(n, "error") {
			t.Errorf("cancel surfaced as an error notice: %q", n)
		}
	}
	if m.cancelAck {
		t.Error("ack flag not consumed")
	}

	// The next genuine error is a real error again.
	streamingTurn(m)
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if m.status != "error" {
		t.Errorf("status after real error = %q", m.status)
	}
	if !strings.Contains(m.msgs[len(m.msgs)-1].content, "boom") {
		t.Error("real error not rendered")
	}
}

func TestContextCanceledWithoutCancelledEvent(t *testing.T) {
	// A cancel issued from another client (REST) aborts the run without a
	// cancelled event on this socket — the context-canceled error must still
	// read as a cancel, not a failure.
	m := newTestModel()
	streamingTurn(m)
	m.handleEvent(client.Event{Type: "error", Message: "post: context canceled"})
	if m.status == "error" {
		t.Error("context-canceled abort rendered as error")
	}
	if !strings.Contains(m.msgs[len(m.msgs)-1].content, "Cancelled") {
		t.Errorf("content = %q", m.msgs[len(m.msgs)-1].content)
	}
}

func TestApprovalFriction(t *testing.T) {
	m := wired(t)
	m.busy = true
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-9", Risk: "shell_exec",
		Command: "rm x", AllowTrust: true, Friction: true, FrictionApprovals: 3})

	// Friction hides trust even though the server allows it.
	for _, o := range m.approvalOptions() {
		if o.action == "trust" {
			t.Fatal("trust offered in friction mode")
		}
	}
	out := plain(m.View())
	if !strings.Contains(out, "friction") || !strings.Contains(out, "3 approvals") {
		t.Errorf("friction line missing:\n%s", out)
	}
	if strings.Contains(out, "› approve") {
		t.Error("selection shortcut rendered in friction mode")
	}

	// Enter on a partial/absent word does not approve.
	m.Update(key("enter"))
	if m.curApproval() == nil {
		t.Fatal("partial word approved")
	}
	// Typing the literal word + enter approves.
	for _, r := range "approve" {
		m.Update(key(string(r)))
	}
	_, cmd := m.Update(key("enter"))
	if m.curApproval() != nil {
		t.Fatal("typed confirmation did not approve")
	}
	exec(cmd)

	// A wrong word resets rather than approving.
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-10", Friction: true})
	for _, r := range "appruve" {
		m.Update(key(string(r)))
	}
	m.Update(key("enter"))
	if m.curApproval() == nil {
		t.Fatal("wrong word approved")
	}
	if m.apprTyped != "" {
		t.Errorf("buffer not reset: %q", m.apprTyped)
	}
	// Esc still denies in one keypress.
	m.Update(key("esc"))
	if m.curApproval() != nil {
		t.Fatal("esc did not deny")
	}
}

func TestSessionsPanelSearchAndPin(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSessions()))
	if len(m.sessions) != 1 {
		t.Fatalf("sessions = %d", len(m.sessions))
	}

	// `/` enters search mode; typed runes edit the draft; enter applies it
	// (the fetch lands with the query server-side).
	m.Update(key("/"))
	if m.panelEdit != panelEditSearch {
		t.Fatal("search mode not entered")
	}
	for _, r := range "first" {
		m.Update(key(string(r)))
	}
	if m.panelDraft != "first" {
		t.Fatalf("draft = %q", m.panelDraft)
	}
	m.Update(key("backspace"))
	m.Update(key("enter"))
	if m.panelEdit != panelEditNone {
		t.Error("enter did not leave search mode")
	}
	if m.sessQuery != "firs" {
		t.Errorf("query = %q", m.sessQuery)
	}
	// Esc abandons an edit without applying the draft.
	m.Update(key("/"))
	m.Update(key("z"))
	m.Update(key("esc"))
	if m.sessQuery != "firs" {
		t.Errorf("esc applied the draft: %q", m.sessQuery)
	}

	// Pin toggles the row in place (POST + local update).
	m.panelSel = 0
	_, cmd := m.Update(key("p"))
	m.Update(exec(cmd)) // sessionUpdatedMsg
	if !m.sessions[0].Pinned {
		t.Errorf("pin not applied: %+v", m.sessions[0])
	}
	if !strings.Contains(plain(m.View()), "📌") {
		t.Error("pin marker missing from rows")
	}

	// Rename prefills the current label; typing appends to it.
	m.Update(key("r"))
	if m.panelEdit != panelEditRename {
		t.Fatal("rename mode not entered")
	}
	m.Update(key(" "))
	for _, r := range "v2" {
		m.Update(key(string(r)))
	}
	_, rcmd := m.Update(key("enter"))
	m.Update(exec(rcmd))
	if m.sessions[0].Task != "first task v2" {
		t.Errorf("rename not applied: %q", m.sessions[0].Task)
	}
}

func TestSessionsPanelPagination(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSessions()))
	// A full page means more may follow and `n` fetches the next offset.
	m.handleSessionsPageMsg(sessionsPageMsg{page: client.SessionsPage{
		Sessions: []client.Session{{ID: "s1"}}, Limit: 1, Count: 1}})
	if !m.sessHasMore {
		t.Fatal("full page not marked has-more")
	}
	m.handleSessionsPageMsg(sessionsPageMsg{page: client.SessionsPage{
		Sessions: []client.Session{{ID: "s2"}}, Limit: 1, Count: 1}, append: true})
	if len(m.sessions) != 2 {
		t.Fatalf("append pagination = %d", len(m.sessions))
	}
	m.handleSessionsPageMsg(sessionsPageMsg{page: client.SessionsPage{
		Sessions: []client.Session{{ID: "s3"}}, Limit: 1, Count: 0}, append: true})
	if m.sessHasMore {
		t.Error("short page still marked has-more")
	}
}

func TestModelEntriesMergeProfiles(t *testing.T) {
	m := newTestModel()
	m.models = []client.ModelInfo{{ID: "glm-5.3", MaxContext: 1000000, Current: true}}
	m.profiles = []client.Profile{
		{ID: "glm", Label: "GLM", MaxContext: 200000},
		{ID: "glm-5.3", Label: "GLM 5.3 (dup)", MaxContext: 999}, // exact dup — dropped
		{ID: "dsv", Label: "DeepSeek", MaxContext: 128000},
	}
	entries := m.modelEntries()
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].id != "glm-5.3" || !entries[0].current || entries[0].detail == "" {
		t.Errorf("configured model not first with context: %+v", entries[0])
	}
	if entries[1].id != "glm" || entries[1].current {
		t.Errorf("profile entry wrong: %+v", entries[1])
	}
	if entries[2].id != "dsv" || entries[2].detail == "" {
		t.Errorf("profile entry missing context: %+v", entries[2])
	}

	// The gauge falls back to a profile's window for a switched model.
	m.model = "dsv4-flash"
	m.resolveMaxContext()
	if m.maxContext != 128000 {
		t.Errorf("profile context not resolved: %d", m.maxContext)
	}
}

func TestAttachStagesAndClears(t *testing.T) {
	m := newTestModel()
	dir := t.TempDir()
	file := dir + "/notes.txt"
	if err := os.WriteFile(file, []byte("hello notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.runCommand("attach", file)
	if len(m.attachments) != 1 || m.attachments[0].Name != "notes.txt" {
		t.Fatalf("attachments = %+v", m.attachments)
	}
	// Restaging replaces, /unattach drops.
	m.runCommand("attach", file)
	if len(m.attachments) != 1 {
		t.Fatalf("restage duplicated: %+v", m.attachments)
	}
	m.runCommand("unattach", "notes.txt")
	if len(m.attachments) != 0 {
		t.Fatalf("unattach left files: %+v", m.attachments)
	}
	// Binary content is refused.
	bin := dir + "/blob.bin"
	if err := os.WriteFile(bin, []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.runCommand("attach", bin)
	if len(m.attachments) != 0 {
		t.Error("binary file staged")
	}
}
