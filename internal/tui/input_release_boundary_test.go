package tui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Bubble Tea reads input 256 bytes at a time and parses each read on its own
// (key.go: `var buf [256]byte`). It has no way to tell that a read ended in
// the middle of a mouse report, so a release that stops there is not "held" —
// the head in front of the cut is a well-formed CSI and the bytes behind it
// are decoded as typed runes.
//
// Both mouse encodings tear this way:
//   - legacy X10 (ESC [ M plus three bytes): ESC [ M matches Bubble Tea's
//     unknownCSIRe on its own — M is a final byte — so the coordinate bytes
//     that follow are typed into the composer as single characters. Terminal.app
//     never negotiates mode 1006 (its xterm-256color terminfo advertises
//     kmous=\E[M and no XM), so a wheel burst is a stream of these 6-byte
//     reports and the 256-byte boundary lands inside one most of the time.
//   - SGR (ESC [ < … M/m): the consumed head makes the parser report an
//     Alt+[ keypress and the digits behind it become text.
//
// The tests below pin the invariant that keeps both out of the composer: a
// release never ends inside a mouse report.

// x10Report is a legacy wheel-up report: ESC [ M plus the button and the
// two 1-based coordinate bytes (0x60 = wheel up, 0x41 = column/row 1).
const x10Report = "\x1b[M\x60\x41\x41"

// sgrReport is the same event in the SGR (mode 1006) encoding.
const sgrReport = "\x1b[<64;1;1M"

// reportSpans locates the mouse reports in a stream with a scanner of its own
// — deliberately not the production escapeLen — so a release boundary can be
// judged against the byte forms terminals actually put on the wire.
func reportSpans(s string) [][2]int {
	var out [][2]int
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "\x1b[M") && i+6 <= len(s):
			out = append(out, [2]int{i, i + 6})
			i += 6
		case strings.HasPrefix(s[i:], "\x1b[<"):
			j := i + 3
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j < len(s) && (s[j] == 'M' || s[j] == 'm') {
				out = append(out, [2]int{i, j + 1})
				i = j + 1
				continue
			}
			i++
		default:
			i++
		}
	}
	return out
}

// assertBoundariesClear fails when any release boundary lands inside a mouse
// report: the report's head and tail are then parsed as two separate reads,
// and the tail is typed into the composer.
func assertBoundariesClear(t *testing.T, stream string, cuts []int) {
	t.Helper()
	spans := reportSpans(stream)
	for _, k := range cuts {
		for _, sp := range spans {
			if sp[0] < k && k < sp[1] {
				t.Fatalf("release boundary %d lands inside the mouse report at %d..%d of %d bytes: "+
					"Bubble Tea parses that read alone and types the bytes behind the head into the composer",
					k, sp[0], sp[1], len(stream))
			}
		}
	}
}

// TestAssembleInputReleaseNeverSplitsAReport drives bursts longer than the
// 256-byte read Bubble Tea uses and asserts that every release ends between
// reports — the invariant the composer depends on.
func TestAssembleInputReleaseNeverSplitsAReport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		burst string
	}{
		{"legacy x10 wheel burst", strings.Repeat(x10Report, 60)},                 // 360 bytes
		{"sgr wheel burst", strings.Repeat(sgrReport, 40)},                        // 440 bytes
		{"x10 burst with typing behind it", strings.Repeat(x10Report, 50) + "hi"}, // 302 bytes
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &replayReader{chunks: [][]byte{[]byte(tc.burst)}, block: make(chan struct{})}
			re := newInputReassembler(src, 5*time.Second, 5*time.Second)
			var got []byte
			var cuts []int
			for len(got) < len(tc.burst) {
				chunk, ok := readWithin(t, re, time.Second)
				if !ok || len(chunk) == 0 {
					t.Fatalf("released %d of %d bytes and then stalled", len(got), len(tc.burst))
				}
				got = append(got, chunk...)
				if len(got) < len(tc.burst) {
					cuts = append(cuts, len(got))
				}
			}
			close(src.block)
			if string(got) != tc.burst {
				t.Fatalf("burst released as %d bytes, want the original %d byte-for-byte", len(got), len(tc.burst))
			}
			assertBoundariesClear(t, tc.burst, cuts)
		})
	}
}

// msgSummary renders the message mix of a run, so a failure says what the
// burst turned into instead of only how much of it survived.
func msgSummary(msgs []tea.Msg) string {
	counts := map[string]int{}
	var order []string
	for _, m := range msgs {
		kind := fmt.Sprintf("%T", m)
		if n, ok := counts[kind]; !ok {
			order = append(order, kind)
			counts[kind] = 1
		} else {
			counts[kind] = n + 1
		}
	}
	parts := make([]string, 0, len(order))
	for _, kind := range order {
		parts = append(parts, fmt.Sprintf("%s×%d", kind, counts[kind]))
	}
	return strings.Join(parts, " ")
}

