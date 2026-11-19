package tui

import (
	"errors"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/BackendStack21/bodek/internal/client"
)

// localBrowser makes the session look local with a launcher on PATH, and
// records what would have been opened instead of starting anything.
func localBrowser(t *testing.T) *[]string {
	t.Helper()
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	var opened []string
	oldLook, oldLaunch := lookPath, launchURL
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	launchURL = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { lookPath, launchURL = oldLook, oldLaunch })
	return &opened
}

// linkOf is the link under a transcript cell, without the wrap caveat.
func linkOf(m *Model, line, col int) string {
	u, _ := m.linkAt(line, col)
	return u
}

// linkModel renders msgs at the given width and returns the model.
func linkModel(t *testing.T, width int, msgs ...message) *Model {
	t.Helper()
	m := newTestModel()
	m.resize(width, 30)
	m.msgs = append(m.msgs, msgs...)
	for i := range m.msgs {
		if m.msgs[i].role == roleAsst && m.msgs[i].rendered == "" {
			m.msgs[i].rendered = m.render(m.msgs[i].content)
		}
	}
	m.refresh()
	m.vp.GotoTop()
	return m
}

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/a.":                             "https://example.com/a",
		"https://example.com/a),":                            "https://example.com/a",
		"https://en.wikipedia.org/wiki/Go_(lang)":            "https://en.wikipedia.org/wiki/Go_(lang)",
		"https://example.com/a**":                            "https://example.com/a",
		"https://example.com/x]":                             "https://example.com/x",
		"http://localhost:8080/health?x=1&y=2":               "http://localhost:8080/health?x=1&y=2",
		"https://":                                           "",
		"https://exa\u202emple.com/":                         "", // direction override survives no sanitize → refused
		"https://" + strings.Repeat("a", maxURLLen) + ".com": "",
		"https://%zz":                                        "",
	} {
		if got := cleanURL(in); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkAtFindsLinkUnderTheCell(t *testing.T) {
	m := linkModel(t, 100, message{role: roleUser,
		content: "see https://example.com/docs. and (https://other.example/x) done"})
	line, col := findCell(t, m, "https://example.com")
	if got := linkOf(m, line, col+4); got != "https://example.com/docs" {
		t.Errorf("linkAt = %q, want the link without the sentence period", got)
	}
	if got := linkOf(m, line, col+len("https://example.com/docs")); got != "" {
		t.Errorf("the period after a link is not the link: %q", got)
	}
	if got := linkOf(m, line, col-2); got != "" {
		t.Errorf("prose before a link matched: %q", got)
	}
	_, col2 := findCell(t, m, "https://other.example")
	if got := linkOf(m, line, col2); got != "https://other.example/x" {
		t.Errorf("linkAt = %q, want the parenthesised link bare", got)
	}
	for _, miss := range [][2]int{{-1, 0}, {9999, 0}, {line, 0}, {line, 99}} {
		if got := linkOf(m, miss[0], miss[1]); got != "" {
			t.Errorf("linkAt%v = %q, want none", miss, got)
		}
	}
}

// Glamour breaks a long link at "." and "-": the first row alone reads
// "https://example." — a valid URL to the wrong host. Every row of the
// wrapped link must resolve to the whole link from the source.
func TestLinkAtRejoinsWrappedLinks(t *testing.T) {
	const long = "https://example.com/a/very/long/path/that-keeps-going/and-going/until-it-wraps/index.html?query=1&other=2"
	m := linkModel(t, 50,
		message{role: roleUser, content: "look at " + long + " please"},
		message{role: roleAsst, content: "See [the docs](" + long + ") and https://short.example/x. Bye."})
	hits := 0
	for i, l := range m.contentLines() {
		p := strings.TrimRight(ansi.Strip(l), " ")
		for _, frag := range []string{"https://example.", "going", "index.html", "long/path"} {
			j := strings.Index(p, frag)
			if j < 0 {
				continue
			}
			hits++
			if got := linkOf(m, i, ansi.StringWidth(p[:j])); got != long {
				t.Errorf("row %d %q: linkAt on %q = %q, want the whole link", i, p, frag, got)
			}
		}
	}
	if hits < 6 {
		t.Fatalf("the link did not wrap as expected (%d fragment hits):\n%s", hits, plain(m.vpContent))
	}
	line, col := findCell(t, m, "https://short.example")
	if got := linkOf(m, line, col+3); got != "https://short.example/x" {
		t.Errorf("short link next to the wrapped one = %q", got)
	}
	if line, col := findCell(t, m, "please"); linkOf(m, line, col) != "" {
		t.Error("prose after a wrapped link resolved to the link")
	}
}

func TestLinkAtRefusesTruncatedPreviews(t *testing.T) {
	m := newTestModel()
	m.resize(60, 30)
	// A notice has no message source: the row is all there is, and a row
	// that cut the link short must not offer the stump.
	m.vpContent, m.vpLines = "· fetched https://example.com/a/very/long/pa… ok\n· see https://example.com/whole now", nil
	if got := linkOf(m, 0, 14); got != "" {
		t.Errorf("a truncated link was offered: %q", got)
	}
	if got := linkOf(m, 1, 10); got != "https://example.com/whole" {
		t.Errorf("an unsourced whole link = %q", got)
	}
	if _, unsure := m.linkAt(1, 10); unsure {
		t.Error("a link with prose after it on the row cannot be a wrap")
	}
	// An unsourced link that runs to the row's end with text right below
	// may have been wrapped: it is offered, flagged.
	localBrowser(t)
	m.vpContent, m.vpLines = "· docs at https://example.\n  com/guide/start\n\n· and https://example.org/done\n", nil
	if u, unsure := m.linkAt(0, 12); u != "https://example" || !unsure {
		t.Fatalf("possibly wrapped link = %q unsure=%v", u, unsure)
	}
	if u, unsure := m.linkAt(3, 8); u != "https://example.org/done" || unsure {
		t.Fatalf("link before a blank row = %q unsure=%v", u, unsure)
	}
	m.vp.SetContent(m.vpContent)
	clickAt(m, 12, headerHeight)
	if m.link == nil || !strings.Contains(strings.Join(m.link.warn, "|"), "may continue on the next row") {
		t.Fatalf("the prompt does not flag a possibly wrapped target: %+v", m.link)
	}
}

// Encoders escape "&" and "/" inside JSON strings: a tool result's link
// still vouches for the decoded text the transcript shows.
func TestLinkSourcesReadThroughJSONEscapes(t *testing.T) {
	const long = "https://example.com/search/with-a-long-path/that-wraps?first=1&second=2"
	m := linkModel(t, 44, message{role: roleAsst, content: "x",
		rendered: "  https://example.com/search/with-a-long-\n  path/that-wraps?first=1&second=2",
		steps:    []step{{name: "http_request", result: `{"url":"https:\/\/example.com\/search\/with-a-long-path\/that-wraps?first=1\u0026second=2"}`}},
		items:    []turnItem{{stepIdx: 0}},
	})
	line, col := findCell(t, m, "path/that-wraps")
	if u, unsure := m.linkAt(line, col); u != long || unsure {
		t.Fatalf("linkAt = %q unsure=%v, want the whole link from the JSON result", u, unsure)
	}
}

// A shorter source link must not claim the cells of a longer rendered one.
func TestLinkAtSourcePrefixDoesNotShadowLongerLink(t *testing.T) {
	m := linkModel(t, 100, message{role: roleAsst,
		content: "x", rendered: "  go to https://example.com/deeper/path now",
		steps: []step{{name: "http_request", result: "https://example.com"}},
		items: []turnItem{{stepIdx: 0}},
	})
	line, col := findCell(t, m, "https://example.com/deeper")
	if got := linkOf(m, line, col+3); got != "https://example.com/deeper/path" {
		t.Errorf("linkAt = %q, want the link as rendered", got)
	}
}

func TestClickLinkOpensConfirmationThenBrowser(t *testing.T) {
	opened := localBrowser(t)
	m := linkModel(t, 100, message{role: roleAsst, content: "Read https://example.com/docs first."})
	line, col := findCell(t, m, "https://example.com")
	before := m.vp.YOffset

	clickAt(m, col+2, headerHeight+line)
	if m.link == nil || m.link.url != "https://example.com/docs" {
		t.Fatalf("click on a link did not raise the prompt: %+v", m.link)
	}
	if len(*opened) != 0 {
		t.Fatal("the link opened without confirmation")
	}
	if m.copyFlashing() {
		t.Error("a link click must not also copy the card")
	}
	view := plain(m.View())
	for _, want := range []string{"open link?", "host example.com", "https://example.com/docs", "⏎ open in browser", "esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation window missing %q:\n%s", want, view)
		}
	}
	if m.vp.YOffset != before || lineCount(m.View()) != m.height {
		t.Error("the window must overlay the frame, not shift the layout")
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "link") || !strings.Contains(foot, "⏎ open") {
		t.Errorf("footer does not teach the prompt: %q", foot)
	}

	_, cmd := m.Update(key("enter"))
	if m.link != nil {
		t.Fatal("enter left the prompt open")
	}
	runCmds(t, m, cmd)
	if len(*opened) != 1 || (*opened)[0] != "https://example.com/docs" {
		t.Fatalf("opened = %v, want the confirmed link", *opened)
	}
}

