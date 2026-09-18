package tui

import (
	"testing"
)

// TestJobsReopenKillsStaleTabTick guards the double-arm race: open → close
// → reopen leaves a tab tick in flight from the FIRST open. When it fires,
// its (now stale) seq must be rejected — otherwise it passes the seq check
// against the reopened tab and arms a second permanent watcher chain,
// doubling the /api/jobs poll rate for the session.
func TestJobsReopenKillsStaleTabTick(t *testing.T) {
	m := newJobsTestModel(t, nil)

	m.openJobs() // first open: arms a 3s tab chain (tick in flight)
	seqFirst := m.jobsSeq
	m.jobsSeq-- // simulate: the in-flight tick captured seqFirst
	m.jobsSeq++

	// Reopen (close/reopen cycle): must invalidate the first chain's tick.
	m.openJobs()

	stale := jobsTickMsg{seq: seqFirst, watch: false}
	cmd := m.handleJobsTick(stale)
	if cmd != nil {
		// Even if it fetched, it must not hand the cadence to a fresh
		// watcher chain on top of the reopened tab's own chain.
		t.Fatal("stale tab tick from the first open was accepted after reopen")
	}
	if m.jobsSeq != seqFirst+1 {
		t.Fatalf("reopen did not bump the tab generation: %d vs %d", m.jobsSeq, seqFirst+1)
	}
}
