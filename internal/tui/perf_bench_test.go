package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BackendStack21/bodek/internal/client"
)

// benchTranscript builds a transcript of n finalized user/assistant pairs so
// benchmarks exercise a realistic long session, not a two-message chat.
func benchTranscript(n int) []message {
	msgs := make([]message, 0, n*2)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("Answer %d: %s", i, strings.Repeat("lorem ipsum dolor sit amet ", 8))
		msgs = append(msgs,
			message{role: roleUser, content: fmt.Sprintf("question %d", i)},
			message{role: roleAsst, content: body},
		)
	}
	return msgs
}

// BenchmarkConversationLongSession measures the transcript rebuild per
// refresh: prefix join, block cache reads, tail rendering. This is the cost
// paid ~12 Hz while streaming and on every UI transition.
func BenchmarkConversationLongSession(b *testing.B) {
	for _, n := range []int{25, 100, 400} {
		b.Run(fmt.Sprintf("msgs=%d", n*2), func(b *testing.B) {
			m := newTestModel()
			m.ready = true
			m.msgs = benchTranscript(n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.conversation()
			}
		})
	}
}

// BenchmarkRefreshStreaming measures the full refresh path (conversation +
// viewport SetContent) with a streaming tail message, as fired by the 80 ms
// render flush while a turn streams.
func BenchmarkRefreshStreaming(b *testing.B) {
	for _, n := range []int{25, 100} {
		b.Run(fmt.Sprintf("msgs=%d", n*2+1), func(b *testing.B) {
			m := newTestModel()
			m.ready = true
			m.msgs = benchTranscript(n)
			m.msgs = append(m.msgs, message{role: roleAsst, streaming: true})
			m.curIdx = len(m.msgs) - 1
			m.busy = true
			m.runStart = time.Now()
			m.handleEvent(client.Event{Type: "token", Content: strings.Repeat("streaming answer text grows here; ", 32)})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.refresh()
			}
		})
	}
}

// BenchmarkIngestTokenBatch measures handleEvent ingestion of reply-token
// deltas into the streaming turn — the per-frame cost before any rendering.
func BenchmarkIngestTokenBatch(b *testing.B) {
	m := newTestModel()
	m.ready = true
	m.msgs = append(m.msgs,
		message{role: roleUser, content: "go"},
		message{role: roleAsst, streaming: true},
	)
	m.curIdx = 1
	m.busy = true
	m.runStart = time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.handleEvent(client.Event{Type: "token", Content: "answer tokens keep arriving in small fragments; "})
	}
}

// BenchmarkAppendReply isolates the delta-accumulation cost on a growing
// reply segment (string += today).
func BenchmarkAppendReply(b *testing.B) {
	for _, chunks := range []int{100, 1000} {
		b.Run(fmt.Sprintf("deltas=%d", chunks), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				msg := &message{}
				appendReply(msg, "seed text\n\n")
				for j := 0; j < chunks; j++ {
					appendReply(msg, "fragment of streamed answer text; ")
				}
			}
		})
	}
}

// BenchmarkPaintCanvas measures the full-frame canvas paint (padding +
// SGR splice) used by light/canvas themes on every View.
func BenchmarkPaintCanvas(b *testing.B) {
	m := newTestModel()
	m.resize(120, 40)
	// Force a canvas-bearing theme so paintCanvas does its real work.
	m.th = themeFrom(emberLight)
	body := strings.Repeat(strings.Repeat("x", 118)+"\n", 38)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.paintCanvas(body)
	}
}

// BenchmarkClampLines measures the line clamp + width re-measure applied to
// freshly rendered blocks and the streaming tail on every flush.
func BenchmarkClampLines(b *testing.B) {
	m := newTestModel()
	m.resize(100, 30)
	s := strings.Repeat(strings.Repeat("wrapped line of transcript output ", 3)+"\n", 60)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.clampLines(s)
	}
}
