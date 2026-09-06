package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// protocolFloor is one odek serve contract bodek already paints. Below
// the floor the surface is silently missing — we say so once at connect.
type protocolFloor struct {
	name string
	min  [3]int // major.minor.patch
}

var protocolFloors = []protocolFloor{
	{name: "jobs", min: [3]int{1, 38, 0}},
	{name: "wake turns", min: [3]int{1, 40, 0}},
	{name: "keepalive", min: [3]int{2, 1, 0}},
	{name: "windowTokens", min: [3]int{2, 3, 0}},
}

// parseOdekVersion turns "v1.40.0" / "1.40" / "odek 2.3.0" into major.minor.patch.
func parseOdekVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "odek ")
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, " -+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || parts[0] == "" {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return false
}

// missingContracts lists floors the running engine is below.
func missingContracts(version string) []string {
	v, ok := parseOdekVersion(version)
	if !ok {
		return nil
	}
	var missing []string
	for _, f := range protocolFloors {
		if versionLess(v, f.min) {
			missing = append(missing, fmt.Sprintf("%s ≥ v%d.%d", f.name, f.min[0], f.min[1]))
		}
	}
	return missing
}

// protocolNote posts a single quiet connect-time card. Empty/unknown
// versions stay silent — attach mode often has no binary to ask.
func (m *Model) protocolNote() tea.Cmd {
	if m.odekVersion == "" {
		return nil
	}
	miss := missingContracts(m.odekVersion)
	if len(miss) == 0 {
		return nil
	}
	return m.transientNoteCmd("odek " + strings.TrimPrefix(m.odekVersion, "v") +
		" — missing " + strings.Join(miss, " · "))
}
