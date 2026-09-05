package tui

import (
	"strings"
	"testing"
	"time"
)

// turnFixture builds a two-turn transcript: both finalized with stats, the
// turns tall enough to scroll between.
func turnFixture(t *testing.T) *Model {
	t.Helper()
	m := newTestModel()

	run := func(prompt, answer string) {
		m.msgs = append(m.msgs, message{role: roleUser, content: prompt, sentAt: time.Now()})
		m.msgs = append(m.msgs, message{role: roleAsst, content: answer})
		i := len(m.msgs) - 1
		m.msgs[i].stats = &turnStats{latency: 2.0, ctxTok: 900, outTok: 120, toolCount: 2}
	}
	run("first question", strings.Repeat("first answer line\n", 8))
	run("second question", strings.Repeat("second answer line\n", 8))
	m.refresh()
	return m
}

// TestTurnFootCarriesTelemetry verifies the sealed stat line sits under the
// assistant reply, not on the "⬡ odek" head.
func TestTurnFootCarriesTelemetry(t *testing.T) {
	m := turnFixture(t)
	out, _ := m.renderMessage(m.msgs[1], 1, 0)
	lines := strings.Split(plain(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("turn render too short:\n%s", plain(out))
	}
	head := lines[0]
	if strings.Contains(head, "⚡") || strings.Contains(head, "2.0s") {
		t.Errorf("telemetry still rides the turn head:\n%s", head)
	}
	if !strings.Contains(head, "odek") {
		t.Errorf("turn head missing identity:\n%s", head)
	}
	foot := lines[len(lines)-1]
	for _, want := range []string{"⚡", "2.0s", "900", "120", "⚒"} {
		if !strings.Contains(foot, want) {
			t.Errorf("turn foot missing %q:\n%s", want, foot)
		}
	}
}

// TestTurnCollapseFold verifies ^F folds the last turn to its head + summary
// and back, invalidating the prefix cache.
func TestTurnCollapseFold(t *testing.T) {
	m := turnFixture(t)
	full, _ := m.renderMessage(m.msgs[3], 3, 0)
	fullLines := lineCount(full)

	m.toggleCollapseLast()
	if !m.msgs[3].collapsed {
		t.Fatal("last turn not collapsed")
	}
	// The refresh inside toggleCollapseLast rebuilds the prefix with the
	// folded card — the cached transcript shrinks accordingly.
	folded, _ := m.renderMessage(m.msgs[3], 3, 0)
	out := plain(folded)
	if !strings.Contains(out, "collapsed") || !strings.Contains(out, "reply:") {
		t.Errorf("folded card missing its summary:\n%s", out)
	}
	if got := lineCount(folded); got >= fullLines {
		t.Errorf("folded card is not shorter: %d vs %d", got, fullLines)
	}
	// The sealed telemetry survives folding, still under the card.
	foldLines := strings.Split(out, "\n")
	if strings.Contains(foldLines[0], "2.0s") {
		t.Errorf("folded telemetry must not ride the head:\n%s", out)
	}
	if !strings.Contains(foldLines[len(foldLines)-1], "2.0s") {
		t.Errorf("folded card lost telemetry foot:\n%s", out)
	}

	m.toggleCollapseLast()
	if m.msgs[3].collapsed {
		t.Fatal("second ^F did not unfold")
	}
}

// TestTurnJump verifies [ and ] scroll between assistant turn heads.
func TestTurnJump(t *testing.T) {
	m := turnFixture(t)
	m.refresh()
	m.vp.GotoBottom()

	// [ jumps to the previous turn's head: strictly upward from the bottom.
	m.jumpTurn(false)
	if m.vp.YOffset >= m.vp.TotalLineCount()-m.vp.Height {
		t.Errorf("[ did not move up: yoffset=%d", m.vp.YOffset)
	}
	first := m.vp.YOffset

	// ] jumps forward to the next head, strictly below the previous target.
	m.jumpTurn(true)
	if m.vp.YOffset <= first {
		t.Errorf("] did not jump forward: %d → %d", first, m.vp.YOffset)
	}

	// [ from the very top goes to the first head (or top) without error.
	m.vp.GotoTop()
	m.jumpTurn(false)
}

// TestUserHeadAge verifies the user turn head shows a coarse age when the
// send time is known, and nothing when it is not (resumed transcripts).
func TestUserHeadAge(t *testing.T) {
	m := newTestModel()
	old := time.Now().Add(-90 * time.Minute)
	out, _ := m.renderMessage(message{role: roleUser, content: "hi", sentAt: old}, 0, 0)
	if !strings.Contains(plain(out), "90d ago") && !strings.Contains(plain(out), "ago") {
		// age formats via ago(): 90 minutes renders as "1h ago"
		t.Errorf("user head missing age:\n%s", plain(out))
	}
	if !strings.Contains(plain(out), "1h ago") {
		t.Errorf("expected coarse age on the user head:\n%s", plain(out))
	}
	out, _ = m.renderMessage(message{role: roleUser, content: "hi"}, 0, 0)
	if strings.Contains(plain(out), "ago") {
		t.Errorf("resumed user turns should render no age:\n%s", plain(out))
	}
}

// TestTurnHeadEvenGap pins one blank row after ❯ you / ⬡ odek — the same
// gap that sits between messages and around the answer card.
func TestTurnHeadEvenGap(t *testing.T) {
	m := turnFixture(t)
	user, _ := m.renderMessage(m.msgs[0], 0, 0)
	asst, _ := m.renderMessage(m.msgs[1], 1, 0)
	for _, tc := range []struct {
		name, out, head string
	}{
		{"user", plain(user), "you"},
		{"assistant", plain(asst), "odek"},
	} {
		lines := strings.Split(tc.out, "\n")
		if len(lines) < 3 {
			t.Fatalf("%s render too short:\n%s", tc.name, tc.out)
		}
		if !strings.Contains(lines[0], tc.head) {
			t.Errorf("%s head = %q, want %q", tc.name, lines[0], tc.head)
		}
		if lines[1] != "" {
			t.Errorf("%s missing blank after the head:\n%s", tc.name, tc.out)
		}
		if len(lines) < 3 || lines[2] == "" {
			t.Errorf("%s doubled the gap after the head:\n%s", tc.name, tc.out)
		}
	}
}

// TestTurnHeadMouseToggle verifies clicking a turn head folds that turn.
func TestTurnHeadMouseToggle(t *testing.T) {
	m := turnFixture(t)
	m.refresh()
	if len(m.turnLineIndex) != 2 {
		t.Fatalf("turn index = %d, want 2", len(m.turnLineIndex))
	}
	head := m.turnLineIndex[1] // second assistant turn
	m.toggleCollapseAt(head.msgIdx)
	if !m.msgs[head.msgIdx].collapsed {
		t.Fatal("click on turn head did not fold the turn")
	}
}
