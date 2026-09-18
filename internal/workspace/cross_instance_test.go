package workspace

import (
	"testing"
)

// Regression: Save/Patch persisted the whole stale in-memory map, so two
// bodek instances in different cwds erased each other's drafts, queues,
// and session ids (last writer wins across EVERY cwd, not just its own).
// Each instance must merge its own cwd into the on-disk state it saw,
// never republish a whole stale snapshot.
func TestSaveKeepsOtherInstancesCwds(t *testing.T) {
	path := t.TempDir() + "/workspaces.json"
	t.Setenv("BODEK_WORKSPACE", path)

	// Instance A saves, then instance B opens the file fresh and saves.
	instA := Open()
	if err := instA.Save("/proj/a", State{Draft: "draft-a", SessionID: "s-a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	instB := Open()
	if err := instB.Save("/proj/b", State{Draft: "draft-b", SessionID: "s-b"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Instance A saves again — it must not wipe B's cwd.
	if err := instA.Save("/proj/a", State{Draft: "draft-a2", SessionID: "s-a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fresh := Open()
	if got := fresh.Load("/proj/b").Draft; got != "draft-b" {
		t.Errorf("instance A's Save wiped instance B's cwd: /proj/b draft = %q, want draft-b", got)
	}
	if got := fresh.Load("/proj/a").Draft; got != "draft-a2" {
		t.Errorf("/proj/a draft = %q, want draft-a2", got)
	}
}

func TestPatchKeepsOtherInstancesCwds(t *testing.T) {
	path := t.TempDir() + "/workspaces.json"
	t.Setenv("BODEK_WORKSPACE", path)

	instA := Open()
	instA.Save("/proj/a", State{History: []string{"a1"}})
	instB := Open()
	instB.Save("/proj/b", State{History: []string{"b1"}})

	instA.Patch("/proj/a", func(st *State) { st.Draft = "patched" })

	fresh := Open()
	if got := fresh.Load("/proj/b"); len(got.History) != 1 || got.History[0] != "b1" {
		t.Errorf("instance A's Patch wiped instance B's cwd /proj/b: %+v", got)
	}
	if got := fresh.Load("/proj/a"); got.Draft != "patched" {
		t.Errorf("/proj/a draft = %q, want patched", got.Draft)
	}
}
