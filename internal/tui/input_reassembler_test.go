package tui

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
)

// replayReader replays fixed chunks, one per Read, then either blocks (until
// the test releases it) or returns EOF. A non-zero gap delays every chunk so
// the reassembler's wait paths are exercised deterministically.
type replayReader struct {
	chunks [][]byte
	gap    time.Duration
	block  chan struct{}
	i      int
}

func (r *replayReader) Read(p []byte) (int, error) {
	for r.i < len(r.chunks) && len(r.chunks[r.i]) == 0 {
		r.i++
	}
	if r.i >= len(r.chunks) {
		if r.block != nil {
			<-r.block
		}
		return 0, io.EOF
	}
	if r.gap > 0 {
		time.Sleep(r.gap)
	}
	n := copy(p, r.chunks[r.i])
	r.chunks[r.i] = r.chunks[r.i][n:]
	return n, nil
}

// splitEverywhere returns every way of cutting s into n contiguous chunks.
func splitEverywhere(s string, n int) [][]string {
	var out [][]string
	var rec func(start int, parts []string)
	rec = func(start int, parts []string) {
		if len(parts) == n-1 {
			out = append(out, append(append([]string(nil), parts...), s[start:]))
			return
		}
		for cut := start + 1; cut < len(s); cut++ {
			rec(cut, append(parts, s[start:cut]))
		}
	}
	rec(0, nil)
	return out
}

// drain reassembles everything the reader can produce, with the given wait
// budgets. Bytes are compared as a stream, so a correct implementation must
// never reorder, duplicate, or drop input.
func drain(t *testing.T, chunks []string, gap, settle, abandon time.Duration) []byte {
	t.Helper()
	parts := make([][]byte, 0, len(chunks))
	for _, c := range chunks {
		parts = append(parts, []byte(c))
	}
	src := &replayReader{chunks: parts, gap: gap}
	out, err := io.ReadAll(newInputReassembler(src, settle, abandon))
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return out
}

func drainString(t *testing.T, chunks []string, gap, settle, abandon time.Duration) string {
	t.Helper()
	return string(drain(t, chunks, gap, settle, abandon))
}

// mouseReports is every SGR shape bodek can receive: wheel up/down, button
// press/release, drag motion, and modifier-carrying buttons. SGR is the form
// the terminal uses once mode 1006 is on, which bodek always enables.
var mouseReports = []string{
	"\x1b[<65;75;25M",   // wheel down
	"\x1b[<64;75;25M",   // wheel up
	"\x1b[<0;75;25M",    // left press
	"\x1b[<0;75;25m",    // left release
	"\x1b[<32;10;8m",    // drag motion
	"\x1b[<16;10;8M",    // ctrl+left
	"\x1b[<4;3;4M",      // shift+left
	"\x1b[<1;1;1M",      // minimal coordinates
	"\x1b[<64;1;1m",     // wheel release (Windows Terminal reports this)
	"\x1b[<65;300;120M", // wide coordinates
}

// ── byte-level reassembly: every fragmentation of every report ─────────────

func TestAssembleInputJoinsEveryTwoWaySplit(t *testing.T) {
	// Tests run with a settle window long enough that a split can only be
	// resolved by holding the head and waiting for the tail — never by the
	// timer — so the assertion is deterministic under -race.
	const settle, abandon = 5 * time.Second, 5 * time.Second
	for _, report := range mouseReports {
		for _, parts := range splitEverywhere(report, 2) {
			if got := drainString(t, parts, 0, settle, abandon); got != report {
				t.Fatalf("report %q split %q → %q, want the original stream", report, parts, got)
			}
		}
	}
}

func TestAssembleInputJoinsEveryThreeWaySplit(t *testing.T) {
	const settle, abandon = 5 * time.Second, 5 * time.Second
	report := "\x1b[<65;75;25M"
	for _, parts := range splitEverywhere(report, 3) {
		if got := drainString(t, parts, 0, settle, abandon); got != report {
			t.Fatalf("three-way split %q → %q, want %q", parts, got, report)
		}
	}
}

