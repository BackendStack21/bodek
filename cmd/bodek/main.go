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
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/BackendStack21/bodek/internal/client"
	"github.com/BackendStack21/bodek/internal/server"
	"github.com/BackendStack21/bodek/internal/settings"
	"github.com/BackendStack21/bodek/internal/tui"
	"github.com/BackendStack21/bodek/internal/workspace"
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
	bel       bool   // terminal bell on turn completion / approval waiting
	notify    bool   // desktop notifications (OSC 9) on the same events
	plain     bool   // linear rendering mode (no alt-screen)
	theme     string // startup palette override (empty = BODEK_THEME / settings)
	verbosity string // startup noise dial: quiet, normal, detailed (empty = normal)
	fresh     bool   // --new: skip last-session resume
	extraArgs []string

	persist settings.Settings // the loaded file, re-saved when /theme switches
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var cfg config
	// Persisted preferences seed the flag defaults, so a choice made once
	// (via /theme or by hand) survives relaunches; explicit flags still
	// win. A broken file warns but never blocks startup.
	st, err := settings.Load()
	if err != nil {
		if output != nil {
			_, _ = fmt.Fprintf(output, "bodek: ignoring settings file: %v\n", err)
		}
		st = settings.Settings{}
	}
	cfg.persist = st
	fs := flag.NewFlagSet("bodek", flag.ContinueOnError)
	if output != nil {
		fs.SetOutput(output)
	}
	fs.StringVar(&cfg.url, "url", "", "attach to an already-running odek serve URL (e.g. http://127.0.0.1:8080/?token=…)")
	fs.StringVar(&cfg.token, "token", "", "WS auth token for an attached odek serve (as printed at its startup)")
	fs.BoolVar(&cfg.sandbox, "sandbox", false, "run tool calls inside odek's Docker sandbox")
	fs.StringVar(&cfg.bin, "odek-bin", "", "path to the odek binary to spawn (default: odek on PATH)")
	themeDefault := st.Theme
	if os.Getenv("BODEK_THEME") != "" {
		themeDefault = "" // the env override wins over the persisted file
	}
	fs.StringVar(&cfg.theme, "theme", themeDefault, "color theme: ember-dark, ember-light, high-contrast, classic (default: BODEK_THEME, then the settings file)")
	fs.BoolVar(&cfg.bel, "bel", st.Bool(st.Bell, true), "ring the terminal bell when a turn completes or an approval is waiting (--bel=false mutes)")
	fs.BoolVar(&cfg.notify, "notify", st.Bool(st.Notify, false), "raise desktop notifications (OSC 9) on turn completion and approvals")
	fs.BoolVar(&cfg.plain, "plain", st.Bool(st.Plain, false), "linear mode: no alt-screen, transcript printed to scrollback (screen readers, pipes, logs)")
	verbDefault := st.Verbosity
	fs.StringVar(&cfg.verbosity, "verbosity", verbDefault, "noise dial: quiet (info notes hidden, compact steps), normal, detailed (steps expand) — /verbosity switches at runtime and persists")
	fs.BoolVar(&cfg.fresh, "new", false, "start a fresh session (skip last-session resume for this directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: bodek [options] [-- <odek serve flags>]\n\n")
		_, _ = fmt.Fprintf(fs.Output(), "A terminal interface for the odek agent.\n\n")
		_, _ = fmt.Fprintf(fs.Output(), "Commands:\n")
		_, _ = fmt.Fprintf(fs.Output(), "  version   print the bodek version\n")
		_, _ = fmt.Fprintf(fs.Output(), "  upgrade   download and install the latest release\n\n")
		_, _ = fmt.Fprintf(fs.Output(), "Options:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(fs.Output(), "\nExamples:\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek                                             # spawn odek serve and resume this directory's last session\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --new                                       # start a fresh session\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --sandbox                                   # spawn odek serve with Docker sandbox\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --url 'http://127.0.0.1:8080/?token=…'      # attach with the token URL odek serve printed\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --url http://127.0.0.1:8080 --token d3adb33f  # attach with an explicit token\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --notify                                    # desktop notifications on turn/approval events\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --theme ember-light                         # start with a specific theme (/theme switches at runtime)\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek --plain                                     # linear mode: transcript to scrollback (pipes, a11y)\n")
		_, _ = fmt.Fprintf(fs.Output(), "  bodek -- --prompt-caching                         # pass extra flags to odek serve\n")
	}
	if err := fs.Parse(args); err != nil {
		fs.Usage()
		return config{}, err
	}
	cfg.extraArgs = fs.Args()
	return cfg, nil
}

func buildProgramOptions(plain bool) []tea.ProgramOption {
	// Filter first so Shift+Enter CSI sequences become a KeyMsg before
	// any other option's machinery sees them.
	opts := []tea.ProgramOption{tea.WithFilter(tui.FilterShiftEnter)}
	if !plain {
		// The alt-screen transcript is the default surface. Linear mode
		// (--plain) stays on the main buffer so printed lines persist in
		// the terminal's native scrollback. Mouse reporting is always on
		// in the alt-screen so the wheel scrolls the transcript; --plain
		// keeps the terminal's own scrollback instead.
		opts = append(opts, tea.WithAltScreen(), tea.WithMouseCellMotion())
	}
	return opts
}

// applyNoColor honors https://no-color.org deterministically: when the
// variable is set, the whole palette (EMBER gradients included) degrades
// to plain text regardless of what the terminal advertises.
func applyNoColor() {
	if os.Getenv("NO_COLOR") != "" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
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
	applyNoColor()

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
	srv, err := connectWithDiagnosis(server.Options{
		URL:       cfg.url,
		Token:     cfg.token,
		Bin:       cfg.bin,
		Sandbox:   cfg.sandbox,
		ExtraArgs: cfg.extraArgs,
		Stderr:    serverErr,
	}, os.Stderr, os.Stdin)
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
		Bell:        cfg.bel,
		Notify:      cfg.notify,
		Plain:       cfg.plain,
		Theme:       cfg.theme,
		Verbosity:   cfg.verbosity,
		Workspace:   workspace.Open(),
		Fresh:       cfg.fresh,
		OnThemeChange: func(name string) error {
			cfg.persist.Theme = name
			return settings.Save(cfg.persist)
		},
		OnVerbosityChange: func(name string) error {
			cfg.persist.Verbosity = name
			return settings.Save(cfg.persist)
		},
		Reconnect: func() (*client.Client, error) {
			return client.Dial(srv.WSURL, srv.Origin, srv.BaseURL, srv.Token)
		},
	})

	// Clear leftover keyboard modes. Shift+Enter is enabled from Model.Init
	// after the alt screen is up — a pre-Run enable is wiped by DECSET 1049.
	tui.RestoreEnhancedKeys()
	p := tea.NewProgram(model, buildProgramOptions(cfg.plain)...)
	defer tui.RestoreEnhancedKeys()
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
		tui.RestoreEnhancedKeys()
		os.Exit(0)
	}()
}
