package tui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

// inputSettle bounds how long an incomplete escape-command head (ESC, ESC[,
// ESC[6, ESCO, …) may wait for the bytes that would complete it. Lapsed, the
// head is handed to Bubble Tea verbatim, so a bare ESC keypress still closes a
// popup promptly.
const inputSettle = 10 * time.Millisecond

// mouseAbandon bounds how long a mouse-shaped head is held before it is
// dropped. A mouse report is never user input, so echoing a truncated one as
// text would type garbage into the composer.
const mouseAbandon = 250 * time.Millisecond

// inputReadLen caps one source read: a whole wheel burst lands in one read,
// while memory stays bounded. A read from the watched terminal is further
// bounded by the caller's room (see readNext).
const inputReadLen = 4096

// stringSeqCap bounds how much of a string sequence (OSC / DCS / APC) is held
// while waiting for its terminator. Past it the bytes are released verbatim:
// an unterminated control string is a broken producer, not input to hide.
const stringSeqCap = 4096

// inputHeadCap bounds how long a single head may grow. A sequence that has not
// resolved by then is malformed (or a hostile producer), so it is resolved
// immediately rather than held — and the buffer stays bounded.
const inputHeadCap = 512

// Bracketed-paste markers. They arrive as complete CSI sequences, so a release
// that would end inside one is shortened instead — paste state is tracked from
// the released bytes.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// errNotWritable is returned by Write for a source that is not a file.
var errNotWritable = errors.New("bodek: input stream is not writable")

// inputChunk is one read from the source.
type inputChunk struct {
	data []byte
	err  error
}

// inputReassembler adapts a terminal input stream so escape sequences split
// across reads reach Bubble Tea in one piece.
//
// Bubble Tea's input reader parses a read byte by byte. When a read ends in
// the middle of a sequence — most visibly a mouse report, `ESC [ < 65 ; x ; y
// M` — the pending `ESC[` is not held: it is consumed as an Alt+[ keypress and
// the printable tail arrives as ordinary text, which the composer then types
// verbatim. This wrapper holds an undecidable head until the bytes that
// complete it arrive.
//
// It never reorders, duplicates, or drops user input. The single exception is
// an SGR mouse-shaped head: the form bodek's terminal emits, never something a
// person types, so a truncated one is discarded instead of being echoed.
//
// When the source is a terminal *os.File the wrapper also presents itself as a
// term.File with that file's descriptor: Bubble Tea enables and restores raw
// mode only for input it recognises as a file (tty_unix.go's initInput), and
// declaring the real descriptor keeps that path intact.
type inputReassembler struct {
	src     io.Reader
	file    *os.File
	buf     []byte
	settle  time.Duration
	abandon time.Duration

	// headAt is where the buffer's trailing undecidable head begins, or -1
	// when there is none. It is recomputed when bytes are appended and
	// shifted when bytes are released, so a release stays O(1) in the buffer
	// size — a large paste must not re-scan itself 256 bytes at a time.
	headAt int

	// holding marks that the wait for a head's tail has begun, with since the
	// moment it started and heldLen the size of the head then.
	holding bool
	since   time.Time
	heldLen int

	// inPaste tracks an open bracketed paste: paste bodies are user data and
	// are never dropped, whatever they contain.
	inPaste bool

	// srcErr is the error the source reported (io.EOF when it closed
	// cleanly); it is delivered once the buffer drains. srcDone marks that
	// the report happened, so no further source reads are attempted.
	srcErr  error
	srcDone bool

	// pending is the in-flight read of a non-file source: one buffered
	// goroutine whose result is either consumed immediately or picked up by
	// the next Read. At most one exists at a time, so reads are never
	// duplicated or reordered.
	pending chan inputChunk

	done      chan struct{}
	closeOnce sync.Once
}

// AssembleInput wraps a terminal input reader so fragmented escape sequences
// are reassembled before Bubble Tea parses them.
//
// Only a terminal file is wrapped: anything else (a pipe, a redirected file)
// is returned untouched, leaving Bubble Tea's own handling — including opening
// /dev/tty — exactly as it is.
func AssembleInput(r io.Reader) io.Reader {
	if f, ok := r.(*os.File); ok && term.IsTerminal(f.Fd()) {
		return newInputReassembler(f, inputSettle, mouseAbandon)
	}
	return r
}

func newInputReassembler(src io.Reader, settle, abandon time.Duration) *inputReassembler {
	r := &inputReassembler{
		buf:     nil,
		settle:  settle,
		abandon: abandon,
		headAt:  -1,
		srcErr:  io.EOF,
		done:    make(chan struct{}),
	}
	r.file, _ = src.(*os.File)
	r.src = src
	return r
}