// runCmds executes a command tree far enough to fire the link launch,
// feeding its result back into the model.
func runCmds(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if c != nil {
				if out, ok := c().(linkOpenedMsg); ok {
					m.Update(out)
				}
			}
		}
	case linkOpenedMsg:
		m.Update(msg)
	}
}

func TestLinkPromptKeys(t *testing.T) {
	opened := localBrowser(t)
	open := func() *Model {
		m := linkModel(t, 100, message{role: roleAsst, content: "Read https://example.com/docs first."})
		line, col := findCell(t, m, "https://example.com")
		clickAt(m, col, headerHeight+line)
		if m.link == nil {
			t.Fatal("prompt did not open")
		}
		return m
	}

	m := open()
	m.busy = true
	m.Update(key("esc"))
	if m.link != nil || m.confirm != confirmNone {
		t.Fatalf("esc must close the prompt and nothing else: link=%v confirm=%v", m.link, m.confirm)
	}

	m = open()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.link != nil || !m.copyFlashing() {
		t.Fatal("c did not copy the link")
	}

	// Any other key cancels — and a typed character still types.
	m = open()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.link != nil || m.ta.Value() != "h" {
		t.Fatalf("a typed key should cancel and type: link=%v draft=%q", m.link, m.ta.Value())
	}

	m = open()
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.link != nil || m.confirm != confirmQuit {
		t.Fatal("^C should close the prompt and arm the quit gate")
	}

	// A pending approval never takes a key the prompt cancelled on.
	m = open()
	m.approvals = []client.Event{{Type: "approval_request", ID: "ap1"}}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.link != nil || len(m.approvals) != 1 {
		t.Fatal("the cancelling key leaked into the approval card")
	}

	if len(*opened) != 0 {
		t.Fatalf("something opened without ⏎: %v", *opened)
	}
}