func TestAssembleInputJoinsWheelBursts(t *testing.T) {
	const settle, abandon = 5 * time.Second, 5 * time.Second
	burst := strings.Repeat("\x1b[<65;75;25M", 12)
	// Cut at every offset near both ends, and on a coarse grid in between:
	// the interesting boundaries are the sequence starts, not every byte.
	offsets := map[int]bool{}
	for i := 1; i < len(burst); i++ {
		if i <= 20 || i%7 == 0 || i > len(burst)-20 {
			offsets[i] = true
		}
	}
	for cut := range offsets {
		parts := []string{burst[:cut], burst[cut:]}
		if got := drainString(t, parts, 0, settle, abandon); got != burst {
			t.Fatalf("burst cut at %d → %q, want %q", cut, got, burst)
		}
	}
}

func TestAssembleInputHoldsHeadUntilTailArrives(t *testing.T) {
	// A tail that arrives well after the settle window must still complete
	// the report instead of tearing it into garbage.
	report := "\x1b[<65;75;25M"
	got := drainString(t, []string{report[:8], report[8:]}, 50*time.Millisecond, 10*time.Millisecond, time.Second)
	if got != report {
		t.Fatalf("delayed tail → %q, want %q", got, report)
	}
}

func TestAssembleInputJoinsSplitX10Report(t *testing.T) {
	// The legacy X10 form (ESC [ M + three bytes) is rare once mode 1006 is
	// on, but it must still be joined across reads rather than torn apart.
	report := "\x1b[M" + " !!"
	const settle, abandon = 5 * time.Second, 5 * time.Second
	for _, parts := range splitEverywhere(report, 2) {
		if got := drainString(t, parts, 0, settle, abandon); got != report {
			t.Fatalf("X10 split %q → %q, want %q", parts, got, report)
		}
	}
}

func TestAssembleInputFlushesTruncatedX10Head(t *testing.T) {
	// An unterminated X10 head is not dropped: its coordinate bytes are
	// indistinguishable from typed text, so it is flushed verbatim once the
	// settle window lapses and the following input keeps flowing.
	src := &replayReader{
		chunks: [][]byte{[]byte("\x1b[M"), []byte("ok")},
		gap:    20 * time.Millisecond,
		block:  make(chan struct{}),
	}
	re := newInputReassembler(src, 5*time.Millisecond, time.Second)
	first, ok := readWithin(t, re, 300*time.Millisecond)
	if !ok || string(first) != "\x1b[M" {
		t.Fatalf("truncated X10 head → %q (ok=%v), want it flushed verbatim", first, ok)
	}
	second, ok := readWithin(t, re, 300*time.Millisecond)
	close(src.block)
	if !ok || string(second) != "ok" {
		t.Fatalf("input after a truncated X10 head → %q (ok=%v), want %q", second, ok, "ok")
	}
}

func TestAssembleInputSplitReportWithTrailingTyping(t *testing.T) {
	// The read that completes a report can carry typing behind it. The report
	// stays intact — Bubble Tea needs it to emit a mouse event and parses the
	// typing behind it from the same read.
	const settle, abandon = 5 * time.Second, 5 * time.Second
	if got := drainString(t, []string{"\x1b[<65;75;25", "Mhello"}, 0, settle, abandon); got != "\x1b[<65;75;25Mhello" {
		t.Fatalf("report completed by a read with typing → %q, want the report and the typing intact", got)
	}
	// A read that cannot belong to the report is user input, not a tail: the
	// truncated head is dropped and the read survives.
	if got := drainString(t, []string{"\x1b[<65;75", "boo"}, 0, settle, abandon); got != "boo" {
		t.Fatalf("uncontinuable read → %q, want %q", got, "boo")
	}
	// A second head in the same read replaces the stalled one.
	if got := drainString(t, []string{"\x1b[<65;75", "\x1b[<64;10;10M"}, 0, settle, abandon); got != "\x1b[<64;10;10M" {
		t.Fatalf("restarted report → %q, want the fresh report", got)
	}
}