// ── term.File: keep Bubble Tea's raw-mode path intact ─────────────────────

// Fd exposes the underlying descriptor so Bubble Tea treats the wrapper as the
// program's input file and puts the terminal into raw mode. Without it the
// program runs in cooked mode — a far worse bug than the one being fixed.
func (r *inputReassembler) Fd() uintptr {
	if r.file == nil {
		return ^uintptr(0) // invalid: not a terminal
	}
	return r.file.Fd()
}

// Name completes the descriptor triple cancelreader looks for (Read, Write,
// Close, Fd, Name). Without it Bubble Tea falls back to a cancel reader that
// cannot interrupt an in-flight read, which delays shutdown and lets a stale
// read loop deliver a keystroke after the program cancelled it.
func (r *inputReassembler) Name() string {
	if r.file == nil {
		return "stdin"
	}
	return r.file.Name()
}

// Write satisfies term.File by delegating to the underlying file. Bubble Tea
// never writes to its input, but the interface requires it.
func (r *inputReassembler) Write(p []byte) (int, error) {
	if r.file == nil {
		return 0, errNotWritable
	}
	return r.file.Write(p)
}

// Close stops the reader without closing the descriptor: the program does not
// own stdin, and Bubble Tea restores the terminal through Fd instead.
func (r *inputReassembler) Close() error {
	r.closeOnce.Do(func() { close(r.done) })
	return nil
}

// isClosed reports whether Close has been called.
func (r *inputReassembler) isClosed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// ── reading ───────────────────────────────────────────────────────────────

// Read implements io.Reader.
//
// The source is consumed synchronously — one read at a time — so bytes stay
// unread in the kernel buffer until Bubble Tea asks for them. That keeps
// kqueue/epoll wakeups coherent: a pre-draining pump goroutine would strand
// already-read bytes where a kevent can never see them, freezing input until
// an unrelated later keystroke lands.
//
// For the same reason a read from the terminal never fetches more than the
// room in p: whatever is read but not released in this call sits here where the
// reader's own readiness wait — it waits on the descriptor before every Read —
// cannot see it. Holding a byte back would stall every remaining byte of the
// burst until the next keystroke. With the read bounded by p, a release is
// either the whole buffer or everything up to a held head, and the only bytes
// left behind are an incomplete sequence whose tail is still arriving. A
// non-file source has no readiness wait to strand bytes behind (the wrapper is
// only ever attached to terminals) and reads its full window instead.
func (r *inputReassembler) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		room := len(p) - len(r.buf)
		if r.headAt > 0 {
			return r.emit(p, r.headAt), nil
		}
		if len(r.buf) > 0 && r.headAt < 0 {
			return r.emit(p, len(r.buf)), nil
		}
		if r.headAt == 0 {
			if r.srcDone {
				return r.finish(p)
			}
			if room <= 0 {
				// The head fills the caller's whole buffer. A head that can only
				// be a mouse report is noise even here: streaming a truncated
				// report as text is the bug being fixed, so drop it.
				if r.droppable() {
					r.drop()
					continue
				}
				// A real sequence longer than the caller's buffer (a long string
				// sequence) is streamed rather than held for a tail that could
				// never fit.
				return r.emit(p, len(r.buf)), nil
			}
			if n, ok := r.held(p, room); ok {
				return n, nil
			}
			continue
		}
		if r.srcDone || r.isClosed() {
			return r.finish(p)
		}
		c, _ := r.readNext(-1, room)
		r.take(c)
	}
}

// held waits for the tail of the head that occupies the whole buffer. It
// reports false when the head was resolved (dropped or flushed), in which case
// the caller re-plans. room is how many bytes the caller can still take.
func (r *inputReassembler) held(p []byte, room int) (int, bool) {
	if !r.holding {
		r.holding, r.since, r.heldLen = true, time.Now(), len(r.buf)
	}
	// A head that keeps growing past the cap is malformed: resolve it now
	// instead of holding forever.
	if len(r.buf) > inputHeadCap {
		if r.droppable() {
			r.drop()
			return 0, false
		}
		return r.emit(p, len(r.buf)), true
	}
	budget := r.settle - time.Since(r.since)
	if r.droppable() {
		budget = r.abandon - time.Since(r.since)
	}
	if budget > 0 {
		if c, ok := r.readNext(budget, room); ok {
			r.take(c)
			if r.srcDone {
				n, err := r.finish(p)
				return n, err == nil
			}
			return 0, false
		}
	}
	// The wait lapsed. A mouse head whose tail never arrived is noise; any
	// other incomplete head is genuine input once it has waited its while.
	if r.droppable() {
		r.abandonHead()
		return 0, false
	}
	return r.emit(p, len(r.buf)), true
}

