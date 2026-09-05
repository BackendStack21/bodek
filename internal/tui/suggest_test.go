package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
)

// execAll drives a cmd tree the way the tea runtime would, flattening
// batches so closures actually run.
func execAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			execAll(c)
		}
	}
}

// applyQuick runs cmd (and BatchMsg children) that finish immediately,
// skipping tea.Tick waits such as noticeSweep.
func applyQuick(m *Model, cmd tea.Cmd) {
	for _, msg := range collectQuick(cmd) {
		m.Update(msg)
	}
}

func collectQuick(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var out []tea.Msg
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				out = append(out, collectQuick(c)...)
			}
		default:
			if msg != nil {
				out = append(out, msg)
			}
		}
	case <-time.After(200 * time.Millisecond):
	}
	return out
}

// TestSkillSuggestionCard pins the suggestion flow end to end: a
// skill_event "suggested" shows the passive card, alt+s sends the
// skill_prompt_response ack (alt-x skips), typing never answers, and the
// next prompt clears the card.
func TestSkillSuggestionCard(t *testing.T) {
	m, _, skills := approvalRecorder(t)

	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested",
		SkillName: "deploy-helper", Detail: "multi-step deploy observed"})
	if m.skillSuggest == nil {
		t.Fatal("suggested event did not arm the card")
	}
	out := plain(m.View())
	for _, want := range []string{"✦", "deploy-helper", "alt+s", "alt+x"} {
		if !strings.Contains(out, want) {
			t.Errorf("suggestion card missing %q:\n%s", want, out)
		}
	}

	// Typing is never captured by the card.
	m.Update(key("s"))
	if m.skillSuggest == nil {
		t.Fatal("a bare letter answered the suggestion")
	}

	// alt+s saves: the ack reaches the socket and the card clears.
	// execAll flattens the batch (note timer + sender) the runtime would run.
	_, cmd := m.Update(key("alt+s"))
	execAll(cmd)
	select {
	case a := <-skills:
		if a != "save" {
			t.Errorf("skill ack = %q, want save", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no skill_prompt_response sent")
	}
	if m.skillSuggest != nil {
		t.Error("card did not clear after answering")
	}

	// A second suggestion clears with the next prompt; alt+x skips.
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested", SkillName: "x2"})
	if m.skillSuggest == nil {
		t.Fatal("second suggestion not armed")
	}
	m.submit() // prompt clears the card's window
	if m.skillSuggest != nil {
		t.Error("card survived into the next prompt")
	}
	// Non-suggested skill events never arm the card.
	m.handleEvent(client.Event{Type: "skill_event", SubType: "saved", SkillName: "x3"})
	if m.skillSuggest != nil {
		t.Error("non-suggested skill event armed the card")
	}
}

// A failed skill_prompt_response write must not tear the turn down the
// way errMsg does — the ack is local; the engine is still running.
func TestSkillSendFailureDoesNotEndTurn(t *testing.T) {
	m, _, _ := approvalRecorder(t)
	busyTurn(m)
	m.handleEvent(client.Event{Type: "approval_request", ID: "apr-1",
		Risk: "shell_exec", Command: "rm x"})
	m.handleEvent(client.Event{Type: "skill_event", SubType: "suggested",
		SkillName: "deploy-helper"})
	if err := m.cl.Close(); err != nil {
		t.Fatal(err)
	}

	applyQuick(m, m.answerSuggestion("skip"))

	if !m.busy {
		t.Error("skill send failure must not end the turn")
	}
	if m.status == "error" {
		t.Errorf("status = %q, send-fail must not take the error path", m.status)
	}
	if m.curApproval() == nil {
		t.Fatal("skill send failure must not drop pending approvals")
	}
	if m.skillSuggest == nil || m.skillSuggest.SkillName != "deploy-helper" {
		t.Fatal("failed send must restore the suggestion chip")
	}
	found := false
	for _, n := range m.notices {
		if strings.Contains(n, "skill send failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a skill send-failed note, got %v", m.notices)
	}
}

// A send-fail that lands after the turn already ended must not re-arm
// the chip — the same contract as a late approvalSendErrMsg.
func TestSkillSendErrAfterDisconnectDoesNotRestore(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.handleEvent(client.Event{Type: client.EventDisconnected})
	if m.busy {
		t.Fatal("precondition: disconnect must end the turn")
	}

	m.Update(skillSendErrMsg{
		ev:  client.Event{Type: "skill_event", SkillName: "deploy-helper"},
		err: errors.New("send failed"),
	})
	if m.skillSuggest != nil {
		t.Fatal("late send-fail must not re-arm a dead skill chip")
	}
	if m.busy {
		t.Error("late send-fail must not reopen the turn")
	}
}