func TestAssembleInputNeverLeaksAStalledReport(t *testing.T) {
	// A stalled report is resolved one of two ways, and neither may type
	// garbage into the composer:
	//   - a read that cannot belong to a report proves it was truncated, so
	//     the head is dropped and the read survives;
	//   - a read made only of digits and semicolons is indistinguishable
	//     from the rest of the report, so it is joined — and if the tail
	//     never comes, the whole head is dropped rather than echoed.
	// The reader must stay usable either way: the next keystroke arrives.
	cases := []struct{ tail, want string }{
		{"boo", "boo"}, // not report bytes: user input survives
		{"0", ""},      // ambiguous: never echoed
		{";", ""},
		{"123", ""},
	}
	for _, tc := range cases {
		src := &replayReader{
			chunks: [][]byte{[]byte("\x1b[<65;75"), []byte(tc.tail), []byte("!")},
			block:  make(chan struct{}),
		}
		re := newInputReassembler(src, 5*time.Millisecond, 30*time.Millisecond)
		got := drainWithin(t, re, len(tc.want)+1, 2*time.Second)
		close(src.block)
		if got != tc.want+"!" {
			t.Fatalf("stalled head then %q → %q, want %q plus the next keystroke", tc.tail, got, tc.want)
		}
	}
}

func TestAssembleInputDropsMouseHeadAtEOF(t *testing.T) {
	// End of input with a mouse head pending: the head is a truncated report
	// and must not be flushed into the composer.
	for _, head := range mouseReports {
		got := drainString(t, []string{head[:8]}, 0, 5*time.Millisecond, 20*time.Millisecond)
		if got != "" {
			t.Fatalf("head %q flushed at EOF as %q, want nothing", head[:8], got)
		}
	}
}

func TestAssembleInputJoinsSplitSS3(t *testing.T) {
	// Application cursor keys and F1-F4 arrive as ESC O plus one byte, which
	// splits into alt+O plus a stray letter if it is not held.
	const settle, abandon = 5 * time.Second, 5 * time.Second
	for _, seq := range []string{"\x1bOA", "\x1bOB", "\x1bOP", "\x1bOS"} {
		for _, parts := range splitEverywhere(seq, 2) {
			if got := drainString(t, parts, 0, settle, abandon); got != seq {
				t.Fatalf("SS3 %q split %q → %q, want %q", seq, parts, got, seq)
			}
		}
	}
}

func TestAssembleInputJoinsSplitStringSequences(t *testing.T) {
	// OSC replies (clipboard, title) end in BEL or ST and can split just as
	// easily as a mouse report.
	const settle, abandon = 5 * time.Second, 5 * time.Second
	for _, seq := range []string{
		"\x1b]0;title\x07",
		"\x1b]52;c;aGk=\x1b\\",
		"\x1bP1;2|data\x1b\\",
	} {
		for _, parts := range splitEverywhere(seq, 2) {
			if got := drainString(t, parts, 0, settle, abandon); got != seq {
				t.Fatalf("string sequence %q split %q → %q, want %q", seq, parts, got, seq)
			}
		}
	}
}

// ── input that must never be altered ──────────────────────────────────────

func TestAssembleInputLeavesTypingUntouched(t *testing.T) {
	const settle, abandon = 10 * time.Millisecond, 20 * time.Millisecond
	for _, typed := range []string{
		"hello world",
		"a<b;c>d",
		"1;2;3",
		"65;75;25M",
		"<65;75;25M",
		"M",
		"<",
		"[",
		"[[[[",
		"~",
		"3~",
		"27;2;13~",
		"path/to/file.go",
	} {
		if got := drainString(t, []string{typed}, 0, settle, abandon); got != typed {
			t.Fatalf("typing %q → %q, want it verbatim", typed, got)
		}
	}
}

