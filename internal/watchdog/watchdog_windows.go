//go:build windows

package watchdog

import "time"

const supported = false

const (
	sigInterrupt = 0
	sigKill      = 0
)

func processAlive(int) bool { return false }
func startToken(int) int64  { return 0 }
func signalGroup(int, int)  {}

var _ = time.Second
