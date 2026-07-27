// Command bodek is a beautiful terminal interface for the odek agent.
// It launches (or attaches to) an odek serve instance and renders the agent's
// live stream as a Bubble Tea TUI.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
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

type config struct {
	url       string
	token     string
	sandbox   bool
	bin       string
	mouse     bool
	extraArgs []string
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("bodek", flag.ContinueOnError)
	if output != nil {
		fs.SetOutput(output)
	}
	fs.StringVar(&cfg.url, "url", "", "attach to an already-running odek serve URL (e.g. http://127.0.0.1:8080/?token=…)")
	fs.StringVar(&cfg.token, "token", "", "WS auth token for an attached odek serve (as printed at its startup)")
	fs.BoolVar(&cfg.sandbox, "sandbox", false, "run tool calls inside odek's Docker sandbox")
	fs.StringVar(&cfg.bin, "odek-bin", "", "path to the odek binary to spawn (default: odek on PATH)")
	fs.BoolVar(&cfg.mouse, "mouse", false, "enable mouse wheel scrolling (disables native text selection/copy)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: bodek [options] [-- <odek serve flags>]\n\n")
		_, _ = fmt.Fprintf(fs.Output(), "A terminal interface for the odek agent.\n\n")
		_, _ = fmt.Fprintf(fs.Output(), "Commands:\n")
		_, _ = fmt.Fprintf(fs.Output(), "  version   print the bodek version\n")
		_, _ = fmt.Fprintf(fs.Output(), "  upgrade   download and install the latest release\n\n")
		_, _ = fmt.Fprintf(fs.Output(), "Options:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(fs.Output(), "\nExamples:\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek                                             # spawn odek serve and start chatting\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --sandbox                                   # spawn odek serve with Docker sandbox\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --url 'http://127.0.0.1:8080/?token=…'      # attach with the token URL odek serve printed\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --url http://127.0.0.1:8080 --token d3adb33f  # attach with an explicit token\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --mouse                                     # enable mouse wheel scrolling (blocks text selection)\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek -- --prompt-caching                         # pass extra flags to odek serve\n")
	}
	if err := fs.Parse(args); err != nil {
		fs.Usage()
		return config{}, err
	}
	cfg.extraArgs = fs.Args()
	return cfg, nil
}

func buildProgramOptions(mouse bool) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}

func run() error {
	// Bare subcommands (`bodek version`, `bodek upgrade`) bypass the TUI
	// entirely, so they run before flag parsing.
	if handled, err := handleSubcommand(os.Args[1:], os.Stdout); handled {
		return err
	}

	cfg, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		return err
	}

	// A spawned `odek serve` logs to stderr. Routing that to our own terminal
	// would corrupt the Bubble Tea alt-screen (stray writes desync the diff
	// renderer), so we capture it to a log file and keep a short in-memory tail
	// to surface if the server dies during startup. When attaching to an
	// external server (--url) there is no subprocess, so nothing is captured.
	var (
		logTail *ringWriter
		logPath string
	)
	// io.Writer(io.Discard) keeps the interface type (serverErr is reassigned to
	// io.MultiWriter / *ringWriter below) without the redundant typed-var
	// declaration that staticcheck's QF1011 flags.
	serverErr := io.Writer(io.Discard)
	if cfg.url == "" {
		logTail = newRingWriter(50)
		if f, path, closeLog := openServerLog(); path != "" {
			defer closeLog()
			logPath = path
			serverErr = io.MultiWriter(f, logTail)
		} else {
			serverErr = logTail
		}
	}

	// Spawn or attach to the odek serve backend.
	srv, err := server.Connect(server.Options{
		URL:       cfg.url,
		Token:     cfg.token,
		Bin:       cfg.bin,
		Sandbox:   cfg.sandbox,
		ExtraArgs: cfg.extraArgs,
		Stderr:    serverErr,
	})
	if err != nil {
		if logTail != nil {
			if tail := strings.TrimSpace(logTail.String()); tail != "" {
				return fmt.Errorf("%w\n\nodek serve output:\n%s", err, tail)
			}
		}
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
		Sandbox:     cfg.sandbox,
		CWD:         cwd,
		LogPath:     logPath,
		OdekVersion: srv.Version,
		Version:     currentVersion(),
	})

	// Mouse reporting enables wheel scrolling in the transcript, but it also
	// captures the terminal mouse and blocks native click-drag text selection
	// and copy. Keep it off by default so users can copy freely; enable it only
	// when explicitly requested with --mouse.
	p := tea.NewProgram(model, buildProgramOptions(cfg.mouse)...)
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