// assertBurstIsAllMouse checks that a burst arrived as exactly reps mouse
// events and nothing else: no typed runes, and no unknown-sequence garbage
// either — both mean the boundary tore a report.
func assertBurstIsAllMouse(t *testing.T, msgs []tea.Msg, reps int, what string) {
	t.Helper()
	if got := keyMsgs(msgs); len(got) > 0 {
		t.Fatalf("%s leaked %d key message(s) into the composer: %v", what, len(got), got)
	}
	if got := mouseMsgs(msgs); len(got) != reps {
		t.Fatalf("%s → %d mouse message(s), want %d (%s)", what, len(got), reps, msgSummary(msgs))
	}
	if len(msgs) != reps {
		t.Fatalf("%s → %d message(s) in total, want exactly %d mouse events (%s)", what, len(msgs), reps, msgSummary(msgs))
	}
}

// TestProgramKeepsLegacyX10BurstIntact is the same invariant through Bubble
// Tea's real parser: the burst must arrive as one mouse event per report with
// nothing typed in between. This is the Terminal.app shape.
func TestProgramKeepsLegacyX10BurstIntact(t *testing.T) {
	const reps = 60
	burst := strings.Repeat(x10Report, reps) // 360 bytes: past the 256-byte read

	assertBurstIsAllMouse(t, runProgram(t, [][]byte{[]byte(burst)}, reps, 2*time.Second), reps, "x10 burst")
}

// TestProgramKeepsSGRBurstIntact covers the mode-1006 terminals (VSCode,
// iTerm2, kitty): the same boundary must not tear their longer reports either.
func TestProgramKeepsSGRBurstIntact(t *testing.T) {
	const reps = 40
	burst := strings.Repeat(sgrReport, reps) // 440 bytes: past the 256-byte read

	assertBurstIsAllMouse(t, runProgram(t, [][]byte{[]byte(burst)}, reps, 2*time.Second), reps, "sgr burst")
}

// TestProgramKeepsLongX10WheelBurstIntact scales the same shape up: a fast
// scroll is kilobytes of reports, and every byte must be delivered without
// waiting for further input. A byte read off the descriptor but not released
// in the same read is invisible to the reader's own readiness wait (the
// kqueue/epoll cancel reader waits on the descriptor first), so the burst
// would stall mid-flight — which is why the reassembler reads no further than
// the room its caller offers.
func TestProgramKeepsLongX10WheelBurstIntact(t *testing.T) {
	const reps = 400
	burst := strings.Repeat(x10Report, reps) // 2.4K: many reads past the room

	assertBurstIsAllMouse(t, runProgram(t, [][]byte{[]byte(burst)}, reps, 5*time.Second), reps, "long x10 burst")
}

// TestProgramDeliversLargePasteWhole guards the other side of the read cap: a
// paste larger than the caller's buffer is delivered in many reads, and
// Bubble Tea only recognises the body as pasted once it has seen both markers
// — so bounding the read must not tear the paste apart or delay its tail.
func TestProgramDeliversLargePasteWhole(t *testing.T) {
	body := strings.Repeat("paste body 42 ", 120) // 1.6K
	stream := pasteStart + body + pasteEnd

	msgs := runProgram(t, [][]byte{[]byte(stream)}, 1, 5*time.Second)

	if len(msgs) != 1 {
		t.Fatalf("large paste → %d message(s), want a single paste (%s)", len(msgs), msgSummary(msgs))
	}
	km, ok := msgs[0].(tea.KeyMsg)
	if !ok {
		t.Fatalf("large paste → %T, want a key message", msgs[0])
	}
	if !km.Paste {
		t.Fatalf("large paste → KeyMsg{Paste: false}: Bubble Tea did not see it as pasted")
	}
	if got := string(km.Runes); got != body {
		t.Fatalf("large paste → %d runes, want %d byte-for-byte", len(km.Runes), len(body))
	}
}

// drainWithBuffer reads everything r produces in chunks of at most size bytes,
// like a caller whose buffer is smaller than Bubble Tea's 256. It returns the
// bytes and every release boundary, so the same boundary invariant can be
// judged for any caller size.
func drainWithBuffer(t *testing.T, r io.Reader, size int, budget time.Duration) (string, []int) {
	t.Helper()
	var got []byte
	var cuts []int
	var spent time.Duration
	step := 5 * time.Millisecond
	for {
		buf := make([]byte, size)
		n, err := r.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
			cuts = append(cuts, len(got))
		}
		if err != nil {
			break
		}
		if n == 0 {
			spent += step
			if spent > budget {
				t.Fatalf("reader stalled after %d bytes", len(got))
			}
			time.Sleep(step)
		}
	}
	if len(cuts) > 0 {
		cuts = cuts[:len(cuts)-1] // the last release ends the stream, not a cut
	}
	return string(got), cuts
}

