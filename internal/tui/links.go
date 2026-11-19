package tui

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	osexec "os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Links in the transcript are wire-borne text: a click never opens one
// directly. It raises a confirmation window that shows the full target —
// host first — and only ⏎ hands it to the browser.

const (
	// maxURLLen bounds what is offered to a browser.
	maxURLLen = 2048
	// linkWrapRows is how far above and below the clicked row a wrapped
	// link is searched for.
	linkWrapRows = 16
	// linkDialogMax caps the confirmation window's width.
	linkDialogMax = 72
)

// urlRe matches an http(s) link up to the first character that cannot be
// part of one in prose, markdown, or JSON. "…" is bodek's truncation mark.
var urlRe = regexp.MustCompile("https?://[^\\s<>\"`\\\\…]+")

// jsonUnescape undoes the escapes encoders put inside a link.
var jsonUnescape = strings.NewReplacer(`\u0026`, "&", `\u003d`, "=", `\/`, "/")

// urlTailRe is the run of link characters at the start of a string.
var urlTailRe = regexp.MustCompile("^[^\\s<>\"`\\\\…]+")

// cleanURL shaves what prose and markdown hang on a link's tail (sentence
// punctuation, emphasis stars, an unbalanced closing bracket) and validates
// the rest. "" when what remains is not an openable http(s) URL.
func cleanURL(s string) string {
	for {
		n := len(s)
		s = strings.TrimRight(s, ".,;:!?'*")
		for _, pair := range [...]string{"()", "[]", "{}"} {
			if strings.HasSuffix(s, pair[1:]) && strings.Count(s, pair[:1]) < strings.Count(s, pair[1:]) {
				s = s[:len(s)-1]
			}
		}
		if len(s) == n {
			break
		}
	}
	if len(s) > maxURLLen || strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return s
}

