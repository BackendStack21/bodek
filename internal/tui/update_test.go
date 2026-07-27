package tui

import (
	"errors"
	"strings"
	"testing"
)

// TestHeaderShowsOdekVersion verifies the engine version appears in the header
// left cluster when known, and leaves no stray separator when it is not.
func TestHeaderShowsOdekVersion(t *testing.T) {
	m := newTestModel()
	m.model = "deepseek-v4-flash"
	m.odekVersion = "v0.2.0"
	if out := plain(m.header()); !strings.Contains(out, "odek v0.2.0") {
		t.Errorf("header missing odek version: %q", out)
	}

	m.odekVersion = ""
	if out := plain(m.header()); strings.Contains(out, "odek v") {
		t.Errorf("header should hide the odek segment when unknown: %q", out)
	}
}

// TestHeaderShowsBodekVersion verifies bodek's own version rides next to the
// logo, with a v prefix for bare semver numbers and verbatim otherwise.
func TestHeaderShowsBodekVersion(t *testing.T) {
	m := newTestModel()
	m.bodekVersion = "0.0.11"
	if out := plain(m.header()); !strings.Contains(out, "bodek v0.0.11") {
		t.Errorf("header missing bodek version: %q", out)
	}

	m.bodekVersion = "dev"
	if out := plain(m.header()); !strings.Contains(out, "bodek dev") {
		t.Errorf("header missing dev marker: %q", out)
	}

	m.bodekVersion = ""
	if out := plain(m.header()); strings.Contains(out, "bodek v") || strings.Contains(out, "bodek dev") {
		t.Errorf("header should hide the bodek version when unknown: %q", out)
	}
}

func TestShouldCheckUpdate(t *testing.T) {
	for _, v := range []string{"", "dev"} {
		if shouldCheckUpdate(v) {
			t.Errorf("shouldCheckUpdate(%q) = true, want false", v)
		}
	}
	for _, v := range []string{"0.0.11", "v0.0.11"} {
		if !shouldCheckUpdate(v) {
			t.Errorf("shouldCheckUpdate(%q) = false, want true", v)
		}
	}
}

// TestCheckUpdateGating verifies dev/unstamped builds never schedule the
// network check, while a stamped build does.
func TestCheckUpdateGating(t *testing.T) {
	m := newTestModel()
	for _, v := range []string{"", "dev"} {
		m.bodekVersion = v
		if cmd := m.checkUpdate(); cmd != nil {
			t.Errorf("checkUpdate() with version %q should return nil", v)
		}
	}
	m.bodekVersion = "0.0.11"
	if cmd := m.checkUpdate(); cmd == nil {
		t.Error("checkUpdate() with a stamped version should schedule the check")
	}
}

// TestUpdateCheckMsgNewer drives the async result through Update: a newer
// latest release surfaces the upgrade hint as a sticky note.
func TestUpdateCheckMsgNewer(t *testing.T) {
	m := newTestModel()
	m.bodekVersion = "0.0.11"
	_, _ = m.Update(updateCheckMsg{latest: "v0.0.12"})
	joined := strings.Join(m.notices, "\n")
	if !strings.Contains(joined, "bodek v0.0.12 available") ||
		!strings.Contains(joined, "bodek upgrade") {
		t.Errorf("upgrade hint missing, notices=%q", joined)
	}
}

// TestUpdateCheckMsgQuiet covers the no-nag paths: equal or older latest
// releases and failed lookups add no note.
func TestUpdateCheckMsgQuiet(t *testing.T) {
	for _, msg := range []updateCheckMsg{
		{latest: "v0.0.11"},          // equal
		{latest: "v0.0.10"},          // older
		{err: errors.New("offline")}, // failed lookup
		{latest: "not-a-version"},    // unparsable tag
	} {
		m := newTestModel()
		m.bodekVersion = "0.0.11"
		_, _ = m.Update(msg)
		if len(m.notices) != 0 {
			t.Errorf("updateCheckMsg %+v added a note: %v", msg, m.notices)
		}
	}
}