// TestAssembleInputSmallCallerBufferNeverTearsAReport pins the release clamp.
// A caller with a smaller buffer than the read that filled the wrapper's own
// buffer forces a cut mid-stream; every such cut must still land between
// reports. Without the clamp in emit the boundary lands inside a report and
// Bubble Tea decodes the bytes behind the head as text — this test fails.
//
// The guarantee needs a buffer that can hold one whole report: a buffer
// shorter than the longest sequence in the stream cannot be served without
// tearing it, because cutting at offset 0 would release nothing and stall the
// caller (that is why the clamp stops there). Bubble Tea reads 256 bytes, an
// order of magnitude above the longest report a terminal emits.
func TestAssembleInputSmallCallerBufferNeverTearsAReport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		burst string
		sizes []int
	}{
		{"legacy x10 reports", strings.Repeat(x10Report, 30), []int{6, 7, 11, 64, 256}},
		{"sgr reports", strings.Repeat(sgrReport, 30), []int{10, 11, 64, 256}},
	} {
		for _, size := range tc.sizes {
			t.Run(fmt.Sprintf("%s buffer %d", tc.name, size), func(t *testing.T) {
				src := &replayReader{chunks: [][]byte{[]byte(tc.burst)}}
				re := newInputReassembler(src, time.Second, time.Second)
				got, cuts := drainWithBuffer(t, re, size, 2*time.Second)
				if got != tc.burst {
					t.Fatalf("released %d bytes, want the original %d byte-for-byte", len(got), len(tc.burst))
				}
				assertBoundariesClear(t, tc.burst, cuts)
			})
		}
	}
}

// TestReadWithNoRoomIsANoOp pins the empty-buffer guard: Read must not reach
// for the source to fill a caller that has no room.
func TestReadWithNoRoomIsANoOp(t *testing.T) {
	src := &replayReader{chunks: [][]byte{[]byte(x10Report)}, block: make(chan struct{})}
	re := newInputReassembler(src, time.Second, time.Second)
	n, err := re.Read(nil)
	close(src.block)
	if n != 0 || err != nil {
		t.Fatalf("Read(nil) → (%d, %v), want (0, nil)", n, err)
	}
}

// TestReadDropsAReportFillingTheCallersBuffer pins the room<=0 branch: when a
// mouse head fills the caller's whole buffer it is noise, not input, so it is
// dropped rather than streamed out as text.
func TestReadDropsAReportFillingTheCallersBuffer(t *testing.T) {
	head := strings.Repeat("\x1b[<64;75", 3) // a mouse-shaped head, no terminator
	src := &replayReader{chunks: [][]byte{[]byte(head), []byte("ok")}, block: make(chan struct{})}
	re := newInputReassembler(src, 5*time.Millisecond, 30*time.Millisecond)

	buf := make([]byte, len(head)) // exactly the head: room == 0
	n, _ := re.Read(buf)
	close(src.block)
	if string(buf[:n]) == head {
		t.Fatalf("a report-shaped head filling the buffer was streamed out as text: %q", string(buf[:n]))
	}
}

// TestCutBeforeSequenceClampsAtTheStraddlingReport pins the helper directly:
// it must name the byte offset a release has to stop at.
func TestCutBeforeSequenceClampsAtTheStraddlingReport(t *testing.T) {
	for _, tc := range []struct {
		name string
		buf  string
		n    int
		want int
	}{
		{"x10 report straddles the cut", x10Report + x10Report, 8, 6},
		{"sgr report straddles the cut", sgrReport + sgrReport, 12, 10},
		{"cut inside the head", x10Report + x10Report, 2, 0},
		{"cut exactly between reports", x10Report + x10Report, 6, 0},
		{"complete sequence before the cut", x10Report + "typed", 11, 0},
		{"no sequence at all", "plain typing here", 12, 0},
		{"whole buffer is the cut", x10Report, len(x10Report), 0},
		{"escape at offset 0 is not a stall", x10Report + x10Report, 3, 0},
		{"typing then a straddling report", "hi" + x10Report, 5, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cutBeforeSequence([]byte(tc.buf), tc.n); got != tc.want {
				t.Fatalf("cutBeforeSequence(%q, %d) = %d, want %d", tc.buf, tc.n, got, tc.want)
			}
		})
	}
}

// clamping releases to report boundaries must not delay or drop the typing
// that follows a burst.
func TestProgramKeepsTypingThroughABurst(t *testing.T) {
	const reps = 50
	burst := strings.Repeat(x10Report, reps) + "ok"
	// One mouse message per report, then the two runes: 52 messages total.
	msgs := runProgram(t, [][]byte{[]byte(burst)}, reps+1, 2*time.Second)

	if got := mouseMsgs(msgs); len(got) != reps {
		t.Fatalf("burst → %d mouse message(s), want %d", len(got), reps)
	}
	keys := keyMsgs(msgs)
	if len(keys) != 1 || string(keys[0].Runes) != "ok" {
		t.Fatalf("typing behind a burst → %v, want a single %q key", keys, "ok")
	}
}