func TestAssembleInputLeavesPasteUntouched(t *testing.T) {
	const settle, abandon = 10 * time.Millisecond, 20 * time.Millisecond
	paste := "\x1b[200~" + "a<\x1b[<65;75;25M" + "\nline2\x1b[201~"
	for _, parts := range splitEverywhere(paste, 2) {
		if got := drainString(t, parts, 0, settle, abandon); got != paste {
			t.Fatalf("paste split %q → %q, want it verbatim", parts, got)
		}
	}
	// A paste body that ends mid-report, with the tail arriving late.
	body := "\x1b[200~literal \x1b[<65;75;25M\x1b[201~"
	got := drainString(t, []string{body[:14], body[14:]}, 40*time.Millisecond, 5*time.Millisecond, 6*time.Millisecond)
	if got != body {
		t.Fatalf("delayed paste → %q, want %q", got, body)
	}
}

func TestAssembleInputReleasesLoneEscape(t *testing.T) {
	// A bare ESC keypress is user input: it must be released once the settle
	// window lapses, not swallowed.
	src := &replayReader{chunks: [][]byte{[]byte("\x1b")}, block: make(chan struct{})}
	defer close(src.block)
	got := firstRead(t, newInputReassembler(src, 20*time.Millisecond, time.Second), time.Second)
	if string(got) != "\x1b" {
		t.Fatalf("lone ESC → %q, want it released", got)
	}
}

func TestAssembleInputFlushesNonMouseHeads(t *testing.T) {
	// ESC[ and ESC[6 are not mouse reports: they must reach the terminal as
	// they are rather than being held or dropped.
	for _, head := range []string{"\x1b[", "\x1b[6", "\x1b[1;5", "\x1bO"} {
		src := &replayReader{chunks: [][]byte{[]byte(head)}, block: make(chan struct{})}
		got := firstRead(t, newInputReassembler(src, 20*time.Millisecond, time.Second), time.Second)
		close(src.block)
		if string(got) != head {
			t.Fatalf("incomplete head %q → %q, want it flushed verbatim", head, got)
		}
	}
}

func TestAssembleInputDropsTruncatedMouseHead(t *testing.T) {
	// A mouse head whose tail never arrives must be dropped, never echoed
	// into the composer as text — and the reader must stay usable, so the
	// input that follows still arrives.
	for _, head := range []string{"\x1b[<65;75", "\x1b[<", "\x1b[<65;"} {
		src := &replayReader{
			chunks: [][]byte{[]byte(head), []byte("ok")},
			block:  make(chan struct{}),
		}
		re := newInputReassembler(src, 5*time.Millisecond, 30*time.Millisecond)
		got, ok := readWithin(t, re, 300*time.Millisecond)
		close(src.block)
		if !ok {
			t.Fatalf("reader stalled after the truncated head %q", head)
		}
		if string(got) != "ok" {
			t.Fatalf("truncated mouse head %q leaked %q, want it dropped", head, got)
		}
	}
}

// ── end to end through Bubble Tea's real parser ───────────────────────────

// programSink records every message the program delivers.
type programSink struct {
	mu   sync.Mutex
	msgs []tea.Msg
	want int
}

func (s *programSink) Init() tea.Cmd { return nil }
func (s *programSink) View() string  { return "" }

func (s *programSink) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s.mu.Lock()
	s.msgs = append(s.msgs, msg)
	done := s.want > 0 && len(s.msgs) >= s.want
	s.mu.Unlock()
	if done {
		return s, tea.Quit
	}
	return s, nil
}

func (s *programSink) collected() []tea.Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tea.Msg(nil), s.msgs...)
}

// runProgram drives a real tea.Program over the given reader and returns the
// messages it produced. The reader stays open, so the only exit is the sink's
// own Quit or the budget.
//
// The reassembler is built directly with generous windows: these tests assert
// reassembly, not the settle timing, so they must not race a timer.
func runProgram(t *testing.T, src *replayReader, want int, budget time.Duration) []tea.Msg {
	t.Helper()
	sink := &programSink{want: want}
	p := tea.NewProgram(sink,
		tea.WithInput(newInputReassembler(src, 5*time.Second, 5*time.Second)),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithFilter(FilterShiftEnter),
	)
	timer := time.AfterFunc(budget, func() { p.Quit() })
	defer timer.Stop()
	if _, err := p.Run(); err != nil {
		t.Fatalf("program run: %v", err)
	}
	return sink.collected()
}

