// Command bodek is a beautiful terminal interface for the odek agent.
// It launches (or attaches to) an odek serve instance and renders the agent's
// live stream as a Bubble Tea TUI.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/server"
	"github.com/BackendStack21/bodek/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bodek:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		url     = flag.String("url", "", "attach to an already-running odek serve URL (e.g. http://127.0.0.1:8080)")
		sandbox = flag.Bool("sandbox", false, "run tool calls inside odek's Docker sandbox")
		bin     = flag.String("odek-bin", "", "path to the odek binary to spawn (default: odek on PATH)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: bodek [options] [-- <odek serve flags>]\n\n")
		fmt.Fprintf(os.Stderr, "A terminal interface for the odek agent.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  bodek                               # spawn odek serve and start chatting\n")
		fmt.Fprintf(os.Stderr, "  bodek --sandbox                     # spawn odek serve with Docker sandbox\n")
		fmt.Fprintf(os.Stderr, "  bodek --url http://127.0.0.1:8080   # attach to a running odek serve\n")
		fmt.Fprintf(os.Stderr, "  bodek -- --prompt-caching           # pass extra flags to odek serve\n")
	}
	flag.Parse()

	extraArgs := flag.Args()

	// Spawn or attach to the odek serve backend.
	srv, err := server.Connect(server.Options{
		URL:       *url,
		Bin:       *bin,
		Sandbox:   *sandbox,
		ExtraArgs: extraArgs,
		Stderr:    os.Stderr,
	})
	if err != nil {
		return err
	}
	defer srv.Stop()

	// Dial the WebSocket and start streaming events.
	cl, err := client.Dial(srv.WSURL, srv.Origin, srv.BaseURL, srv.Token)
	if err != nil {
		return err
	}
	defer func() { _ = cl.Close() }()

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// Gracefully shutdown on SIGINT/SIGTERM so the server gets a clean exit.
	setupSignalHandler(srv, cl)

	model := tui.New(cl, tui.Options{
		Sandbox: *sandbox,
		CWD:     cwd,
	})

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI exited: %w", err)
	}
	return nil
}

func setupSignalHandler(srv *server.Conn, cl *client.Client) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		_ = cl.Close()
		srv.Stop()
		os.Exit(0)
	}()
}
