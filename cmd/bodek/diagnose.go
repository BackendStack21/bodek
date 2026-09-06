package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BackendStack21/bodek/internal/server"
	"github.com/BackendStack21/bodek/internal/tui"
)

// connectWithDiagnosis attaches or spawns odek serve. On failure it prints
// one card and, on a TTY, offers a single-key retry. No wizard, no key store.
func connectWithDiagnosis(opts server.Options, stderr io.Writer, stdin io.Reader) (*server.Conn, error) {
	for {
		srv, err := server.Connect(opts)
		if err == nil {
			return srv, nil
		}
		card := server.Diagnose(err, tui.MissingProvider())
		if stderr != nil && card != "" {
			_, _ = fmt.Fprintln(stderr, card)
		}
		if stdin == nil || !isTerminal(stdin) {
			return nil, err
		}
		_, _ = fmt.Fprint(stderr, " ")
		line, readErr := bufio.NewReader(stdin).ReadString('\n')
		if readErr != nil {
			return nil, err
		}
		ans := strings.TrimSpace(strings.ToLower(line))
		if ans == "" || ans == "y" || ans == "retry" {
			continue
		}
		return nil, err
	}
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