func TestLinkPromptClickAwayAndDoubleClick(t *testing.T) {
	localBrowser(t)
	t.Setenv("SSH_TTY", "/dev/ttys001") // copies ride OSC 52
	m := linkModel(t, 100, message{role: roleAsst, content: "Read https://example.com/docs first."})
	line, col := findCell(t, m, "https://example.com")
	y := headerHeight + line

	clickAt(m, col+2, y)
	if m.link == nil {
		t.Fatal("prompt did not open")
	}
	// A click elsewhere only dismisses: it must not act on what is under it.
	clickAt(m, 0, headerHeight+m.turnLineIndex[0].line)
	if m.link != nil || m.msgs[0].collapsed {
		t.Fatalf("click-away: link=%v collapsed=%v", m.link, m.msgs[0].collapsed)
	}

	// Double click on a link selects its text instead of prompting twice.
	clickAt(m, col+2, y)
	clickAt(m, col+2, y)
	if m.link != nil {
		t.Fatal("the second click of a double click re-opened the prompt")
	}
	if got := selText(m); got != "https://example.com/docs" {
		t.Fatalf("double click selected %q, want the link", got)
	}
}

func TestLinkPromptWarnsAboutDeceptiveTargets(t *testing.T) {
	localBrowser(t)
	m := newTestModel()
	for u, want := range map[string]string{
		"http://example.com/":               "not encrypted",
		"https://google.com@evil.example/x": "sign-in name before the host",
		"https://xn--pple-43d.com/":         "lookalike-prone",
		"https://аpple.com/":                "lookalike-prone", // Cyrillic а
		"https://192.168.0.1/admin":         "raw IP address",
		"http://[::1]:8080/":                "raw IP address",
	} {
		m.openLinkPrompt(u, false)
		if got := strings.Join(m.link.warn, " | "); !strings.Contains(got, want) {
			t.Errorf("%s: warnings %q, want %q", u, got, want)
		}
		if view := plain(m.View()); !strings.Contains(view, "⚠") {
			t.Errorf("%s: warning not shown:\n%s", u, view)
		}
	}
	m.openLinkPrompt("https://google.com@evil.example/x", false)
	if m.link.host != "evil.example" {
		t.Errorf("host = %q, want the real host behind the userinfo", m.link.host)
	}
	m.openLinkPrompt("https://example.com/plain", false)
	if len(m.link.warn) != 0 {
		t.Errorf("a plain https link warned: %v", m.link.warn)
	}
}