// take folds a source read into the buffer and records any error it carried.
func (r *inputReassembler) take(c inputChunk) {
	r.absorb(c)
	if c.err != nil {
		r.srcErr = c.err
		r.srcDone = true
	}
}

// readNext performs one read from the source, waiting no longer than budget
// for input to arrive. A read from the watched descriptor never fetches more
// than room bytes; a non-file source (tests only) reads its full window. A
// negative budget blocks until input or the source ends. It reports false only
// when the budget lapsed without any data — nothing was consumed, so the
// caller may retry or give up on the head.
//
// For a file source the wait is poll(2) on the descriptor followed by the read
// itself, so unread bytes stay in the kernel buffer and kqueue/epoll wakeups
// remain coherent. Non-file sources only occur in unit tests (the wrapper
// itself is attached to terminals only); for those, a single buffered read
// goroutine provides the bounded wait without ever losing a read.
func (r *inputReassembler) readNext(budget time.Duration, room int) (inputChunk, bool) {
	if room <= 0 && r.file != nil {
		return inputChunk{}, false
	}
	// A read from the watched descriptor never exceeds the caller's room:
	// bytes read ahead of an unfilled buffer sit where the readiness wait that
	// gates every Read (kqueue/epoll, see waitReadable) cannot see them, which
	// would stall the rest of the burst until the next keystroke. A non-file
	// source has no such wait — the wrapper is only ever attached to terminals,
	// so that path exists for tests — and reads its full window instead; the
	// release clamp in emit is what keeps its sequences whole.
	size := inputReadLen
	if r.file != nil {
		size = min(inputReadLen, room)
	}
	if r.file != nil {
		if budget >= 0 && !r.waitReadable(budget) {
			return inputChunk{}, false
		}
		b := make([]byte, size)
		n, err := r.file.Read(b)
		return inputChunk{data: b[:n], err: err}, true
	}
	if r.pending == nil {
		ch := make(chan inputChunk, 1)
		r.pending = ch
		go func() {
			b := make([]byte, size)
			n, err := r.src.Read(b)
			ch <- inputChunk{data: b[:n], err: err}
		}()
	}
	if budget >= 0 {
		select {
		case c := <-r.pending:
			r.pending = nil
			return c, true
		case <-time.After(budget):
			return inputChunk{}, false
		}
	}
	c := <-r.pending
	r.pending = nil
	return c, true
}

// waitReadable blocks until the descriptor has input or budget elapses,
// reporting whether input arrived. Platform-specific: see
// input_reassembler_poll.go and input_reassembler_poll_windows.go.

// absorb appends a read to the buffer.
//
// A held mouse head is resolved against the new bytes: the report runs through
// digits and semicolons to its terminator. A read that completes the report is
// appended whole — Bubble Tea parses the report and any typing behind it from
// one read, so nothing needs splitting. A read that cannot belong to the
// report proves the head was truncated, and only the head is discarded.
func (r *inputReassembler) absorb(c inputChunk) {
	if len(c.data) > 0 {
		if !r.inPaste && r.headAt == 0 && mouseHead(r.buf) {
			if n, complete := mouseReportSpan(c.data); !complete && n < len(c.data) {
				// The read starts with a byte no mouse report can hold: the
				// head is a truncated report. Drop it, keep the read — it is
				// user input, and may itself open a new report.
				r.drop()
			}
		}
		r.buf = append(r.buf, c.data...)
		r.headAt = trailingHead(r.buf)
		if r.headAt >= 0 {
			// Progress: the wait restarts from here, so a report that keeps
			// arriving byte by byte is joined instead of being abandoned
			// mid-flight, while a stalled head still times out.
			r.holding, r.since, r.heldLen = true, time.Now(), len(r.buf)
		}
	}
	if c.err != nil {
		r.srcErr = c.err
	}
}

