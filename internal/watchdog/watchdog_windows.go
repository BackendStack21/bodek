//go:build windows

package watchdog

import "context"
import "time"

const supported = false

const (
	sigInterrupt = 0
	sigKill      = 0
)

func processAlive(int) bool { return false }
func signalGroup(int, int)  {}

var _ = time.Second