func TestLinkPromptCopiesWhenNoBrowserIsReachable(t *testing.T) {
	opened := localBrowser(t)
	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22")
	m := newTestModel()
	m.openLinkPrompt("https://example.com/x", false)
	if m.link.copyOnly == "" {
		t.Fatal("a remote session should fall back to copying")
	}
	view := plain(m.View())
	if !strings.Contains(view, "remote session") || !strings.Contains(view, "⏎ copy link") || strings.Contains(view, "open in browser") {
		t.Errorf("remote prompt:\n%s", view)
	}
	if foot := plain(m.footer()); !strings.Contains(foot, "⏎ copy link") {
		t.Errorf("remote footer: %q", foot)
	}
	m.Update(key("enter"))
	if len(*opened) != 0 || !m.copyFlashing() {
		t.Fatalf("remote ⏎ should copy, not open: opened=%v", *opened)
	}

	t.Setenv("SSH_CONNECTION", "")
	lookPath = func(string) (string, error) { return "", osexec.ErrNotFound }
	m.openLinkPrompt("https://example.com/x", false)
	if !strings.Contains(m.link.copyOnly, "no browser launcher") {
		t.Errorf("missing launcher: copyOnly = %q", m.link.copyOnly)
	}
}

func TestLinkLaunchFailureIsReported(t *testing.T) {
	localBrowser(t)
	launchURL = func(string) error { return errors.New("exec: no such file") }
	m := newTestModel()
	m.openLinkPrompt("https://example.com/x", false)
	_, cmd := m.Update(key("enter"))
	runCmds(t, m, cmd)
	if note, _ := lastNoteMatching(m, "could not open the link"); !strings.Contains(note, "no such file") {
		t.Fatalf("launch failure not reported: %v", m.notices)
	}
	// A clean launch posts nothing more.
	n := len(m.notices)
	m.Update(linkOpenedMsg{})
	if len(m.notices) != n {
		t.Errorf("a successful launch posted a note: %v", m.notices)
	}
}

func TestLaunchURLStartsTheLauncherWithTheLinkAsOneArg(t *testing.T) {
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	self, err := os.Executable()
	if err != nil {
		t.Skip("no test binary path")
	}
	oldLook := lookPath
	t.Cleanup(func() { lookPath = oldLook })
	// The test binary stands in for the launcher: with an argument that
	// matches no test it exits at once — nothing is ever opened.
	lookPath = func(string) (string, error) { return self, nil }
	if err := launchURL("https://example.com/x?a=1&b=2;rm -rf /"); err != nil {
		t.Fatalf("launchURL: %v", err)
	}
	lookPath = func(string) (string, error) { return "/nonexistent/launcher", nil }
	if err := launchURL("https://example.com/x"); err == nil {
		t.Error("a launcher that cannot start must report it")
	}
	lookPath = func(string) (string, error) { return "", osexec.ErrNotFound }
	if err := launchURL("https://example.com/x"); err == nil || !strings.Contains(err.Error(), "no browser launcher") {
		t.Errorf("missing launcher error = %v", err)
	}
	if got := browserArgv("darwin", "u"); got[0] != "open" || got[1] != "u" {
		t.Errorf("darwin argv = %v", got)
	}
	if got := browserArgv("windows", "u"); got[0] != "rundll32" || got[2] != "u" {
		t.Errorf("windows argv = %v", got)
	}
	if got := browserArgv("linux", "u"); got[0] != "xdg-open" || got[1] != "u" {
		t.Errorf("linux argv = %v", got)
	}
}