// emit copies n buffered bytes into p, tracks paste state across them, and
// never ends a release inside an escape sequence or a bracketed-paste marker.
//
// The caller's buffer is what forces the cut: Bubble Tea reads input 256 bytes
// at a time and parses every read on its own, so a chunk that stops inside a
// mouse report is not "incomplete" from its side. The head in front of the cut
// is a finished CSI (ESC [ M is the legacy form's final byte) and the bytes
// behind it are decoded as typed runes — one stray character per boundary,
// repeated through a wheel burst. A torn marker would likewise desync paste
// tracking.
func (r *inputReassembler) emit(p []byte, n int) int {
	if n > len(r.buf) {
		n = len(r.buf)
	}
	// The caller's buffer is the hard cut, and it is applied before the
	// sequence rules below: a clamp inside copy would silently tear whatever
	// sequence happens to sit at the boundary.
	if n > len(p) {
		n = len(p)
	}
	if len(p) >= len(pasteStart) {
		if cut := cutBeforeMarker(r.buf, n); cut > 0 && cut < n {
			n = cut
		}
	}
	if cut := cutBeforeSequence(r.buf, n); cut > 0 && cut < n {
		n = cut
	}
	written := copy(p, r.buf[:n])
	r.trackPaste(r.buf[:written])
	r.buf = r.buf[written:]
	if r.headAt >= 0 {
		// A release can stop short of the head's start — the rules above cut
		// it back — and the remainder is then the head's own tail, so the
		// head begins at offset 0. It must never go negative: a negative
		// offset reads as "no head", and the tail would be released unheld
		// on the next call.
		if r.headAt >= written {
			r.headAt -= written
		} else {
			r.headAt = 0
		}
	}
	if len(r.buf) == 0 {
		r.headAt = -1
	}
	r.holding = false
	return written
}

// abandonHead drops a mouse head whose tail never arrived while keeping any
// bytes that arrived after it: a keystroke that follows the stalled report is
// user input and must survive.
func (r *inputReassembler) abandonHead() {
	keep := r.buf[min(r.heldLen, len(r.buf)):]
	r.buf = append(r.buf[:0], keep...)
	r.headAt = trailingHead(r.buf)
	r.holding = false
}

// drop discards a head that can only be a mouse report. It is used where the
// head is the whole buffer, so nothing else can be lost with it.
func (r *inputReassembler) drop() {
	r.buf = r.buf[:0]
	r.headAt = -1
	r.holding = false
	r.heldLen = 0
}

// finish flushes what is buffered and then reports the source's error. A
// pending mouse head is dropped first: at end of input it can only be a
// truncated report, and flushing it would type garbage. Bytes that arrived
// after the head are kept — they are not part of the report.
func (r *inputReassembler) finish(p []byte) (int, error) {
	if r.droppable() {
		r.abandonHead()
	}
	if len(r.buf) > 0 {
		return r.emit(p, len(r.buf)), nil
	}
	if r.srcErr != nil {
		return 0, r.srcErr
	}
	return 0, io.EOF
}

// droppable reports whether the held head can only be a mouse report.
func (r *inputReassembler) droppable() bool {
	return !r.inPaste && mouseHead(r.buf)
}

// trackPaste toggles the paste flag for the bracketed-paste markers in a
// released chunk. Markers are never split by a release (see cutBeforeMarker),
// so this stays accurate.
func (r *inputReassembler) trackPaste(b []byte) {
	s := string(b)
	for {
		marker := pasteEnd
		if !r.inPaste {
			marker = pasteStart
		}
		i := bytes.Index([]byte(s), []byte(marker))
		if i < 0 {
			return
		}
		r.inPaste = !r.inPaste
		s = s[i+len(marker):]
	}
}

// ── sequence geometry ─────────────────────────────────────────────────────

// trailingHead returns the offset where b's first undecidable head begins, or
// -1 when b can be handed to Bubble Tea in full.
func trailingHead(b []byte) int {
	for i := 0; i < len(b); {
		if b[i] != 0x1b {
			i++
			continue
		}
		n, complete := escapeLen(b[i:])
		if !complete {
			return i
		}
		if n <= 0 {
			i++
			continue
		}
		i += n
	}
	return -1
}

