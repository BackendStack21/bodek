package workspace

import (
	"os"
	"testing"
)

func TestRegressUnreadableWorkspaceNotOverwritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	t.Setenv("HOME", t.TempDir())
	p := Path()
	if err := os.MkdirAll(p[:len(p)-len("/workspace.json")], 0o700); err != nil {
		t.Fatal(err)
	}
	orig := `{"workspaces":{"/a":{"draft":"keep me"}}}`
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0)
	s := Open()
	s.Patch("/b", func(st *State) { st.Draft = "new" })
	_ = os.Chmod(p, 0o600)
	data, _ := os.ReadFile(p)
	if string(data) != orig {
		t.Fatalf("unreadable workspace file replaced: %q", data)
	}
}
