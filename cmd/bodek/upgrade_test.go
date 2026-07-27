package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildTarGz packs data as a single file into a tar.gz archive.
func buildTarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar entry: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// buildZip packs data as a single file into a zip archive.
func buildZip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// upgradeServer serves a fake latest release: the API JSON, a tar.gz archive
// holding newBinary, and a checksums.txt matching it. If badChecksum is set,
// checksums.txt serves a wrong digest for the archive.
func upgradeServer(t *testing.T, tag, assetName string, archive []byte, badChecksum bool) *httptest.Server {
	t.Helper()
	digest := sha256Hex(archive)
	if badChecksum {
		digest = sha256Hex([]byte("something else"))
	}
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "bodek-updater" {
			t.Errorf("expected User-Agent bodek-updater, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[`+
			`{"name":%q,"browser_download_url":%q},`+
			`{"name":"checksums.txt","browser_download_url":%q}]}`,
			tag, assetName, base+"/files/"+assetName, base+"/files/checksums.txt")
	})
	mux.HandleFunc("/files/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/files/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
	})
	srv := httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

// installFake writes a pretend installed bodek binary in a temp dir.
func installFake(t *testing.T, content []byte) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "bodek")
	if err := os.WriteFile(exe, content, 0o755); err != nil {
		t.Fatalf("install fake binary: %v", err)
	}
	return exe
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestUpgradeHappyPath(t *testing.T) {
	setVersion(t, "0.0.1")
	newBinary := []byte("#!/bin/sh\necho new bodek\n")
	assetName := archiveName("9.9.9", runtime.GOOS, runtime.GOARCH)
	srv := upgradeServer(t, "v9.9.9", assetName, buildTarGz(t, "bodek", newBinary), false)
	exe := installFake(t, []byte("old bodek"))

	var out bytes.Buffer
	if err := upgrade(context.Background(), srv.Client(), srv.URL+"/latest", exe, &out); err != nil {
		t.Fatalf("upgrade returned error: %v", err)
	}
	if got := readFile(t, exe); !bytes.Equal(got, newBinary) {
		t.Errorf("expected executable to be replaced with new binary, got %q", got)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("stat upgraded executable: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("expected 0755 permissions, got %v", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "upgraded bodek v0.0.1 → v9.9.9") {
		t.Errorf("missing upgrade confirmation in output: %q", out.String())
	}
}

func TestUpgradeAlreadyUpToDate(t *testing.T) {
	setVersion(t, "9.9.9")
	assetName := archiveName("9.9.9", runtime.GOOS, runtime.GOARCH)
	srv := upgradeServer(t, "v9.9.9", assetName, buildTarGz(t, "bodek", []byte("x")), false)
	exe := installFake(t, []byte("current bodek"))

	var out bytes.Buffer
	if err := upgrade(context.Background(), srv.Client(), srv.URL+"/latest", exe, &out); err != nil {
		t.Fatalf("upgrade returned error: %v", err)
	}
	if got, want := out.String(), "bodek is already up to date (v9.9.9)\n"; got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	if got := readFile(t, exe); string(got) != "current bodek" {
		t.Errorf("executable must be untouched, got %q", got)
	}
}

func TestUpgradeFromDevBuild(t *testing.T) {
	setVersion(t, "")
	assetName := archiveName("9.9.9", runtime.GOOS, runtime.GOARCH)
	srv := upgradeServer(t, "v9.9.9", assetName, buildTarGz(t, "bodek", []byte("new")), false)
	exe := installFake(t, []byte("dev build"))

	var out bytes.Buffer
	if err := upgrade(context.Background(), srv.Client(), srv.URL+"/latest", exe, &out); err != nil {
		t.Fatalf("upgrade returned error: %v", err)
	}
	if !strings.Contains(out.String(), "unstamped (dev)") {
		t.Errorf("missing dev-build notice in output: %q", out.String())
	}
	if !strings.Contains(out.String(), "upgraded bodek dev → v9.9.9") {
		t.Errorf("missing upgrade confirmation in output: %q", out.String())
	}
}

func TestUpgradeChecksumMismatch(t *testing.T) {
	setVersion(t, "0.0.1")
	assetName := archiveName("9.9.9", runtime.GOOS, runtime.GOARCH)
	srv := upgradeServer(t, "v9.9.9", assetName, buildTarGz(t, "bodek", []byte("new")), true)
	exe := installFake(t, []byte("old bodek"))

	err := upgrade(context.Background(), srv.Client(), srv.URL+"/latest", exe, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	if got := readFile(t, exe); string(got) != "old bodek" {
		t.Errorf("executable must be untouched after checksum failure, got %q", got)
	}
}

func TestUpgradeMissingPlatformAsset(t *testing.T) {
	setVersion(t, "0.0.1")
	// Serve a release whose only asset targets a different platform.
	otherName := archiveName("9.9.9", "plan9", "amd64")
	srv := upgradeServer(t, "v9.9.9", otherName, buildTarGz(t, "bodek", []byte("new")), false)
	exe := installFake(t, []byte("old bodek"))

	err := upgrade(context.Background(), srv.Client(), srv.URL+"/latest", exe, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no asset") {
		t.Fatalf("expected missing-asset error, got %v", err)
	}
	if got := readFile(t, exe); string(got) != "old bodek" {
		t.Errorf("executable must be untouched, got %q", got)
	}
}

func TestVerifyChecksumMissingEntry(t *testing.T) {
	err := verifyChecksum([]byte("data"), []byte("abc123  other.tar.gz\n"), "bodek.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("expected missing-entry error, got %v", err)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	want := []byte("windows bodek")
	got, err := extractBinary(buildZip(t, "bodek.exe", want), "bodek_9.9.9_windows_amd64.zip")
	if err != nil {
		t.Fatalf("extractBinary returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractBinaryNoBinary(t *testing.T) {
	archive := buildTarGz(t, "README.md", []byte("docs"))
	if _, err := extractBinary(archive, "bodek_9.9.9_linux_amd64.tar.gz"); err == nil {
		t.Fatal("expected error when archive holds no bodek binary")
	}
}

func TestReplaceExecutableFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	realExe := filepath.Join(dir, "bodek-real")
	if err := os.WriteFile(realExe, []byte("old"), 0o755); err != nil {
		t.Fatalf("write real binary: %v", err)
	}
	link := filepath.Join(dir, "bodek")
	if err := os.Symlink(realExe, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := replaceExecutable([]byte("new"), link); err != nil {
		t.Fatalf("replaceExecutable returned error: %v", err)
	}
	if got := readFile(t, realExe); string(got) != "new" {
		t.Errorf("expected symlink target to be replaced, got %q", got)
	}
	linkDest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("symlink must survive the upgrade: %v", err)
	}
	if linkDest != realExe {
		t.Errorf("symlink target changed: %q → %q", realExe, linkDest)
	}
}
