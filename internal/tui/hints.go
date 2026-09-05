package tui

import "time"

// ── just-in-time hints ─────────────────────────────────────────────────────
//
// The teaching layer: one-time, state-triggered tips that surface a key the
// moment its feature first appears — the first queued prompt, the first
// sub-agent swarm, the first multi-step turn. Each fires exactly once per
// process (hintsShown guards it) and renders as an ordinary info-tier note
// (same strip, same fade), so it costs zero layout and zero new surfaces.
// Hints deliberately bypass the verbosity dial's quiet suppression: they are
// capped by construction, and they ARE the discoverability path for the
// dial itself.

// Hint keys — the states worth teaching, each exactly once.
const (
	hintQueue = "queue" // first prompt held while a turn runs
	hintSwarm = "swarm" // first sub-agent swarm frame on screen
	hintSteps = "steps" // first finished turn that carried tool steps
)

// teach pushes hint text as a one-time transient note. Fire-and-forget by
// design: every caller's return path already batches noticeSweep(), which
// fades whatever the strip holds — including this note.
func (m *Model) teach(key, text string) {
	if m.hintsShown == nil {
		m.hintsShown = make(map[string]bool)
	}
	if m.hintsShown[key] {
		return
	}
	m.hintsShown[key] = true
	m.pushNote("💡 "+text, time.Now().Add(hintTTL))
}