func keyMsgs(msgs []tea.Msg) []tea.KeyMsg {
	var out []tea.KeyMsg
	for _, m := range msgs {
		if km, ok := m.(tea.KeyMsg); ok {
			out = append(out, km)
		}
	}
	return out
}

func mouseMsgs(msgs []tea.Msg) []tea.MouseMsg {
	var out []tea.MouseMsg
	for _, m := range msgs {
		if mm, ok := m.(tea.MouseMsg); ok {
			out = append(out, mm)
		}
	}
	return out
}

func TestProgramParsesEverySplitReportAsMouse(t *testing.T) {
	// The byte-level matrix above is exhaustive; this proves the integration
	// through Bubble Tea's real parser. Three split points per shape are
	// enough to have teeth: the head split (after ESC), the middle, and the
	// terminator — each leaks garbage without the reassembler.
	// The exhaustive matrix lives in the byte-level tests above; this proves
	// integration through Bubble Tea's real parser for the shapes that matter:
	// wheel, click, and drag motion, each cut at the head, the middle, and the
	// terminator.
	for _, report := range []string{mouseReports[0], mouseReports[2], mouseReports[4]} {
		for _, cut := range []int{1, len(report) / 2, len(report) - 1} {
			if cut <= 0 || cut >= len(report) {
				continue
			}
			parts := []string{report[:cut], report[cut:]}
			chunks := make([][]byte, 0, len(parts))
			for _, p := range parts {
				chunks = append(chunks, []byte(p))
			}
			src := &replayReader{chunks: chunks, block: make(chan struct{})}
			msgs := runProgram(t, src, 1, 2*time.Second)
			close(src.block)

			if got := keyMsgs(msgs); len(got) > 0 {
				t.Fatalf("report %q split %q leaked %d key message(s): %v", report, parts, len(got), got)
			}
			if got := mouseMsgs(msgs); len(got) != 1 {
				t.Fatalf("report %q split %q → %d mouse message(s), want 1", report, parts, len(got))
			}
			// Exactly one message: a leak emitted after the expected count
			// must not slip past the sink's quit.
			if n := len(keyMsgs(msgs)) + len(mouseMsgs(msgs)); n != 1 {
				t.Fatalf("report %q split %q produced %d messages, want exactly 1", report, parts, n)
			}
			if want := reportButton(report); want != tea.MouseButtonNone && mouseMsgs(msgs)[0].Button != want {
				t.Fatalf("report %q split %q → button %v, want %v", report, parts, mouseMsgs(msgs)[0].Button, want)
			}
		}
	}
}

func TestProgramParsesWholeReportAsMouse(t *testing.T) {
	src := &replayReader{
		chunks: [][]byte{[]byte("\x1b[<65;75;25M")},
		block:  make(chan struct{}),
	}
	msgs := runProgram(t, src, 1, 300*time.Millisecond)
	close(src.block)

	got := mouseMsgs(msgs)
	if len(got) != 1 {
		t.Fatalf("whole report → %d mouse message(s), want 1 (%v)", len(got), msgs)
	}
	if got[0].Button != tea.MouseButtonWheelDown {
		t.Fatalf("whole report → %v, want a wheel-down event", got[0])
	}
}

func TestProgramKeepsTypedTextThroughSplitReports(t *testing.T) {
	// A wheel report split across reads must not eat the text typed around
	// it: the input stream is compared message by message.
	src := &replayReader{
		chunks: [][]byte{[]byte("hello"), []byte("\x1b[<65;75"), []byte(";25M"), []byte(" world")},
		block:  make(chan struct{}),
	}
	msgs := runProgram(t, src, 4, 300*time.Millisecond)
	close(src.block)

	var typed strings.Builder
	for _, km := range keyMsgs(msgs) {
		typed.WriteString(km.String())
	}
	if got := typed.String(); got != "hello world" {
		t.Fatalf("typed text = %q, want %q (messages: %v)", got, "hello world", msgs)
	}
	if got := mouseMsgs(msgs); len(got) != 1 {
		t.Fatalf("split report → %d mouse message(s), want 1", len(got))
	}
}

