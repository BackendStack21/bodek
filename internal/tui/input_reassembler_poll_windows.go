//go:build windows

package tui

import "time"

// waitReadable on Windows has no poll(2); bodek's input reassembler is only
// wired in on non-Windows builds (buildProgramOptions), so this stub is here
// solely to keep the package compiling. It proceeds straight to the read.
func (r *inputReassembler) waitReadable(budget time.Duration) bool {
	_ = budget
	return true
}
