package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestWheelBurstNeverTypesIntoComposer reproduces the Logitech wheel report:
// a fast wheel burst split by the terminal arrives as partial report tails —
// including Alt-prefixed heads (ESC consumed as alt+[) and tails missing
// their button digits — and splices codes like ";1;1M" repeatedly into the
// composer. None of these shapes may reach the draft.
func TestWheelBurstNeverTypesIntoComposer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		msg   tea.KeyMsg
		wantN int // runes that must survive (0 = fully stripped)
	}{
		{"alt head tail", tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune(";1;1M")}, 0},
		{"plain partial tail", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(";5;13M")}, 0},
		{"repeated tails", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("64;5;13M65;5;13M66;5;13M")}, 0},
		{"bracketed mid-string", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello[<64;5;13M")}, 5},
		{"typed text survives", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M;1;1 hello 1M")}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := FilterShiftEnter(nil, tc.msg)
			if out == nil {
				if tc.wantN == 0 {
					return
				}
				t.Fatalf("fully stripped, want %d runes to survive", tc.wantN)
			}
			km, ok := out.(tea.KeyMsg)
			if !ok {
				t.Fatalf("unexpected message type %T", out)
			}
			if tc.wantN == 0 {
				t.Fatalf("mouse-shaped runes leaked into a key message: %q", string(km.Runes))
			}
			if tc.wantN >= 0 && len(km.Runes) != tc.wantN {
				t.Fatalf("kept %d runes %q, want %d", len(km.Runes), string(km.Runes), tc.wantN)
			}
			if tc.wantN < 0 && !strings.Contains(string(km.Runes), "M;1;1 hello 1M") {
				t.Fatalf("typed text mangled: %q", string(km.Runes))
			}
		})
	}
}