func TestProgramSurvivesWheelBurst(t *testing.T) {
	burst := strings.Repeat("\x1b[<65;75;25M", 8)
	cut := 7
	src := &replayReader{
		chunks: [][]byte{[]byte(burst[:cut]), []byte(burst[cut:])},
		block:  make(chan struct{}),
	}
	msgs := runProgram(t, src, 8, 300*time.Millisecond)
	close(src.block)

	if got := mouseMsgs(msgs); len(got) != 8 {
		t.Fatalf("burst → %d mouse message(s), want 8", len(got))
	}
	if got := keyMsgs(msgs); len(got) > 0 {
		t.Fatalf("burst leaked %d key message(s): %v", len(got), got)
	}
}

func TestAssembleInputStreamsLargePaste(t *testing.T) {
	// A paste arrives in many reads and must come out byte-for-byte, with
	// nothing delayed or dropped — this also exercises the cached-head path
	// that keeps large buffers from re-scanning themselves.
	body := strings.Repeat("lorem ipsum dolor sit amet 12345\n", 8000)
	paste := "\x1b[200~" + body + "\x1b[201~"
	var chunks []string
	for i := 0; i < len(paste); i += inputReadLen {
		chunks = append(chunks, paste[i:min(i+inputReadLen, len(paste))])
	}
	if got := drainString(t, chunks, 0, 10*time.Millisecond, 20*time.Millisecond); got != paste {
		t.Fatalf("large paste reassembled to %d bytes, want %d", len(got), len(paste))
	}
}

func TestProgramKeepsApplicationKeysIntact(t *testing.T) {
	// SS3 keys are the same shape as a mouse report's head: torn apart they
	// become alt+O plus a stray letter. Through the parser they must stay one
	// key message.
	for _, tc := range []struct{ seq, want string }{
		{"\x1bOA", "up"},
		{"\x1bOB", "down"},
		{"\x1bOC", "right"},
		{"\x1bOD", "left"},
	} {
		src := &replayReader{chunks: [][]byte{[]byte(tc.seq[:2]), []byte(tc.seq[2:])}, block: make(chan struct{})}
		msgs := runProgram(t, src, 1, 2*time.Second)
		close(src.block)

		keys := keyMsgs(msgs)
		if len(keys) != 1 {
			t.Fatalf("SS3 %q split produced %d key messages, want 1 (%v)", tc.seq, len(keys), msgs)
		}
		if got := keys[0].String(); got != tc.want {
			t.Fatalf("SS3 %q split → %q, want %q", tc.seq, got, tc.want)
		}
	}
}

func TestProgramSurvivesEveryBurstCut(t *testing.T) {
	// The same burst cut at every offset: each cut must still yield the full
	// count of wheel events and no typed garbage.
	// The byte-level burst test covers every offset; here a handful of cuts
	// near the head and mid-burst prove the integration through the parser.
	const reps = 4
	burst := strings.Repeat("\x1b[<65;75;25M", reps)
	for _, cut := range []int{1, 3, 7, 9, len(burst) / 2, len(burst) - 1} {
		src := &replayReader{
			chunks: [][]byte{[]byte(burst[:cut]), []byte(burst[cut:])},
			block:  make(chan struct{}),
		}
		msgs := runProgram(t, src, reps, 2*time.Second)
		close(src.block)

		if got := mouseMsgs(msgs); len(got) != reps {
			t.Fatalf("burst cut at %d → %d mouse message(s), want %d", cut, len(got), reps)
		}
		if got := keyMsgs(msgs); len(got) > 0 {
			t.Fatalf("burst cut at %d leaked %d key message(s): %v", cut, len(got), got)
		}
	}
}

