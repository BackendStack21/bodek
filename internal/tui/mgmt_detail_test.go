package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// TestMgmtSkillDetailOnEnter verifies Enter expands the highlighted skill
// into a readable detail view (full description, provenance, usage) and esc
// folds it back with the selection intact — promote needs something to read.
func TestMgmtSkillDetailOnEnter(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSkills()))
	m.panelSel = 0 // deploy-helper (carries a description)
	m.Update(key("enter"))
	if !m.panelDetail {
		t.Fatal("enter did not open the detail view")
	}
	out := plain(m.View())
	for _, want := range []string{"deploy-helper", "deploys", "regions", "~/.odek/skills"} {
		if !strings.Contains(out, want) {
			t.Errorf("skill detail missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "esc back") {
		t.Errorf("detail footer missing the way back:\n%s", out)
	}

	// Esc closes the detail but keeps the panel open and the row selected.
	m.Update(key("esc"))
	if m.panelDetail || m.panel != panelSkills || m.panelSel != 0 {
		t.Fatalf("esc: detail=%v panel=%d sel=%d", m.panelDetail, m.panel, m.panelSel)
	}
	// Back on the list the long description is one truncated dim line:
	// the tail is detail-only.
	list := plain(m.View())
	if !strings.Contains(list, "deploys") {
		t.Errorf("list view lost the description line:\n%s", list)
	}
	if strings.Contains(list, "regions") {
		t.Errorf("list view shows the untruncated description:\n%s", list)
	}
}

// TestMgmtSkillDetailPromoteInPlace verifies p promotes straight from the
// detail view, and the post-action refetch folds the detail away.
func TestMgmtSkillDetailPromoteInPlace(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSkills()))
	m.panelSel = 1 // tainted-thing (needs review)
	m.Update(key("enter"))
	if !m.panelDetail {
		t.Fatal("detail did not open")
	}
	_, cmd := m.Update(key("p"))
	if cmd == nil {
		t.Fatal("p did not fire promote from the detail view")
	}
	_, cmd2 := m.Update(exec(cmd)) // mgmtActionMsg → afterMgmtAction refetch
	m.Update(exec(cmd2))           // mgmtMsg lands, tab rebuilt
	if m.panelDetail {
		t.Error("detail stayed open after the promote refetch")
	}
}

// TestMgmtMemoryDetail verifies facts and pending episodes expand, and q
// folds the detail like esc.
func TestMgmtMemoryDetail(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openMemory()))

	m.panelSel = 0 // a user fact
	m.Update(key("enter"))
	out := plain(m.View())
	if !strings.Contains(out, "user fact") || !strings.Contains(out, "prefers vim") {
		t.Errorf("fact detail missing text:\n%s", out)
	}
	m.Update(key("q"))
	if m.panelDetail {
		t.Error("q did not fold the detail")
	}

	m.panelSel = 2 // the pending episode
	m.Update(key("enter"))
	out = plain(m.View())
	if !strings.Contains(out, "pending episode") || !strings.Contains(out, "fixed the login bug") {
		t.Errorf("episode detail missing summary:\n%s", out)
	}
}

// TestMgmtToolsDetail verifies built-in tools and MCP servers expand with
// their arguments.
func TestMgmtToolsDetail(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openTools()))

	m.panelSel = 0 // a built-in tool
	m.Update(key("enter"))
	if !strings.Contains(plain(m.View()), "shell") {
		t.Errorf("tool detail missing name:\n%s", plain(m.View()))
	}

	m.closeDetail()
	m.panelSel = len(m.toolRows) - 1 // the MCP server row
	m.Update(key("enter"))
	out := plain(m.View())
	for _, want := range []string{"mcp-fs", "--ro"} {
		if !strings.Contains(out, want) {
			t.Errorf("mcp detail missing %q:\n%s", want, out)
		}
	}
}