// linkSources lists the links in the source text of the message rendered
// at a transcript line. Rendering wraps long links across rows (glamour
// breaks at "." and "-"), so a row alone can hold a fragment that is itself
// a valid — and wrong — URL; the source says where each link really ends.
func (m *Model) linkSources(line int) []string {
	idx := -1
	for _, r := range m.msgLineIndex {
		if r.line > line {
			break
		}
		idx = r.msgIdx
	}
	if idx < 0 || idx >= len(m.msgs) {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(texts ...string) {
		for _, t := range texts {
			// Tool results are often JSON: read the link through its escapes.
			for _, raw := range urlRe.FindAllString(jsonUnescape.Replace(sanitize(t)), -1) {
				if u := cleanURL(raw); u != "" && !seen[u] {
					seen[u] = true
					out = append(out, u)
				}
			}
		}
	}
	msg := &m.msgs[idx]
	add(msg.content, msg.thinking)
	for i := range msg.items {
		add(msg.items[i].text)
	}
	for i := range msg.steps {
		s := &msg.steps[i]
		add(s.arg, s.callArgs, s.result)
		add(s.logs...)
	}
	return out
}

// linkRow is one transcript row in the flattened run linkAt searches.
type linkRow struct {
	start int  // offset of its text in the run
	wraps bool // it ends where a renderer breaks a long token
}

// wrapBreaks are the characters glamour and ansi.Wrap may end a row on when
// they split a token that has no spaces.
const wrapBreaks = ",.;-+|"

// continuesLink reports whether a row's first word reads like the rest of
// a link rather than prose.
func continuesLink(body string) bool {
	w := strings.TrimRight(urlTailRe.FindString(body), ".,;:!?)")
	return strings.ContainsAny(w, "/.=?&%#")
}

// linkAt returns the link under a transcript cell, or "". unsure is set
// when the rows cannot settle where the target ends: it may continue on
// the next row.
func (m *Model) linkAt(line, col int) (link string, unsure bool) {
	lines := m.contentLines()
	if line < 0 || line >= len(lines) {
		return "", false
	}
	// Flatten the rows around the click into one run — margins and
	// padding dropped, blank rows kept as breaks — so a wrapped link reads
	// whole.
	var flat strings.Builder
	var rows []linkRow
	at := -1 // byte offset of the clicked cell
	for i := max(0, line-linkWrapRows); i <= min(len(lines)-1, line+linkWrapRows); i++ {
		text := strings.TrimRight(ansi.Strip(lines[i]), " ")
		body := strings.TrimLeft(text, " ")
		// A row is a wrap point only where a renderer would have broken a
		// token: on a break character, or hard against the right edge.
		// Rows that merely sit under each other never fuse into a link.
		row := linkRow{start: flat.Len()}
		if n := len(body); n > 0 {
			row.wraps = strings.IndexByte(wrapBreaks, body[n-1]) >= 0 || ansi.StringWidth(text) >= m.vp.Width-6
		}
		rows = append(rows, row)
		if i == line {
			lead := len(text) - len(body)
			if col < lead {
				return "", false // the margin
			}
			off := len(ansi.Cut(body, 0, col-lead))
			if off >= len(body) {
				return "", false // past the end of the row
			}
			at = flat.Len() + off
		}
		if body == "" {
			flat.WriteByte('\n')
			continue
		}
		flat.WriteString(body)
	}
	run := flat.String()
	rowOf := func(off int) int { // the row a run offset sits on
		r := 0
		for r+1 < len(rows) && rows[r+1].start <= off {
			r++
		}
		return r
	}
	rowEnd := func(r int) int {
		if r+1 < len(rows) {
			return rows[r+1].start
		}
		return len(run)
	}
	best := ""
	for _, u := range m.linkSources(line) {
		if len(u) <= len(best) {
			continue
		}
	occurrences:
		for from := 0; from < len(run); {
			i := strings.Index(run[from:], u)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(u)
			from = start + 1
			if at < start || at >= end {
				continue
			}
			first, last := rowOf(start), rowOf(end-1)
			for r := first; r < last; r++ {
				if !rows[r].wraps {
					continue occurrences // two rows, not one wrapped link
				}
			}
			// The rendered link must end where the source one does: more
			// link characters on the same row mean a longer link than u.
			tail := urlTailRe.FindString(run[end:rowEnd(last)])
			if cleanURL(u+tail) != u {
				continue
			}
			// It runs to a wrap point with link-like text right below: the
			// visible target may be longer than what the source vouches for.
			unsure = end+len(tail) == rowEnd(last) && rows[last].wraps &&
				last+1 < len(rows) && continuesLink(run[rows[last+1].start:rowEnd(last+1)])
			best = u
			break
		}
	}
	if best != "" {
		return best, unsure
	}
	// No source vouches for it (notices, typed tool views): take the link
	// as the clicked row shows it, unless that row cut it short.
	text := strings.TrimRight(ansi.Strip(lines[line]), " ")
	for _, loc := range urlRe.FindAllStringIndex(text, -1) {
		u := cleanURL(text[loc[0]:loc[1]])
		c0 := ansi.StringWidth(text[:loc[0]])
		if u == "" || col < c0 || col >= c0+ansi.StringWidth(u) {
			continue
		}
		if strings.HasPrefix(text[loc[1]:], "…") {
			return "", false // a truncated preview is not the link
		}
		// It runs to the row's end with text right below: maybe a wrap.
		more := line+1 < len(lines) && strings.TrimSpace(ansi.Strip(lines[line+1])) != ""
		return u, loc[1] == len(text) && more
	}
	return "", false
}

// linkPrompt is the pending open-link confirmation.
type linkPrompt struct {
	url      string
	host     string
	warn     []string // what about the target deserves a second look
	copyOnly string   // why this session cannot open a browser ("" = it can)
}

// openLinkPrompt raises the confirmation window for a link. unsure marks a
// target read off a single row that may have wrapped it (see linkAt).
func (m *Model) openLinkPrompt(u string, unsure bool) {
	p := &linkPrompt{url: u}
	if unsure {
		p.warn = append(p.warn, "may continue on the next row — check it")
	}
	if pu, err := url.Parse(u); err == nil {
		p.host = pu.Host
		host := pu.Hostname()
		if pu.User != nil {
			p.warn = append(p.warn, "sign-in name before the host — check the host")
		}
		if strings.Contains(host, "xn--") || strings.ContainsFunc(host, func(r rune) bool { return r > unicode.MaxASCII }) {
			p.warn = append(p.warn, "host uses lookalike-prone characters")
		}
		if ipHost(host) {
			p.warn = append(p.warn, "host is a raw IP address")
		}
		if pu.Scheme == "http" {
			p.warn = append(p.warn, "not encrypted (http)")
		}
	}
	_, p.copyOnly = linkOpener()
	m.link = p
}

// ipHost reports a host that is an address, not a name — including the
// bare-number forms browsers still resolve (127.1, 2130706433, 0x7f000001).
func ipHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if _, err := strconv.ParseUint(label, 0, 64); err != nil {
			return false
		}
	}
	return true
}

// linkCramped reports a terminal too short to show the host, every
// warning, and the keys together. Nothing opens from a window that cannot
// show what it is asking: ⏎ copies there instead.
func (m *Model) linkCramped(l *linkPrompt) bool {
	return m.height-2 < 2+len(l.warn)
}

// linkOpener names the helper that hands a URL to the desktop browser, or
// says why this session has none: over SSH the browser lives on the
// operator's machine, out of reach.
func linkOpener() (tool, why string) {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return "", "remote session — no browser here"
	}
	name := browserArgv(runtime.GOOS, "")[0]
	path, err := lookPath(name)
	if err != nil {
		return "", "no browser launcher (" + name + ") found"
	}
	return path, ""
}

// browserArgv is the command that hands u to the desktop browser. The link
// is one argv element, never a shell word: nothing in it can be read as a
// command, and it starts with "http", so never as a flag.
func browserArgv(goos, u string) []string {
	switch goos {
	case "darwin":
		return []string{"open", u}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", u}
	}
	return []string{"xdg-open", u}
}