func TestLinkDialogFitsNarrowAndShortTerminals(t *testing.T) {
	localBrowser(t)
	long := "https://example.com/" + strings.Repeat("segment-", 60) + "end"
	for _, size := range [][2]int{{40, 12}, {40, 30}, {80, 24}, {120, 40}, {20, 10}} {
		m := newTestModel()
		m.resize(size[0], size[1])
		m.msgs = append(m.msgs, message{role: roleUser, content: "hi"})
		m.refresh()
		for _, u := range []string{long, "http://user@192.168.0.1/x"} {
			m.openLinkPrompt(u, false)
			box := m.linkDialog()
			for _, row := range strings.Split(box, "\n") {
				if w := ansi.StringWidth(row); w > size[0] {
					t.Errorf("%v: dialog row is %d cells wide: %q", size, w, plain(row))
				}
			}
			view := m.View()
			if n := lineCount(view); n != size[1] {
				t.Errorf("%v: view is %d rows with the dialog open", size, n)
			}
			for _, row := range strings.Split(view, "\n") {
				if w := ansi.StringWidth(row); w > size[0] {
					t.Errorf("%v: overlaid row is %d cells wide: %q", size, w, plain(row))
				}
			}
			if !strings.Contains(plain(box), "esc") {
				t.Errorf("%v: dialog lost its exit hint:\n%s", size, plain(box))
			}
		}
		m.openLinkPrompt(long, false)
		if size[0] >= 40 && size[1] <= 12 && !strings.Contains(plain(m.linkDialog()), "c copies all") {
			t.Errorf("%v: a clipped link must say so:\n%s", size, plain(m.linkDialog()))
		}
	}
}

func TestStepRowsKeepTheirClickOverLinks(t *testing.T) {
	localBrowser(t)
	m := linkModel(t, 100, message{role: roleAsst,
		steps: []step{{name: "http_request", arg: "https://example.com/api", done: true, result: "ok"}},
		items: []turnItem{{stepIdx: 0}},
	})
	line, col := findCell(t, m, "https://example.com/api")
	clickAt(m, col+2, headerHeight+line)
	if m.link != nil {
		t.Fatal("a step row's click must inspect the step, not prompt for its argument")
	}
	if !m.inspectChrome() {
		t.Fatal("the step row click did not open the inspector")
	}
}

func TestLinkAtIgnoresTheMargin(t *testing.T) {
	m := linkModel(t, 100, message{role: roleAsst, content: "https://example.com/first-thing"})
	line, col := findCell(t, m, "https://example.com")
	if col == 0 {
		t.Fatal("the answer card should indent its text")
	}
	if got := linkOf(m, line, 0); got != "" {
		t.Errorf("the card margin resolved to %q", got)
	}
}

func TestLinkOverlayPadsShortRows(t *testing.T) {
	localBrowser(t)
	m := newTestModel()
	m.openLinkPrompt("https://example.com/x", false)
	out := m.linkOverlay(strings.Repeat("x\n", 20))
	if !strings.Contains(plain(out), "open link?") {
		t.Fatalf("dialog missing over a short frame:\n%s", plain(out))
	}
	for _, row := range strings.Split(out, "\n") {
		if p := plain(row); strings.Contains(p, "╭") && !strings.HasPrefix(p, "x  ") {
			t.Errorf("the box is not padded out to its column: %q", p)
		}
	}
	// A frame shorter than the box keeps what fits.
	if got := m.linkOverlay("only one row"); lineCount(got) != 1 {
		t.Errorf("overlay grew a one-row frame to %d rows", lineCount(got))
	}
}

func TestLinkPromptCancelTypesIntoClarifyAnswer(t *testing.T) {
	localBrowser(t)
	m := newTestModel()
	m.clarify = &client.Event{Type: "clarify_request", ID: "clr-1", Question: "q"}
	m.openLinkPrompt("https://example.com/x", false)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.link != nil || m.clarifyBuf != "n" || m.ta.Value() != "" {
		t.Fatalf("cancelling key: link=%v answer=%q draft=%q", m.link, m.clarifyBuf, m.ta.Value())
	}
}

