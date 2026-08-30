package tui

import "testing"

// Esc is the easiest key to hit by accident, and it used to kill the run
// outright. The cancel rides the same two-step confirm gate as every other
// destructive action: esc arms, y fires, any other key disarms.

func TestEscWhileBusyArmsCancelConfirm(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"

	m.Update(key("esc"))

	if m.confirm != confirmCancel {
		t.Fatalf("esc while busy did not arm confirmCancel: %v", m.confirm)
	}
	if !m.busy {
		t.Fatal("arming the gate cancelled the run outright")
	}
	if m.status == "cancelling" {
		t.Error("arming the gate already entered the cancelling state")
	}
}

func TestEscThenYConfirmsCancel(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"

	m.Update(key("esc"))
	if _, cmd := m.Update(key("y")); cmd == nil {
		t.Fatal("y on an armed cancel gate returned no command")
	}
	if m.status != "cancelling" {
		t.Errorf("y did not fire the cancel: status %q", m.status)
	}
}

func TestEscThenOtherKeyDisarms(t *testing.T) {
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"

	m.Update(key("esc"))
	m.Update(key("n"))

	if m.confirm != confirmNone {
		t.Errorf("any other key must disarm the gate, got %v", m.confirm)
	}
	if !m.busy || m.status == "cancelling" {
		t.Error("disarming the gate cancelled the run")
	}
}

func TestEscWhenIdleNeverArms(t *testing.T) {
	m := newTestModel()

	m.Update(key("esc"))

	if m.confirm != confirmNone {
		t.Errorf("esc while idle armed a confirm: %v", m.confirm)
	}
}

func TestCancelConfirmAfterRunSettles(t *testing.T) {
	// The run settles while the gate is armed: y must degrade to the benign
	// "nothing to cancel" note, never fire a stale cancel.
	m := newTestModel()
	busyTurn(m)
	m.sessionID = "s1"
	m.Update(key("esc"))
	m.busy = false

	m.Update(key("y"))

	if m.confirm != confirmNone {
		t.Errorf("gate still armed after firing: %v", m.confirm)
	}
	if n := len(m.notices); n == 0 || m.notices[n-1] != "nothing to cancel" {
		t.Errorf("y after settle = %v, want the nothing-to-cancel note", m.notices)
	}
}
