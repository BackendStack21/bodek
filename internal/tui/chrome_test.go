package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/BackendStack21/bodek/internal/client"
)

func viewRows(m *Model) int {
	return strings.Count(m.View(), "\n") + 1
}

func TestDrawerSheetKeepsTranscript(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/queue")
	if m.panel != panelQueue {
		t.Fatalf("panel = %v, want panelQueue", m.panel)
	}
	if m.drawerFullBleed() {
		t.Fatal("100×30 must split the drawer, not full-bleed")
	}
	if got := m.drawerSheetHeight(); got < sheetMin || got > sheetMax {
		t.Fatalf("sheet height = %d, want %d–%d", got, sheetMin, sheetMax)
	}
	if viewRows(m) != m.height {
		t.Errorf("view = %d rows, terminal = %d", viewRows(m), m.height)
	}
	out := plain(m.View())
	if !strings.Contains(out, "type a task") && !strings.Contains(out, "bodek") {
		t.Errorf("transcript home missing above the sheet:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "queue") {
		t.Errorf("drawer sheet missing queue chrome:\n%s", out)
	}
}

func TestDrawerFullBleedOnShortTerminal(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/queue")
	m.resize(80, 14)
	if !m.drawerFullBleed() {
		t.Fatalf("14-row terminal should full-bleed (avail=%d)", m.drawerAvail())
	}
	if viewRows(m) != m.height {
		t.Errorf("full-bleed view = %d rows, terminal = %d", viewRows(m), m.height)
	}
	out := plain(m.View())
	if !strings.Contains(strings.ToLower(out), "queue") {
		t.Errorf("full-bleed body missing the panel:\n%s", out)
	}
}

func TestComposerShelfChips(t *testing.T) {
	m := newTestModel()
	m.attachments = []client.Attachment{{Name: "main.go"}}
	m.queue = []string{"held prompt"}
	m.skillSuggest = &client.Event{SkillName: "deploy-helper"}
	m.relayout()
	m.refresh()
	out := plain(m.View())
	for _, want := range []string{"main.go", "queued", "✦", "deploy-helper", "alt+s", "alt+x"} {
		if !strings.Contains(out, want) {
			t.Errorf("shelf missing %q:\n%s", want, out)
		}
	}
	if m.queueStripVisible() {
		t.Error("unfocused queue must stay on the shelf")
	}
	if m.shelfHeight() != 1 {
		t.Errorf("shelfHeight = %d, want 1", m.shelfHeight())
	}
}

func TestApprovalKeepsComposer(t *testing.T) {
	m := newTestModel()
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr",
		Risk: "shell_exec", Command: "rm x", Description: "delete", AllowTrust: true})
	out := plain(m.View())
	if !strings.Contains(out, "approval required") {
		t.Fatalf("approval card missing:\n%s", out)
	}
	if m.inputAreaHeight() <= lineCount(m.approvalPanel()) {
		t.Fatalf("composer vanished under the approval card: input=%d panel=%d",
			m.inputAreaHeight(), lineCount(m.approvalPanel()))
	}
	if viewRows(m) != m.height {
		t.Errorf("approval view = %d rows, terminal = %d", viewRows(m), m.height)
	}
	foot := plain(m.footer())
	if !strings.Contains(foot, "approval") {
		t.Errorf("mode pill missing from approval footer: %q", foot)
	}
}

func TestHeaderInstruments(t *testing.T) {
	m := newTestModel()
	m.planInit = true
	m.planAvail = planAvailable
	m.plan = client.PlanSnapshot{Found: true, Steps: []client.PlanStep{
		{ID: "a", Status: client.PlanDone},
		{ID: "b", Status: client.PlanPending},
	}}
	m.jobs = []client.Job{{Status: "running"}, {Status: "running"}}
	if got := m.headerPlanLabel(); got != "plan 1/2" {
		t.Errorf("plan label = %q, want plan 1/2", got)
	}
	if got := m.headerJobsLabel(); got != "● 2 jobs" {
		t.Errorf("jobs label = %q, want ● 2 jobs", got)
	}
	head := plain(m.header())
	if !strings.Contains(head, "plan 1/2") || !strings.Contains(head, "2 jobs") {
		t.Errorf("header missing instruments:\n%s", head)
	}

	m.jobs = []client.Job{{Status: "failed"}}
	if got := m.headerJobsLabel(); got != "✗ job" {
		t.Errorf("failed jobs label = %q, want ✗ job", got)
	}
}

