package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/update"
)

// TestSwapAsideWindowsRollback guards the Windows swap: if the second
// rename (new binary into place) fails after the running binary was moved
// aside, the old binary must be restored — otherwise the install path is
// bricked with the executable stranded as .old.
func TestSwapAsideWindowsRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bodek")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".bodek-upgrade-new")
	if err := os.WriteFile(tmp, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	realRename := renameFn
	defer func() { renameFn = realRename }()
	calls := 0
	renameFn = func(from, to string) error {
		calls++
		if calls == 2 { // the "install new binary" rename fails
			return errors.New("access denied")
		}
		return realRename(from, to)
	}

	err := swapAsideWindows(target, tmp)
	if err == nil {
		t.Fatal("expected the injected rename failure to surface")
	}
	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("executable missing after failed swap: %v", rerr)
	}
	if string(got) != "old-binary" {
		t.Fatalf("old binary not restored after failed swap: %q", got)
	}
}

// TestReplaceExecutableSyncsBeforeRename guards durability: the new binary
// must be fsynced before the rename, or a crash can persist the rename
// with no/partial data behind it — a zero-length bodek — despite the
// "never leaves a truncated binary" contract.
func TestReplaceExecutableSyncsBeforeRename(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bodek")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	realSync := syncFile
	defer func() { syncFile = realSync }()
	synced := false
	syncFile = func(f *os.File) error { synced = true; return realSync(f) }

	if err := replaceExecutable([]byte("new"), target); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}
	if !synced {
		t.Fatal("new binary was never fsynced before the rename")
	}
}

// TestUpgradeSlowLinkDownloads guards the download budget: Client.Timeout
// bounds the whole body read, so reusing the short API client for a
// multi-MB archive fails every transfer slower than that budget. The
// download path must not inherit the API client's overall deadline.
func TestUpgradeSlowLinkDownloads(t *testing.T) {
	archive := buildTarGz(t, "bodek", []byte("slow-payload"))
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write(archive)
	}))
	defer slow.Close()

	// A 300ms overall client budget vs a 2s drip — margins wide enough to
	// be CI-stable in both directions.
	c := &http.Client{Timeout: 300 * time.Millisecond}
	data, err := download(context.Background(), c, slow.URL+"/bodek.tar.gz")
	if err != nil {
		t.Fatalf("download on a slow link failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty download")
	}
}

// TestNewerPseudoVersion guards commit-installed builds: a Go pseudo-version
// stamp (v0.1.3-0.20260901abcdef12-abc1234) must compare by its release
// prefix, not report "already up to date" against every future release.
func TestNewerPseudoVersion(t *testing.T) {
	if !update.Newer("v9.9.9", "v0.1.3-0.20260901000000-abc1234") {
		t.Fatal("v9.9.9 must be newer than a v0.1.3 pseudo-version stamp")
	}
	if update.Newer("v0.1.3", "v0.1.3-0.20260901000000-abc1234") {
		t.Fatal("v0.1.3 must not upgrade over a v0.1.3 pseudo-version")
	}
	// A genuine semver prerelease must not be misparsed as a pseudo-version.
	if !update.Newer("v1.3.0", "v1.2.0-0.1") {
		t.Fatal("v1.3.0 must be newer than prerelease v1.2.0-0.1")
	}
}
