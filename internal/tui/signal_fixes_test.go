package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// FIX-1: the failure BEL guard re-arms on a fresh LOCAL send, so two
// consecutive locally-submitted failed turns each ring once.
func TestFailureBellRearmsOnLocalSend(t *testing.T) {
	m := newTestModel()
	m.bell = true
	busyTurn(m)
	m.handleEvent(client.Event{Type: "error", Message: "boom"})
	if !m.failBellFired {
		t.Fatal("precondition: the first failed turn latched the bell guard")
	}
	// A fresh local prompt must re-arm the guard for the new turn.
	m.sendPrompt("try again")
	if m.failBellFired {
		t.Error("sendPrompt must re-arm the failure bell for the new turn")
	}
	// And the second failed turn rings again.
	m.handleEvent(client.Event{Type: "error", Message: "boom again"})
	if !m.failBellFired {
		t.Error("second failed turn must fire the bell again after re-arm")
	}
}

// FIX-2: verdict chips never claim success without confirmed metadata.
func TestSilentBuildChipIsNeutral(t *testing.T) {
	th := newTheme()
	// Silent output (empty or '(no output)') carries no exit metadata —
	// the chip is a neutral 'built', never a ✓ success claim.
	for _, res := range []string{"", "(no output)"} {
		got := plain(stepHeadSuffixFor("shell", "go build ./...", res, false, th))
		if strings.Contains(got, "✓") {
			t.Errorf("silent build output must not render ✓: %q", got)
		}
		if !strings.Contains(got, "built") {
			t.Errorf("silent build output should keep a neutral built chip: %q", got)
		}
	}
	if got := plain(stepHeadSuffixFor("shell", "go vet ./...", "", false, th)); strings.Contains(got, "✓") {
		t.Errorf("silent vet output must not render ✓: %q", got)
	}
	// A step that failed (isErr) never paints any success-flavored chip —
	// not even the neutral one.
	if got := plain(stepHeadSuffixFor("shell", "go build ./...", "", true, th)); got != "" {
		t.Errorf("isErr step with silent output must render no build chip: %q", got)
	}
	if got := plain(stepHeadSuffixFor("shell", "go vet ./...", "", true, th)); got != "" {
		t.Errorf("isErr step with silent output must render no vet chip: %q", got)
	}
}

// FIX-3: a resumed turn whose steps all carry dur 0 omits the duration
// segment instead of inventing '<1s'.
func TestFoldTallyOmitsZeroDuration(t *testing.T) {
	msg := message{role: roleAsst, streaming: false}
	msg.steps = append(msg.steps,
		step{name: "read_file", arg: "a.go", done: true, dur: 0},
		step{name: "shell", arg: "ls", done: true, dur: 0},
	)
	msg.items = append(msg.items, turnItem{stepIdx: 0}, turnItem{stepIdx: 1})
	got := foldTally(msg)
	if strings.Contains(got, "<1s") {
		t.Errorf("all-zero durs must not render '<1s': %q", got)
	}
	if strings.Contains(got, "·") && strings.Count(got, "·") > 0 && !strings.HasPrefix(got, "2 tools") {
		t.Errorf("tally must still lead with the tools count: %q", got)
	}
	if got != "2 tools" {
		t.Errorf("zero-duration tally = %q, want %q", got, "2 tools")
	}
	// A sub-second live turn still shows '<1s' — only the zero total drops.
	msg.steps[0].dur = 300 * time.Millisecond
	if got := foldTally(msg); !strings.Contains(got, "<1s") {
		t.Errorf("sub-second total must still show '<1s': %q", got)
	}
}

// FIX-5: a markdown heading alone must not paint 'build failed'.
func TestBuildFailMarkdownHeadingNotFailure(t *testing.T) {
	th := newTheme()
	if got := plain(stepHeadSuffixFor("shell", "make build",
		"# Introduction\n\nSome prose about the build system.", false, th)); got != "" {
		t.Errorf("markdown heading must not render a build chip: %q", got)
	}
	// A real go-build header followed by a file:line diagnostic still fails.
	if got := plain(stepHeadSuffixFor("shell", "make build",
		"# github.com/x/y\ny.go:9:2: undefined: Foo", false, th)); got != "build failed" {
		t.Errorf("go-build header + diagnostic must render build failed: %q", got)
	}
}

// FIX-6: renderNotices prefers an unexpired alert-tier notice over a newer
// transient one — the alert renders, the transient folds into the count.
func TestNoticeAlertTierWins(t *testing.T) {
	m := newTestModel()
	m.addNote("error: provider down")
	m.transientNoteCmd("skill · loaded")
	out := plain(m.renderNotices())
	if !strings.Contains(out, "error: provider down") {
		t.Errorf("alert-tier notice must render over a newer transient: %q", out)
	}
	if strings.Contains(out, "skill · loaded") {
		t.Errorf("newer transient must fold into the count: %q", out)
	}
	if !strings.Contains(out, "1 note") {
		t.Errorf("folded transient must be counted: %q", out)
	}
}
