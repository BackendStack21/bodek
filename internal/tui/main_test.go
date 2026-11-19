package tui

import (
	"os"
	"testing"
	"time"
)

// launcherEnv turns the test binary into a stand-in for the desktop's URL
// launcher. A Go test binary ignores positional arguments and would run
// the whole suite again — and with it the test that spawned it — so the
// stand-in must exit before the suite starts.
const launcherEnv = "BODEK_TEST_LAUNCHER"

func TestMain(m *testing.M) {
	switch os.Getenv(launcherEnv) {
	case "ok":
		os.Exit(0)
	case "fail":
		os.Exit(3)
	case "slow":
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