// escapeLen measures the escape sequence at the start of tail and reports
// whether tail holds all of it.
//
// The forms are those a terminal emits: CSI (ESC [ … final 0x40..0x7E, plus
// the X10 mouse shape ESC [ M plus three bytes), SS3 (ESC O + one byte),
// string sequences (OSC / DCS / SOS / PM / APC, terminated by BEL or ST), and
// the short ESC + intermediate + final family (charset selection and friends).
func escapeLen(tail []byte) (int, bool) {
	if len(tail) == 0 || tail[0] != 0x1b {
		return 0, true
	}
	if len(tail) == 1 {
		return 0, false // lone ESC — may head a split sequence
	}
	switch c := tail[1]; {
	case c == '[': // CSI
		if len(tail) == 2 {
			return 0, false
		}
		if tail[2] == 'M' { // X10 mouse: three coordinate bytes
			if len(tail) < 6 {
				return 0, false
			}
			return 6, true
		}
		for j := 2; j < len(tail); j++ {
			switch ch := tail[j]; {
			case ch >= 0x30 && ch <= 0x3f, ch >= 0x20 && ch <= 0x2f: // parameter / intermediate
			case ch >= 0x40 && ch <= 0x7e: // final byte — sequence complete
				return j + 1, true
			default: // malformed: nothing further can complete it
				return j + 1, true
			}
		}
		return 0, false
	case c == 'O': // SS3: ESC O plus one byte (application cursor keys, F1…)
		if len(tail) < 3 {
			return 0, false
		}
		return 3, true
	case c == ']', c == 'P', c == 'X', c == '^', c == '_': // string sequences
		if len(tail) > stringSeqCap {
			return 0, true // broken producer: release rather than hold forever
		}
		for j := 2; j < len(tail); j++ {
			if tail[j] == 0x07 { // BEL
				return j + 1, true
			}
			if tail[j] == 0x1b && j+1 < len(tail) && tail[j+1] == '\\' { // ST
				return j + 2, true
			}
		}
		return 0, false
	case c >= 0x20 && c <= 0x2f: // ESC + intermediate, one byte to follow
		if len(tail) < 3 {
			return 0, false
		}
		return 3, true
	default: // two-byte escape (ESC 7, ESC M, …)
		return 2, true
	}
}

// mouseHead reports whether b is an unterminated SGR mouse report:
// ESC [ < digits/; … . Reaching this with a complete report is impossible —
// trailingHead only returns complete sequences as runnable.
//
// Only the SGR form (mode 1006, which bodek enables) is treated as
// disposable. The legacy X10 form ESC [ M plus three arbitrary bytes is held
// and joined like any other incomplete CSI, but never dropped: its coordinate
// bytes are indistinguishable from typed text, so discarding it could eat real
// input.
func mouseHead(b []byte) bool {
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' || b[2] != '<' {
		return false
	}
	for _, c := range b[3:] {
		if (c < '0' || c > '9') && c != ';' {
			return false
		}
	}
	return true
}

// mouseReportSpan measures how far the mouse report started by a head extends
// into data: its digits and semicolons, plus the terminating M or m when one
// is present. complete reports whether a terminator was seen — without one the
// data cannot have belonged to the report, so it is user input.
func mouseReportSpan(data []byte) (n int, complete bool) {
	for n < len(data) {
		switch c := data[n]; {
		case c >= '0' && c <= '9', c == ';':
			n++
		case c == 'M', c == 'm':
			return n + 1, true
		default:
			return n, false
		}
	}
	return n, false
}

// cutBeforeMarker shortens n so a release never ends inside a bracketed-paste
// marker — otherwise the marker's tail would be released as text and paste
// tracking would desync.
func cutBeforeMarker(b []byte, n int) int {
	if n <= 0 || n >= len(b) {
		return n
	}
	lo := max(0, n-6)
	hi := min(len(b), n+6)
	window := b[lo:hi]
	for _, marker := range []string{pasteStart, pasteEnd} {
		if k := bytes.Index(window, []byte(marker)); k >= 0 {
			start := lo + k
			if start < n && n < start+len(marker) {
				return start
			}
		}
	}
	return n
}

// cutBeforeSequence shortens n so a release never ends inside an escape
// sequence. It returns the offset the release must stop at, or 0 when no
// sequence straddles n.
//
// The room-bounded read (see Read) already lands releases from a watched
// descriptor on sequence boundaries; this is the release-side guarantee, and
// it is load-bearing wherever the buffer can hold more than the caller asked
// for — a non-file source reading its full window, and a caller that hands a
// smaller buffer than the read that filled the buffer. Bubble Tea cannot
// recover from the split itself: ESC [ M is a valid final byte on its own, so
// it consumes a torn legacy X10 report's head as an unknown CSI and decodes
// the coordinate bytes behind it as text, while a torn SGR report leaves
// ESC [ < … with no final byte and is reported as an Alt+[ keypress with the
// digits behind it as text. Either way the bytes reach the composer. The tail
// is held here instead and released with the next read.
//
// Only the last escape before n can straddle it, so that is the one examined.
// An escape at offset 0 is left alone: stopping there would return an empty
// read and stall the caller — a sequence longer than the caller's buffer (a
// long OSC string) is streamed instead, which Bubble Tea already handles by
// waiting for its terminator.
func cutBeforeSequence(b []byte, n int) int {
	if n <= 1 || n >= len(b) {
		return 0
	}
	i := bytes.LastIndexByte(b[:n], 0x1b)
	if i <= 0 {
		return 0
	}
	length, complete := escapeLen(b[i:])
	if !complete || i+length > n {
		return i
	}
	return 0
}
