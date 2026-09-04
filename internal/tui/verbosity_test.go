package tui

import (
	"strings"
	"testing"
)

// verbosity_test.go — F5: one noise dial instead of five toggles. Quiet
// hides info-tier traces and forces compact steps; detailed implies ^E;
// normal is exactly today's behavior. Hints and the dial's own ack always
// show — they are the discovery path for the dial itself.

func TestVerbosityFromNames(t *testing.T) {
	cases := map[string]int{
		"quiet":     verbosityQuiet,
		"QUIET":     verbosityQuiet,
		"normal":    verbosityNormal,
		"":          verbosityNormal,
		"nonsense":  verbosityNormal,
		"detailed":  verbosityDetailed,
		"verbose":   verbosityDetailed,
		" detailed": verbosityDetailed,
	}
	for name, want := range cases {
		if got := verbosityFrom(name); got != want {
			t.Errorf("verbosityFrom(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestVerbosityDialCyclesViaCommand(t *testing.T) {
	m := newTestModel()
	want := []int{verbosityQuiet, verbosityDetailed, verbosityNormal, verbosityQuiet}
	for i, v := range want {
		m.runCommandLine("/verbosity")
		if m.verbosity != v {
			t.Fatalf("cycle %d: verbosity = %d, want %d", i, m.verbosity, v)
		}
	}
	if !strings.Contains(strings.Join(m.notices, "\n"), "verbosity: quiet") {
		t.Fatalf("the dial must acknowledge itself even from quiet: %q", m.notices)
	}
}

func TestQuietSuppressesInfoNotesOnly(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/verbosity quiet")
	m.notices, m.noticeExp = nil, nil // drop the dial ack — isolate the effects

	m.addTransientNote("info trace")
	if countNotices(m, "info trace") != 0 {
		t.Fatal("quiet must suppress info-tier transient notes")
	}
	m.addNote("boom")
	if countNotices(m, "boom") != 1 {
		t.Fatal("quiet must keep alert-tier notes (errors, warnings)")
	}
	m.teach(hintQueue, "tip: teaching beats silence")
	if countNotices(m, "tip: teaching beats silence") != 1 {
		t.Fatal("quiet must not suppress JIT hints — they are the discovery path")
	}
}

func TestQuietSuppressesQueuedAck(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/verbosity quiet")
	m.notices, m.noticeExp = nil, nil
	m.hintsShown = map[string]bool{hintQueue: true} // isolate the ack effect

	m.busy = true
	m.ta.SetValue("held prompt")
	m.submit()
	if countNotices(m, "queued — it sends") != 0 {
		t.Fatalf("quiet must suppress the queued ack: %q", m.notices)
	}
	if len(m.queue) != 1 {
		t.Fatalf("the prompt must still queue: %d", len(m.queue))
	}
}

func TestDetailedImpliesExpandAll(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/verbosity detailed")
	if !m.expandAll {
		t.Fatal("detailed must imply the ^E expand-all view")
	}
	m.runCommandLine("/verbosity quiet")
	if m.expandAll {
		t.Fatal("quiet must force compact steps")
	}
	m.runCommandLine("/verbosity normal")
	if m.expandAll {
		t.Fatal("normal must reset to the compact default")
	}
}

func TestVerbosityRejectsUnknownArg(t *testing.T) {
	m := newTestModel()
	m.runCommandLine("/verbosity loud")
	if m.verbosity != verbosityNormal {
		t.Fatalf("unknown arg must not move the dial: %d", m.verbosity)
	}
	if !strings.Contains(strings.Join(m.notices, "\n"), "quiet, normal, or detailed") {
		t.Fatalf("unknown arg must explain the choices: %q", m.notices)
	}
}

func TestPaletteCarriesVerbosityEntries(t *testing.T) {
	m := newTestModel()
	m.pal = palState{open: true}
	m.pal.all = m.basePaletteEntries()
	for _, want := range []string{"verbosity: quiet", "verbosity: normal", "verbosity: detailed"} {
		found := false
		for _, e := range m.pal.all {
			if strings.HasPrefix(e.title, want) {
				found = true
				e.run(m)
			}
		}
		if !found {
			t.Errorf("palette missing %q", want)
		}
	}
	if m.verbosity != verbosityDetailed {
		t.Fatalf("the last palette run should have landed detailed, got %d", m.verbosity)
	}
}