// Two rows that merely sit under each other are not one wrapped link, even
// when text the reader cannot see (reasoning, tool output) holds their
// concatenation. Reply: "…at https://accounts.google.com" / ".evil.example/…".
func TestLinkAtDoesNotFuseRowsThatAreNotAWrap(t *testing.T) {
	const fused = "https://accounts.google.com.evil.example/login"
	m := linkModel(t, 100, message{role: roleAsst, content: "x",
		rendered: "  it looked like https://accounts.google.com\n  .evil.example/login was the page",
		thinking: "the page is " + fused,
	})
	line, col := findCell(t, m, "https://accounts")
	u, unsure := m.linkAt(line, col+5)
	if u == fused {
		t.Fatalf("a hidden source fused two displayed rows into %q", u)
	}
	if u != "https://accounts.google.com" || !unsure {
		t.Fatalf("linkAt = %q unsure=%v, want the displayed link, flagged", u, unsure)
	}
}

// A source can vouch for a prefix of what is displayed: when the link runs
// to a wrap point and link-like text follows, the prompt says so.
func TestLinkAtFlagsSourcePrefixBeforeAWrap(t *testing.T) {
	m := linkModel(t, 100, message{role: roleAsst, content: "x",
		rendered: "  https://evilcdn.net.\n  accounts.google.com/login",
		thinking: "fetch https://evilcdn.net first",
	})
	line, col := findCell(t, m, "https://evilcdn")
	if u, unsure := m.linkAt(line, col+3); u != "https://evilcdn.net" || !unsure {
		t.Fatalf("linkAt = %q unsure=%v, want the vouched prefix flagged as possibly longer", u, unsure)
	}
	// Prose below a link that ends a sentence is not a continuation.
	m = linkModel(t, 100, message{role: roleAsst, content: "x",
		rendered: "  read https://example.com/docs.\n  Then run the tests",
		thinking: "https://example.com/docs",
	})
	line, col = findCell(t, m, "https://example.com")
	if u, unsure := m.linkAt(line, col+3); u != "https://example.com/docs" || unsure {
		t.Fatalf("linkAt = %q unsure=%v, want the plain link without a caveat", u, unsure)
	}
}

func TestLinkPromptFlagsNumericHosts(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "2130706433": true, "0x7f000001": true, "127.1": true,
		"example.com": false, "localhost": false, "1password.com": false, "": false,
	} {
		if got := ipHost(host); got != want {
			t.Errorf("ipHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// The registrable domain sits at the end of a host: a long one keeps its
// tail, never "accounts.google.com.aaaa…".
func TestLinkDialogKeepsTheTailOfALongHost(t *testing.T) {
	localBrowser(t)
	m := newTestModel()
	m.resize(80, 30)
	m.openLinkPrompt("https://accounts.google.com."+strings.Repeat("a", 60)+".evil.example/login", false)
	box := plain(m.linkDialog())
	if row := strings.Split(box, "\n")[2]; !strings.Contains(row, "host …") || !strings.Contains(row, "evil.example") {
		t.Fatalf("a long host must be cut at its head and keep the registrable domain:\n%s", box)
	}
}

// A window that cannot show the host, every warning and the keys must not
// open anything: it says so and ⏎ copies.
func TestLinkDialogNeverHidesWarningsOrKeys(t *testing.T) {
	opened := localBrowser(t)
	const target = "http://user@1.2.3.4/a/long/path/that/needs/several/rows/to/show/in/full"
	for h := 4; h <= 16; h++ {
		m := newTestModel()
		m.resize(60, h)
		m.openLinkPrompt(target, true)
		if len(m.link.warn) != 4 {
			t.Fatalf("fixture should carry four warnings: %v", m.link.warn)
		}
		box := plain(m.linkDialog())
		cramped := m.linkCramped(m.link)
		if !cramped {
			if lineCount(box) > h {
				t.Errorf("height %d: dialog is %d rows", h, lineCount(box))
			}
			if n := strings.Count(box, "⚠"); n != 4 {
				t.Errorf("height %d: %d of 4 warnings shown:\n%s", h, n, box)
			}
			if !strings.Contains(box, "host 1.2.3.4") || !strings.Contains(box, "⏎ open") {
				t.Errorf("height %d: host or keys missing:\n%s", h, box)
			}
		} else if !strings.Contains(box, "⏎ copy") {
			t.Errorf("height %d: a cramped window must offer copy, not open:\n%s", h, box)
		}
		m.Update(key("enter"))
		if cramped && (len(*opened) != 0 || !m.copyFlashing()) {
			t.Fatalf("height %d: ⏎ opened a link the window could not show: %v", h, *opened)
		}
		*opened = nil
	}
}
