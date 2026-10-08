package client

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	ws "golang.org/x/net/websocket"
)

func TestDecodeAnswerSupersededAndVerified(t *testing.T) {
	var ev Event
	if err := json.Unmarshal([]byte(`{"type":"answer_superseded","reason":"verify_retry","cycle":2,"turn_id":"t_1"}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "answer_superseded" || ev.Reason != "verify_retry" || ev.Cycle != 2 {
		t.Fatalf("bad answer_superseded decode: %+v", ev)
	}
	ev = Event{}
	if err := json.Unmarshal([]byte(`{"type":"done","verified":"fail"}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Verified != "fail" {
		t.Fatalf("done.verified = %q, want fail", ev.Verified)
	}
}

func TestDecodeRuntimeFrame(t *testing.T) {
	ev, ok := decodeRuntimeFrame([]byte(`{"type":"runtime_event","turn_id":"t_1","event":{"schema":"odek.event/v1","type":"verification_completed","data":{"verdict":"fail","cycles_used":1}}}`))
	if !ok || ev.Type != "runtime_event" || ev.TurnID != "t_1" || ev.Runtime == nil {
		t.Fatalf("bad runtime decode: %+v ok=%v", ev, ok)
	}
	if ev.Runtime.Type != "verification_completed" || ev.Runtime.Data["verdict"] != "fail" {
		t.Fatalf("bad runtime payload: %+v", ev.Runtime)
	}
	for _, bad := range []string{
		`{"type":"subagent_log","event":{"x":1}}`, // object event on a non-runtime frame stays malformed
		`{"type":"runtime_event"}`,                // no record
		`not json`,
	} {
		if _, ok := decodeRuntimeFrame([]byte(bad)); ok {
			t.Errorf("decodeRuntimeFrame(%s) accepted a malformed frame", bad)
		}
	}
}

// A runtime_event frame used to fail the Event decode (its object "event"
// key collides with the string SubType) and was silently dropped.
func TestRuntimeEventFrameReachesEvents(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(c *ws.Conn) {
		_ = ws.Message.Send(c, `{"type":"runtime_event","event":{"type":"verification_started"}}`)
		_ = ws.Message.Send(c, `{"type":"subagent_log","event":"spawned","name":"SA1"}`)
		time.Sleep(200 * time.Millisecond)
	}))
	cl, _ := newTestServer(t, mux)
	for i, want := range []string{"runtime_event", "subagent_log"} {
		select {
		case ev := <-cl.Events:
			if ev.Type != want {
				t.Fatalf("event %d = %q, want %q", i, ev.Type, want)
			}
			if want == "runtime_event" && (ev.Runtime == nil || ev.Runtime.Type != "verification_started") {
				t.Fatalf("runtime payload = %+v", ev.Runtime)
			}
			if want == "subagent_log" && ev.SubType != "spawned" {
				t.Fatalf("subagent_log SubType = %q", ev.SubType)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}
