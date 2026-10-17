//go:build windows

package server

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWindowsHelperChild is the stand-in odek: re-exec'd by the tests
// below, it reports readiness and exits 0 on os.Interrupt.
func TestWindowsHelperChild(t *testing.T) {
	if os.Getenv("BODEK_WIN_CHILD") == "" {
		t.Skip("helper process only")
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	fmt.Println("ready")
	select {
	case <-ch:
		os.Exit(0)
	case <-time.After(30 * time.Second):
		os.Exit(3)
	}
}

func startHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsHelperChild$")
	cmd.Env = append(os.Environ(), "BODEK_WIN_CHILD=1")
	setPgroup(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line, _ := bufio.NewReader(out).ReadString('\n')
	if !strings.Contains(line, "ready") {
		t.Fatalf("helper not ready: %q", line)
	}
	return cmd
}

func waitExit(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("child did not exit")
		return nil
	}
}

// The child needs its own console group, or CTRL_BREAK cannot target it.
func TestWindowsSetPgroupNewConsoleGroup(t *testing.T) {
	cmd := exec.Command("cmd")
	setPgroup(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("child not started in a new process group")
	}
}

// signalServer's graceful path reaches a Go child as os.Interrupt, so
// Stop no longer waits out the grace and force-kills on windows.
func TestWindowsGracefulSignalReachesChild(t *testing.T) {
	cmd := startHelper(t)
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)); err != nil {
		t.Skipf("no console to deliver ctrl events: %v", err)
	}
	if err := waitExit(t, cmd); err != nil {
		t.Fatalf("child did not exit cleanly on CTRL_BREAK: %v", err)
	}
	cmd = startHelper(t)
	(&Conn{proc: cmd}).signalServer(syscall.SIGINT)
	if err := waitExit(t, cmd); err != nil {
		t.Fatalf("signalServer did not stop the child gracefully: %v", err)
	}
}

// Closing the guard's job handle (what the OS does when bodek dies)
// terminates the child: no orphaned odek.
func TestWindowsJobKillsChildOnClose(t *testing.T) {
	cmd := startHelper(t)
	c := &Conn{proc: cmd}
	c.guardChild()
	if c.watch == nil {
		t.Fatal("job object guard not installed")
	}
	c.watch()
	_ = waitExit(t, cmd) // fails the test if the child outlives its job

}
