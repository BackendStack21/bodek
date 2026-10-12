package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// Attachment limits mirror the server's prompt-attachment contract: 5 MB per
// file, 10 MB total per prompt. Bigger or binary content belongs in an
// @reference, where the server resolves, truncates, and wraps it itself.
const (
	maxAttachBytes      = 5 << 20
	maxAttachTotalBytes = 10 << 20
)

// binarySniffLen is how much of a file is inspected for NUL bytes — the
// classic "this is not text" marker — before staging it as an attachment.
const binarySniffLen = 512

// attachList notes the staged attachments, or their absence.
func (m *Model) attachList() tea.Cmd {
	if len(m.attachments) == 0 {
		return m.transientNoteCmd("no files staged — /attach <path>")
	}
	names := make([]string, 0, len(m.attachments))
	for _, a := range m.attachments {
		names = append(names, a.Name)
	}
	return m.transientNoteCmd("staged: " + strings.Join(names, ", "))
}

// attachFile stages a file for the next prompt. It is read client-side and
// sent as a prompt attachment — the server wraps it in the nonce'd
// untrusted-content envelope before the model sees it.
func (m *Model) attachFile(path string) tea.Cmd {
	path = strings.TrimSpace(path)
	if path == "" {
		return m.attachList()
	}
	if p, err := filepath.Abs(expandTilde(path)); err == nil {
		path = p
	}
	// Stage, name and persist the real file: the persisted path then lets a
	// later restore detect a symlink planted anywhere along it.
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	info, err := os.Stat(path)
	if err != nil {
		return m.transientNoteCmd("attach: " + err.Error())
	}
	if info.IsDir() {
		return m.transientNoteCmd("attach: " + path + " is a directory")
	}
	if !info.Mode().IsRegular() {
		// A FIFO would block the UI goroutine forever; /dev/zero reports
		// size 0 and would read without bound.
		return m.transientNoteCmd("attach: " + filepath.Base(path) + " is not a regular file")
	}
	if info.Size() > maxAttachBytes {
		return m.transientNoteCmd(fmt.Sprintf("attach: %s is %s — over the 5 MB per-file cap (use an @reference)",
			filepath.Base(path), human(int(info.Size()))))
	}
	var total int
	for _, a := range m.attachments {
		total += len(a.Content)
	}
	if total+int(info.Size()) > maxAttachTotalBytes {
		return m.transientNoteCmd("attach: staged files would exceed the 10 MB prompt cap")
	}
	data, err := readCapped(path, maxAttachBytes)
	if err != nil {
		return m.transientNoteCmd("attach: " + err.Error())
	}
	if idx := bytes.IndexByte(data[:min(len(data), binarySniffLen)], 0); idx >= 0 {
		return m.transientNoteCmd("attach: " + filepath.Base(path) + " looks binary — attach text files only")
	}
	name := filepath.Base(path)
	// Restaging the same file replaces it rather than duplicating.
	for i := range m.attachments {
		if m.attachments[i].Name == name {
			m.attachments[i].Content = string(data)
			if i < len(m.attachPaths) {
				m.attachPaths[i] = path
			} else {
				m.attachPaths = append(m.attachPaths, path)
			}
			m.persistLocal()
			return m.transientNoteCmd("restaged " + name + " (" + human(len(data)) + ")")
		}
	}
	m.attachments = append(m.attachments, client.Attachment{Name: name, Content: string(data)})
	m.attachPaths = append(m.attachPaths, path)
	m.persistLocal()
	return m.transientNoteCmd(fmt.Sprintf("attached %s (%s) — sends with the next prompt · /unattach to drop",
		name, human(len(data))))
}

// unattachFile drops one staged file by name, or the whole stage with no arg.
func (m *Model) unattachFile(name string) tea.Cmd {
	name = strings.TrimSpace(name)
	if name == "" {
		if len(m.attachments) == 0 {
			return m.transientNoteCmd("nothing staged")
		}
		n := len(m.attachments)
		m.attachments = nil
		m.attachPaths = nil
		m.persistLocal()
		return m.transientNoteCmd("dropped " + plural(n, "staged file", "staged files"))
	}
	for i := range m.attachments {
		if m.attachments[i].Name == name {
			m.attachments = append(m.attachments[:i], m.attachments[i+1:]...)
			if i < len(m.attachPaths) {
				m.attachPaths = append(m.attachPaths[:i], m.attachPaths[i+1:]...)
			}
			m.persistLocal()
			return m.transientNoteCmd("dropped " + name)
		}
	}
	return m.transientNoteCmd("not staged: " + name)
}

// expandTilde resolves a leading ~ to the user's home directory.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// readCapped reads at most limit bytes of path, failing if the file grew
// past it after the size check.
func readCapped(path string, limit int64) ([]byte, error) {
	// O_NONBLOCK: a path swapped for a FIFO after the caller's Stat must
	// not block the open; the descriptor is then re-checked, so the bytes
	// read always come from the regular file that was vetted.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s grew past the 5 MB per-file cap", filepath.Base(path))
	}
	return data, nil
}

// restoreAttachment re-stages a path persisted from an earlier run. Unlike
// an explicit /attach, nobody chose it this launch, and attachFile persists
// fully resolved paths: if it no longer resolves to itself, a symlink was
// planted somewhere along it (an agent with workspace write access can, and
// inside a bind-mounted sandbox it resolves on the host). It is skipped, so
// a host secret is never sent with the next prompt.
func (m *Model) restoreAttachment(path string) tea.Cmd {
	p := expandTilde(path)
	if real, err := filepath.EvalSymlinks(p); err == nil && real != filepath.Clean(p) {
		return m.transientNoteCmd("attach: " + filepath.Base(p) + " now resolves elsewhere — not re-attached")
	}
	return m.attachFile(p)
}
