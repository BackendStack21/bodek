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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BackendStack21/bodek/internal/update"
)

// runUpgrade self-updates the running executable to the latest release.
func runUpgrade(stdout io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return upgrade(context.Background(), client, update.LatestURL, exe, stdout)
}

// upgrade performs the full self-update against the given API endpoint,
// replacing the executable at exePath. runUpgrade wires in the production
// defaults; tests point it at an httptest server and a temp binary.
func upgrade(ctx context.Context, client *http.Client, apiURL, exePath string, stdout io.Writer) error {
	tag, assets, err := update.LatestRelease(ctx, client, apiURL)
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(tag, "v")
	current := strings.TrimPrefix(currentVersion(), "v")
	// Dev/unstamped builds always fetch. Anything else only upgrades when
	// GitHub is actually newer — equality used to downgrade local builds
	// stamped ahead of the latest release.
	if current != "dev" && !update.Newer(latest, current) {
		_, _ = fmt.Fprintf(stdout, "bodek is already up to date (v%s)\n", current)
		return nil
	}
	if current == "dev" {
		_, _ = fmt.Fprintln(stdout, "note: this build is unstamped (dev); upgrading to the latest release anyway")
	}

	name := archiveName(latest, runtime.GOOS, runtime.GOARCH)
	archiveURL, ok := assets[name]
	if !ok {
		return fmt.Errorf("release %s has no asset for %s/%s (looked for %s)", tag, runtime.GOOS, runtime.GOARCH, name)
	}
	checksumsURL, ok := assets["checksums.txt"]
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt asset", tag)
	}

	_, _ = fmt.Fprintf(stdout, "downloading %s …\n", name)
	archive, err := download(ctx, client, archiveURL)
	if err != nil {
		return err
	}
	checksums, err := download(ctx, client, checksumsURL)
	if err != nil {
		return err
	}
	// Supply-chain check: refuse to install anything the release checksums
	// don't vouch for.
	if err := verifyChecksum(archive, checksums, name); err != nil {
		return err
	}
	binary, err := extractBinary(archive, name)
	if err != nil {
		return err
	}
	if err := replaceExecutable(binary, exePath); err != nil {
		return err
	}

	if current == "dev" {
		_, _ = fmt.Fprintf(stdout, "upgraded bodek dev → v%s\n", latest)
	} else {
		_, _ = fmt.Fprintf(stdout, "upgraded bodek v%s → v%s\n", current, latest)
	}
	return nil
}

// archiveName mirrors the GoReleaser name_template: tar.gz everywhere except
// Windows, which ships a zip. tag must be the bare version (no "v" prefix).
func archiveName(tag, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("bodek_%s_%s_%s.%s", tag, goos, goarch, ext)
}

// download fetches url into memory. Release archives are a few MB, so a
// buffered read is fine and keeps checksum verification straightforward.
func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", "bodek-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return data, nil
}

// verifyChecksum checks the sha256 of data against the line checksums.txt
// publishes for name. The file uses the classic "<hex>  <name>" sha256sum
// format; a mismatch or missing entry is a hard error.
func verifyChecksum(data, checksums []byte, name string) error {
	var want string
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got sha256 %s, checksums.txt says %s", name, got, want)
	}
	return nil
}

// extractBinary pulls the bodek executable out of a release archive.
func extractBinary(archive []byte, name string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		return extractZip(archive)
	}
	return extractTarGz(archive)
}

// isBinaryName matches the executable inside a release archive, regardless
// of any directory prefix (GoReleaser keeps it at the archive root).
func isBinaryName(name string) bool {
	base := name[strings.LastIndex(name, "/")+1:]
	return base == "bodek" || base == "bodek.exe"
}

func extractTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open tar.gz archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && isBinaryName(hdr.Name) {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("archive contains no bodek binary")
}

func extractZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	for _, f := range zr.File {
		if !f.Mode().IsRegular() || !isBinaryName(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in zip: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", f.Name, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("archive contains no bodek binary")
}

// replaceExecutable atomically swaps the binary at target with data: the new
// file is written next to the target and renamed over it, so a crash
// mid-upgrade never leaves a truncated binary. target is resolved through
// symlinks first so `go install` shims and PATH links are not clobbered.
func replaceExecutable(data []byte, target string) error {
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolve executable path %s: %w", target, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".bodek-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp file next to %s: %w", resolved, err)
	}
	tmpName := tmp.Name()
	// No-op once the rename below has moved the temp file into place.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mark new binary executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("flush new binary: %w", err)
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		if runtime.GOOS != "windows" {
			return fmt.Errorf("replace %s: %w", resolved, err)
		}
		// Windows refuses to rename over a running executable; move the old
		// one aside first, then drop the new binary into place.
		old := resolved + ".old"
		_ = os.Remove(old)
		if rerr := os.Rename(resolved, old); rerr != nil {
			return fmt.Errorf("move current executable aside: %w", rerr)
		}
		if rerr := os.Rename(tmpName, resolved); rerr != nil {
			return fmt.Errorf("install new binary: %w", rerr)
		}
		_ = os.Remove(old) // best effort: a locked .old goes away on a later run
	}
	return nil
}
