package client

import (
	"encoding/json"
	"testing"
)

// turn_started (≥ the turn_started protocol): every turn announces itself
// with an identity and provenance; turn_id also annotates streamed frames
// (R3). The decoder must surface both fields.
func TestTurnStartedDecode(t *testing.T) {
	var ev Event
	err := json.Unmarshal([]byte(`{
		"type": "turn_started",
		"turn_id": "t_0123abcd",
		"session_id": "s1",
		"initiated": "system",
		"model": "glm-5.3-flash"
	}`), &ev)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "turn_started" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.TurnID != "t_0123abcd" {
		t.Errorf("TurnID = %q", ev.TurnID)
	}
	if ev.SessionID != "s1" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.Initiated != "system" {
		t.Errorf("Initiated = %q", ev.Initiated)
	}
	if ev.Model != "glm-5.3-flash" {
		t.Errorf("Model = %q", ev.Model)
	}
}

// R3: streamed frames carry turn_id while a turn is live.
func TestTurnIDOnStreamedFrames(t *testing.T) {
	var ev Event
	if err := json.Unmarshal([]byte(`{"type":"tool_call","name":"shell","data":"{}","turn_id":"t_0123abcd"}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.TurnID != "t_0123abcd" {
		t.Errorf("TurnID on streamed frame = %q", ev.TurnID)
	}
}