func TestSessionHomeAfterClear(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.msgs = []message{
		{role: roleUser, content: "fix the flaky test"},
		{role: roleAsst, content: "done", steps: []step{
			{name: "write_file", arg: "internal/tui/chrome.go", done: true, result: "ok"},
		}},
	}
	m.clearConversation()
	out := plain(m.View())
	if strings.Contains(out, "fix the flaky test") == false {
		t.Errorf("session home missing last prompt:\n%s", out)
	}
	if !strings.Contains(out, "touched") {
		t.Errorf("session home missing receipt:\n%s", out)
	}
	if !strings.Contains(out, "session") {
		t.Errorf("session home missing the session mark:\n%s", out)
	}

	_ = m.startFreshSession()
	out = plain(m.View())
	if strings.Contains(out, "fix the flaky test") {
		t.Errorf("/new must drop the session home:\n%s", out)
	}
	if !strings.Contains(out, "type a task") {
		t.Errorf("fresh session should show first-run home:\n%s", out)
	}
}

func TestFooterModePill(t *testing.T) {
	m := newTestModel()
	if got := plain(m.footer()); !strings.Contains(got, "composer") {
		t.Errorf("idle footer missing composer pill: %q", got)
	}
	m.panel = panelJobs
	m.relayout()
	if got := m.modeName(); got != "jobs" {
		t.Errorf("modeName = %q, want jobs", got)
	}
	if got := plain(m.footer()); !strings.Contains(got, "jobs") {
		t.Errorf("jobs footer missing mode pill: %q", got)
	}
}

func TestCardsShareWidth(t *testing.T) {
	m := newTestModel()
	want := lipgloss.Width(m.th.inputBox.Width(m.cardWidth()).Render("x"))
	if want < 80 {
		t.Fatalf("precondition: composer width %d, want a wide terminal", want)
	}

	m.showHelp()
	helpW := lipgloss.Width(m.msgs[len(m.msgs)-1].content)
	if helpW != want {
		t.Errorf("help card width %d, want composer %d", helpW, want)
	}

	ans, _ := m.answerCardBody("hello")
	if got := lipgloss.Width(ans); got != want {
		t.Errorf("answer card width %d, want composer %d", got, want)
	}

	note, _ := m.renderMessage(message{role: roleNote, content: "note"}, 0, 0)
	if got := lipgloss.Width(note); got != want {
		t.Errorf("note width %d, want composer %d", got, want)
	}

	m.panel = panelStats
	sheet := strings.Split(m.renderPanel(m.width, 10), "\n")[0]
	if got := lipgloss.Width(sheet); got != want {
		t.Errorf("stats sheet width %d, want composer %d", got, want)
	}

	cockpit := strings.Split(m.popoverView(m.width, 12), "\n")[0]
	if got := lipgloss.Width(cockpit); got != want {
		t.Errorf("cockpit width %d, want composer %d", got, want)
	}

	if got := lipgloss.Width(strings.Split(m.acPopup(), "\n")[0]); got != want {
		t.Errorf("@ popup width %d, want composer %d", got, want)
	}
	if got := lipgloss.Width(strings.Split(m.palPopup(), "\n")[0]); got != want {
		t.Errorf("palette width %d, want composer %d", got, want)
	}

	m.approvals = []client.Event{{Type: "approval_request", Risk: "low", Command: "ls"}}
	if got := lipgloss.Width(strings.Split(m.approvalPanel(), "\n")[0]); got != want {
		t.Errorf("approval width %d, want composer %d", got, want)
	}
}
