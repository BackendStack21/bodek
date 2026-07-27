package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeBin writes an executable shell script to a temp dir and returns its path.
func writeBin(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "odek-fake")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake odek: %v", err)
	}
	return path
}

func TestBinVersion(t *testing.T) {
	cases := []struct {
		name, script, want string
	}{
		{"v-prefixed", "#!/bin/sh\necho 'odek v9.9.9'\n", "v9.9.9"},
		{"bare", "#!/bin/sh\necho 'odek 1.2.3'\n", "1.2.3"},
		{"first line wins", "#!/bin/sh\necho 'odek v9.9.9'\necho 'odek v0.0.1'\n", "v9.9.9"},
		{"unrecognized output", "#!/bin/sh\necho 'hello world'\n", ""},
		{"name only", "#!/bin/sh\necho 'odek'\n", ""},
		{"exit 1", "#!/bin/sh\nexit 1\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := binVersion(context.Background(), writeBin(t, tc.script)); got != tc.want {
				t.Errorf("binVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBinVersionMissingBinary(t *testing.T) {
	if got := binVersion(context.Background(), filepath.Join(t.TempDir(), "no-such-odek")); got != "" {
		t.Errorf("binVersion = %q, want empty for a nonexistent binary", got)
	}
}

func TestConnectSpawnCapturesVersion(t *testing.T) {
	// A spawned odek answers `version` on stdout and prints its serve banner
	// (with the WS token) on stderr; Connect must capture both.
	bin := writeBin(t, `#!/bin/sh
if [ "$1" = "version" ]; then
	echo 'odek v9.9.9'
	exit 0
fi
echo '  WS token:  cafef00d' >&2
`)
	conn, err := Connect(Options{Bin: bin})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Stop()
	if conn.Version != "v9.9.9" {
		t.Errorf("Version = %q, want %q", conn.Version, "v9.9.9")
	}
	if conn.Token != "cafef00d" {
		t.Errorf("Token = %q, want %q", conn.Token, "cafef00d")
	}
}
