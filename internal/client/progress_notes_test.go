package client

import (
	"net/http"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

func TestShortAssistantNoteDeliveredBeforeNextFrame(t *testing.T) {
	for _, kind := range []string{"token", "token_delta"} {
		t.Run(kind, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
				_ = ws.JSON.Send(c, Event{Type: kind, Content: "I’ll inspect the files.", TurnID: "turn-note"})
				// The model/tool has not produced another frame yet. Receiving
				// this ack proves delivery without a trailing delta or done.
				var ack []byte
				if err := ws.Message.Receive(c, &ack); err != nil {
					return
				}
				_ = ws.JSON.Send(c, Event{Type: "tool_call", Name: "read_file", TurnID: "turn-note"})
			}))
			cl, _ := newTestServer(t, mux)
			select {
			case ev := <-cl.Events:
				if ev.Type != kind || ev.Content != "I’ll inspect the files." || ev.TurnID != "turn-note" {
					t.Fatalf("wrong progress note: %+v", ev)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("short assistant note waited for a future frame")
			}
			if err := cl.SendPrompt("ack", PromptOpts{}); err != nil {
				t.Fatal(err)
			}
			select {
			case ev := <-cl.Events:
				if ev.Type != "tool_call" {
					t.Fatalf("note repeated or overtaken by tool: %+v", ev)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("tool event was not delivered")
			}
		})
	}
}
