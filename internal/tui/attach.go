package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	info, err := os.Stat(path)
	if err != nil {
		return m.transientNoteCmd("attach: " + err.Error())
	}
	if info.IsDir() {
		return m.transientNoteCmd("attach: " + path + " is a directory")
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
	data, err := os.ReadFile(path)
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
			return m.transientNoteCmd("restaged " + name + " (" + human(len(data)) + ")")
		}
	}
	m.attachments = append(m.attachments, client.Attachment{Name: name, Content: string(data)})
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
		return m.transientNoteCmd("dropped " + plural(n, "staged file", "staged files"))
	}
	for i := range m.attachments {
		if m.attachments[i].Name == name {
			m.attachments = append(m.attachments[:i], m.attachments[i+1:]...)
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