// TestMgmtConfigFlattenAndRawDetail verifies nested config values flatten one
// level in the list and deeper values render as indented JSON in the detail.
func TestMgmtConfigFlattenAndRawDetail(t *testing.T) {
	rows := buildCfgRows(map[string]any{
		"model":   "m",
		"sandbox": map[string]any{"enabled": true},
		"deep":    map[string]any{"a": map[string]any{"b": 1}},
		"servers": []any{"x"},
		"miss_me": "",
	}, client.Usage{}, nil)

	want := map[string]string{
		"model":           "m",
		"sandbox.enabled": "true",
		"deep.a":          "·", // two levels down keeps the marker…
		"servers":         "·", // …as do slices
	}
	seen := map[string]string{}
	for _, r := range rows {
		seen[r.k] = r.v
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("cfg row %q = %q, want %q (rows: %v)", k, seen[k], v, seen)
		}
	}

	// The marker row keeps its raw value: the detail view shows the JSON.
	m := newTestModel()
	m.panel = panelConfig
	m.cfgRows = rows
	for i, r := range rows {
		if r.k != "deep.a" {
			continue
		}
		m.panelSel = i
	}
	m.panelDetail = true
	out := plain(m.View())
	if !strings.Contains(out, `"b": 1`) {
		t.Errorf("deep config detail missing JSON body:\n%s", out)
	}
}

// TestPanelDetailScrollAndTabs verifies detail scrolling clamps and tab
// switches (] [ and digits) leave the detail behind.
func TestPanelDetailScrollAndTabs(t *testing.T) {
	m := wired(t)
	m.Update(exec(m.openSkills()))
	m.Update(key("enter"))

	m.detailScroll = 5
	m.Update(key("up"))
	if m.detailScroll != 4 {
		t.Errorf("up: scroll = %d, want 4", m.detailScroll)
	}
	m.detailScroll = 0
	m.Update(key("up"))
	if m.detailScroll != 0 {
		t.Errorf("up at top: scroll = %d, want clamp 0", m.detailScroll)
	}
	// Shrink the window so the skill detail overflows and scrolling has
	// somewhere to go; down stops at the last line, not beyond.
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	for i := 0; i < 200; i++ {
		m.Update(key("down"))
	}
	if m.detailScroll <= 0 || m.detailScroll != m.detailMaxScroll() {
		t.Errorf("down: scroll = %d, want clamp at max %d", m.detailScroll, m.detailMaxScroll())
	}
	m.Update(key("up"))
	if m.detailScroll != m.detailMaxScroll()-1 {
		t.Errorf("up: scroll = %d, want %d", m.detailScroll, m.detailMaxScroll()-1)
	}

	// Digit jumps switch tabs and reset the detail.
	m.Update(key("2")) // runs
	if m.panelDetail || m.panel != panelRuns {
		t.Errorf("digit jump: detail=%v panel=%d", m.panelDetail, m.panel)
	}
	m.Update(exec(m.openSkills()))
	m.Update(key("enter"))
	if !m.panelDetail {
		t.Fatal("detail did not reopen")
	}
	m.Update(key("]")) // skills → tools
	if m.panelDetail || m.panel != panelTools {
		t.Errorf("]: detail=%v panel=%d", m.panelDetail, m.panel)
	}
}

// TestSkillSelRow accounts for description lines when windowing the skills
// list: item 1 with one description above sits on visual row 2.
func TestSkillSelRow(t *testing.T) {
	m := newTestModel()
	m.panel = panelSkills
	m.skills = []client.Skill{
		{Name: "a", Description: "has one"},
		{Name: "b"},
		{Name: "c"},
	}
	m.panelSel = 1
	if got := m.skillSelRow(); got != 2 {
		t.Errorf("skillSelRow = %d, want 2", got)
	}
	m.panelSel = 2
	if got := m.skillSelRow(); got != 3 {
		t.Errorf("skillSelRow(2) = %d, want 3 (desc above + row 0)", got)
	}
}