func TestAssembleInputJoinsSlowReport(t *testing.T) {
	// A report that arrives byte by byte, slower than the abandon window,
	// must still be joined: progress restarts the wait. Abandoning it would
	// flush the coordinates as typing — the original bug.
	report := "\x1b[<65;75;25M"
	chunks := make([][]byte, 0, len(report))
	for i := 0; i < len(report); i++ {
		chunks = append(chunks, []byte(report[i:i+1]))
	}
	src := &replayReader{chunks: chunks, gap: 20 * time.Millisecond, block: make(chan struct{})}
	re := newInputReassembler(src, 10*time.Millisecond, 20*time.Millisecond)
	out := drainWithin(t, re, len(report), 3*time.Second)
	close(src.block)
	if out != report {
		t.Fatalf("slow report → %q, want it joined as %q", out, report)
	}
}

func TestAssembleInputBoundsEndlessHead(t *testing.T) {
	// A producer that streams an unterminated head forever must not grow the
	// buffer without bound, and must not stall the reader.
	chunks := make([][]byte, 0, 200)
	for i := 0; i < 200; i++ {
		chunks = append(chunks, []byte("1234567890"))
	}
	src := &replayReader{chunks: chunks, block: make(chan struct{})}
	re := newInputReassembler(src, 5*time.Millisecond, 15*time.Millisecond)
	out, ok := readWithin(t, re, time.Second)
	close(src.block)
	if !ok {
		t.Fatal("reader stalled on an endless head")
	}
	if len(out) == 0 {
		t.Fatal("endless head produced no bytes")
	}
}

func TestAssembleInputKeepsPasteMarkersWholeForSmallReads(t *testing.T) {
	// Bubble Tea reads with a 256-byte buffer. A paste body larger than that
	// must not have a marker cut in half — that would desync paste tracking
	// and let mouse-shaped bytes inside the paste be dropped.
	body := strings.Repeat("x", 300)
	paste := pasteStart + body + pasteEnd
	src := &replayReader{chunks: [][]byte{[]byte(paste)}, block: make(chan struct{})}
	re := newInputReassembler(src, 10*time.Millisecond, 20*time.Millisecond)

	// Bubble Tea reads with a 256-byte buffer: the release must never end
	// inside a marker, whatever the caller's buffer size is.
	got := drainWithin(t, re, len(paste), 3*time.Second)
	close(src.block)
	if got != paste {
		t.Fatalf("large paste → %d bytes, want %d", len(got), len(paste))
	}
	// Paste tracking must have seen both markers, so they survive the read
	// boundary that a 256-byte release would otherwise cut.
	for _, want := range []string{pasteStart, pasteEnd} {
		if !strings.Contains(got, want) {
			t.Fatalf("marker %q was split by a release", want)
		}
	}
}

// ── Bubble Tea integration contract ──────────────────────────────────────

func TestAssembleInputPresentsTerminalFile(t *testing.T) {
	// Bubble Tea enables raw mode only for input it recognises as a term.File
	// with a terminal descriptor (tty_unix.go initInput). The wrapper must
	// therefore expose the real descriptor, or the program would run cooked.
	f, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer f.Close()

	re := newInputReassembler(f, time.Millisecond, time.Millisecond)
	var _ term.File = re         // compile-time: Fd, Read, Write, Close
	var _ cancelreader.File = re // lint-free: same shape cancelreader probes
	if re.Fd() != f.Fd() {
		t.Fatalf("Fd = %d, want the underlying file's %d", re.Fd(), f.Fd())
	}
	if re.Name() != f.Name() {
		t.Fatalf("Name = %q, want the file's %q", re.Name(), f.Name())
	}
	if _, err := re.Write([]byte("x")); err != nil {
		t.Fatalf("Write must delegate to the file: %v", err)
	}
	if err := re.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close stops the pump, it does not close a descriptor the program does
	// not own.
	if _, err := f.Stat(); err != nil {
		t.Fatalf("Close closed the underlying file: %v", err)
	}
	if re.Fd() == ^uintptr(0) {
		t.Fatal("wrapped terminal must keep a usable descriptor")
	}
}