// lookPath and launchURL are seams: tests must never start a browser.
var (
	lookPath  = osexec.LookPath
	launchURL = func(u string) error {
		tool, why := linkOpener()
		if tool == "" {
			return errors.New(why)
		}
		c := osexec.Command(tool, browserArgv(runtime.GOOS, u)[1:]...)
		if err := c.Start(); err != nil {
			return err
		}
		go func() { _ = c.Wait() }() // reap; the launcher may outlive the click
		return nil
	}
)

// linkOpenedMsg reports the browser launch.
type linkOpenedMsg struct{ err error }

// handleLinkKey answers the confirmation window: ⏎ opens (or copies when
// no browser is reachable), c copies, anything else cancels.
func (m *Model) handleLinkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	l := m.link
	m.link = nil
	switch msg.String() {
	case "enter":
		if l.copyOnly != "" || m.linkCramped(l) {
			return m, m.copyText(l.url)
		}
		u := l.url
		return m, tea.Batch(m.transientNoteCmd("opening link in browser"),
			func() tea.Msg { return linkOpenedMsg{err: launchURL(u)} })
	case "c", "C":
		return m, m.copyText(l.url)
	case "ctrl+c":
		return m, m.armConfirm(confirmQuit, "bodek")
	case "esc":
		return m, nil
	}
	// Like the confirm gates: a cancelling printable key still types.
	return m.typeThrough(msg)
}

// linkDialog renders the confirmation window. Every row is pre-fitted to
// the box so lipgloss never reflows it. The host, every warning, and the
// keys always show; the title, the spacer, and the tail of a long target
// give way on a short terminal.
func (m *Model) linkDialog() string {
	th := m.th
	l := m.link
	boxW := max(min(m.width-2, linkDialogMax), 12)
	inner := boxW - 4
	// A long host keeps its tail: the registrable domain is what matters.
	host := visibleInvocation(l.host)
	if room := max(inner-5, 1); ansi.StringWidth(host) > room {
		host = ansi.TruncateLeft(host, ansi.StringWidth(host)-room+1, "…")
	}
	must := []string{th.acDim.Render("host ") + th.acSel.Render(host)}
	for _, w := range l.warn {
		must = append(must, th.badgeDanger.Render(truncate("⚠ "+w, inner)))
	}
	// Key hints, widest that fits: the exit stays visible at any width.
	hints := []string{"⏎ open in browser · c copy · esc cancel", "⏎ open · c copy · esc cancel", "⏎ open · esc"}
	reason := l.copyOnly
	if reason == "" && m.linkCramped(l) {
		reason = "terminal too short to review"
	}
	if reason != "" {
		hints = []string{"⏎ copy link · esc cancel", "⏎ copy · esc"}
	}
	hint := hints[len(hints)-1]
	for _, h := range hints {
		if ansi.StringWidth(h) <= inner {
			hint = h
			break
		}
	}
	room := m.height - 2 - len(must) - 1 // rows left once the essentials are in
	var rows []string
	if room > 1 {
		rows = append(rows, th.acTitle.Render("open link?"))
		room--
	}
	rows = append(rows, must[0])
	spacer := room > 2
	if spacer {
		room--
	}
	if reason != "" && room > 1 {
		room--
	} else {
		reason = "" // the ⏎ hint already says copy
	}
	// The whole target, wrapped by cells. A terminal too short for all of
	// it keeps the head and says how much is hidden — never a silent cut.
	urlRows := strings.Split(ansi.Hardwrap(visibleInvocation(l.url), inner, true), "\n")
	if room < 1 {
		urlRows = nil
	} else if len(urlRows) > room {
		hidden := 0
		for _, r := range urlRows[room-1:] {
			hidden += ansi.StringWidth(r)
		}
		urlRows = append(urlRows[:room-1], truncate(fmt.Sprintf("… +%d more · c copies all", hidden), inner))
	}
	for _, r := range urlRows {
		rows = append(rows, th.acItem.Render(r))
	}
	rows = append(rows, must[1:]...)
	if reason != "" {
		rows = append(rows, th.acDim.Render(truncate(reason, inner)))
	}
	if spacer {
		rows = append(rows, "")
	}
	rows = append(rows, th.acDim.Render(truncate(hint, inner)))
	return th.acBox.Width(boxW - 2).Render(strings.Join(rows, "\n"))
}

// linkOverlay centres the confirmation window over the composed frame.
// An overlay, not a card in the layout: the transcript must not shift
// under the link that was just clicked.
func (m *Model) linkOverlay(frame string) string {
	if m.link == nil {
		return frame
	}
	box := strings.Split(m.linkDialog(), "\n")
	rows := strings.Split(frame, "\n")
	boxW := 0
	for _, r := range box {
		boxW = max(boxW, ansi.StringWidth(r))
	}
	x0 := max((m.width-boxW)/2, 0)
	y0 := max((len(rows)-len(box))/2, 0)
	for i, b := range box {
		y := y0 + i
		if y >= len(rows) {
			break
		}
		left := ansi.Cut(rows[y], 0, x0)
		if pad := x0 - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		row := left + selOff + b + selOff
		if w := ansi.StringWidth(rows[y]); x0+boxW < w {
			row += ansi.Cut(rows[y], x0+boxW, w)
		}
		rows[y] = row
	}
	return strings.Join(rows, "\n")
}