func TestAssembleInputLeavesNonTerminalReadersAlone(t *testing.T) {
	// A pipe or redirected stdin keeps Bubble Tea's own handling (including
	// opening /dev/tty) rather than being wrapped.
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()
	if got := AssembleInput(pr); got != io.Reader(pr) {
		t.Fatalf("AssembleInput wrapped a non-terminal reader: %T", got)
	}
	// A bare reader without a descriptor must not claim a terminal either.
	re := newInputReassembler(strings.NewReader("hi"), time.Millisecond, time.Millisecond)
	var _ term.File = re
	if re.Fd() != ^uintptr(0) {
		t.Fatalf("Fd = %d, want the invalid descriptor for a non-file source", re.Fd())
	}
	if _, err := re.Write([]byte("x")); err == nil {
		t.Fatal("Write must fail for a non-file source")
	}
}

func TestCutBeforeMarkerKeepsPasteMarkersWhole(t *testing.T) {
	b := []byte("abc" + pasteStart + "xyz")
	for n := 1; n <= len(b); n++ {
		got := cutBeforeMarker(b, n)
		if cut := len("abc"); n > cut && n < cut+len(pasteStart) && got != cut {
			t.Fatalf("cutBeforeMarker(%d) = %d, want %d so the marker stays whole", n, got, cut)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

// drainWithin reads until want bytes have arrived or d lapses. The source is
// still open, so a test must never wait for EOF.
func drainWithin(t *testing.T, r io.Reader, want int, d time.Duration) string {
	t.Helper()
	type result struct{ b string }
	ch := make(chan result, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 256) // Bubble Tea's read size
		for b.Len() < want {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		ch <- result{b: b.String()}
	}()
	select {
	case got := <-ch:
		return got.b
	case <-time.After(d):
		t.Fatalf("only %s of %d bytes arrived within %s", "some", want, d)
		return ""
	}
}

// reportButton maps a mouse report's leading parameter to the button Bubble
// Tea must decode from it, or MouseButtonNone when the report carries none
// (motion, X10).
func reportButton(report string) tea.MouseButton {
	if !strings.HasPrefix(report, "\x1b[<") {
		return tea.MouseButtonNone
	}
	body := report[len("\x1b[<"):]
	semi := strings.IndexByte(body, ';')
	if semi <= 0 {
		return tea.MouseButtonNone
	}
	code, err := strconv.Atoi(body[:semi])
	if err != nil {
		return tea.MouseButtonNone
	}
	const motion = 32
	if code >= motion { // motion flag: the button bits alone identify it
		code -= motion
	}
	switch code {
	case 0:
		return tea.MouseButtonLeft
	case 1:
		return tea.MouseButtonMiddle
	case 2:
		return tea.MouseButtonRight
	case 64:
		return tea.MouseButtonWheelUp
	case 65:
		return tea.MouseButtonWheelDown
	}
	return tea.MouseButtonNone
}

// firstRead returns the first non-empty read, or fails after d.
func firstRead(t *testing.T, r io.Reader, d time.Duration) []byte {
	t.Helper()
	buf := make([]byte, 64)
	type result struct{ b []byte }
	ch := make(chan result, 1)
	go func() {
		n, _ := r.Read(buf)
		ch <- result{b: append([]byte(nil), buf[:n]...)}
	}()
	select {
	case got := <-ch:
		return got.b
	case <-time.After(d):
		t.Fatalf("no input released within %s", d)
		return nil
	}
}

// readWithin reads one chunk, reporting false when nothing arrived in d.
func readWithin(t *testing.T, r io.Reader, d time.Duration) ([]byte, bool) {
	t.Helper()
	buf := make([]byte, 256)
	type result struct{ b []byte }
	ch := make(chan result, 1)
	go func() {
		n, _ := r.Read(buf)
		ch <- result{b: append([]byte(nil), buf[:n]...)}
	}()
	select {
	case got := <-ch:
		return got.b, true
	case <-time.After(d):
		return nil, false
	}
}

var _ = time.Second
