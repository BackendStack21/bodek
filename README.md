# bodek

[![CI](https://github.com/BackendStack21/bodek/actions/workflows/ci.yml/badge.svg)](https://github.com/BackendStack21/bodek/actions/workflows/ci.yml)
[![Release](https://github.com/BackendStack21/bodek/actions/workflows/release.yml/badge.svg)](https://github.com/BackendStack21/bodek/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/BackendStack21/bodek.svg)](https://pkg.go.dev/github.com/BackendStack21/bodek)
[![Go Report Card](https://goreportcard.com/badge/github.com/BackendStack21/bodek)](https://goreportcard.com/report/github.com/BackendStack21/bodek)

**A beautiful [Bubble Tea](https://github.com/charmbracelet/bubbletea) terminal interface for the [odek](https://github.com/BackendStack21/odek) agent.**

[📺 Watch intro →](bodek-intro-july-2026.mov)

```
██████   ██████  ██████  ███████ ██   ██
██   ██ ██    ██ ██   ██ ██      ██  ██
██████  ██    ██ ██   ██ █████   █████
██   ██ ██    ██ ██   ██ ██      ██  ██
██████   ██████  ██████  ███████ ██   ██
```

bodek is a **pure front-end**. It launches (or attaches to) an `odek serve`
instance and renders the agent's live stream — reasoning, tokens, tool calls,
approvals, skills, and memory — as a polished TUI. Every bit of agent
behaviour (tools, danger gating, sandbox, skills, memory, sessions) comes from
**odek itself**; bodek never re-implements any of it.

---

## Why a separate front-end?

odek already ships a streaming WebSocket protocol (the one its Web UI speaks).
bodek reuses that exact protocol from the terminal, which means:

- **Zero duplicated logic** — tools, the `danger` approval engine, the Docker
  sandbox, skills, and memory all run inside odek, unchanged.
- **Full fidelity** — token streaming, per-tool activity, and security prompts
  appear in the TUI exactly as the engine emits them.
- **One source of truth** — upgrade odek and bodek gets the new behaviour for
  free.
- **Minimal core footprint** — odek stays deliberately dependency-light, while
  the rich terminal UI lives here with its Bubble Tea, glamour, and other
  front-end dependencies.

```
┌──────────────┐   WebSocket (RFC 6455, JSON)   ┌──────────────────┐
│    bodek     │ ◄────────────────────────────► │   odek serve      │
│ (Bubble Tea) │   tokens · tools · approvals    │  (ReAct engine,   │
│   TUI client │                                 │   tools, sandbox) │
└──────────────┘                                 └──────────────────┘
```

---

## Install

**Prerequisite:** bodek is only the front-end — you also need the `odek`
engine. See [odek's install instructions](https://github.com/BackendStack21/odek)
(any OpenAI-compatible provider key via `ODEK_API_KEY`).

### Prebuilt binaries (bodek only)

Download the latest compiled binary from the
[releases page](https://github.com/BackendStack21/bodek/releases) — archives
are published for Linux, macOS, and Windows (amd64 & arm64), with
`checksums.txt` for verification.

One-liner for Linux / macOS (resolves the latest asset for your platform and
installs into `~/.local/bin`):

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); [ "$ARCH" = "x86_64" ] && ARCH=amd64
URL=$(curl -fsSL https://api.github.com/repos/BackendStack21/bodek/releases/latest \
  | grep browser_download_url | grep "${OS}_${ARCH}" | cut -d '"' -f 4)
curl -fsSL "$URL" | tar -xz bodek && install -m 755 bodek ~/.local/bin/
```

On Windows, download the `windows_amd64` (or `arm64`) `.zip` from the
releases page and put `bodek.exe` on your `PATH`.

### From source

```bash
# Install odek (the engine) and bodek (the TUI)
go install github.com/BackendStack21/odek/cmd/odek@latest
go install github.com/BackendStack21/bodek/cmd/bodek@latest

# Provide an LLM key (any OpenAI-compatible provider)
export ODEK_API_KEY=sk-...

bodek
```

bodek looks for `odek` on your `PATH`. To point at a specific binary use
`--odek-bin`, or skip spawning entirely with `--url`.

---

## Usage

```bash
bodek                                             # launch odek serve and start chatting
bodek --sandbox                                   # run tool calls inside odek's Docker sandbox
bodek --url 'http://127.0.0.1:8080/?token=…'      # attach with the token URL odek serve printed
bodek --url http://127.0.0.1:8080 --token d3adb33f  # attach with an explicit token
bodek --odek-bin ./odek                           # use a specific odek binary
bodek --mouse                                     # enable mouse wheel scrolling (blocks text selection)
bodek -- --prompt-caching                         # pass extra flags through to `odek serve`
bodek version                                     # print the bodek version
bodek upgrade                                     # download and install the latest release
```

`odek serve` protects its WebSocket and REST APIs with a per-instance token it
prints to stderr at startup (`odek serve ⚡  http://…/?token=…`). When bodek
spawns the server it picks the token up automatically; when you attach to one
that's already running, paste the token URL into `--url` or pass the token
itself via `--token`. Older odek versions without token enforcement are
detected and connected to as before.

Configuration (model, base URL, API key, MCP servers, memory, skills) is read
by `odek serve` from its usual chain — `~/.odek/config.json` → `./odek.json` →
`ODEK_*` env vars — so bodek inherits whatever you've already set up.

### Key bindings

| Key | Action |
|-----|--------|
| `⏎` | Send the prompt (queues it while a turn is running) |
| `/` | Open the command palette (see below) |
| `@` | Attach a file (see below) |
| `^R` | Browse & resume saved sessions |
| `^O` | Switch the model |
| `^T` | Toggle extended thinking for the next turn |
| `^J` | Insert a newline in the input |
| `^L` | Clear the conversation |
| `Esc` | Cancel the running turn (queued prompts return to the input) |
| `↑` / `↓` / `PgUp` / `PgDn` / `^U` / `^D` | Scroll the transcript (arrows at the input's edge lines) |
| `^P` / `^N` | Recall previous prompts (prompt history) |
| `^G` / `End` (empty input) | Jump to the latest output |
| `wheel` (with `--mouse`) | Scroll the transcript |
| `r` (when disconnected) | Retry the connection |
| `^C` | Quit |

Prompts sent while a turn is running are **queued** and sent automatically
when the turn ends — the footer shows how many are waiting. While the
transcript is scrolled up mid-run, the footer flags `↓ new output`; press
`^G` to jump to the latest. If the connection drops, bodek retries with
backoff and, after giving up, keeps your draft and offers a manual retry
on `r`.

### Commands (`/`)

Type `/` at the start of the input for a command palette. `↑`/`↓` to choose,
`⇥` to complete, `⏎` to run, `esc` to dismiss. You can also just type the full
command and press `⏎`.

| Command | Action |
|---------|--------|
| `/help` | Show available commands and key bindings |
| `/clear` | Clear the conversation |
| `/stats` | Show session metrics, cost, cache & link status |
| `/sessions` | Browse, search, pin, rename, export & resume sessions |
| `/model [name]` | Switch model (opens a picker with no argument) |
| `/thinking [on\|off]` | Toggle extended thinking for the next turn |
| `/cancel` | Cancel the running turn |
| `/attach <path>` | Stage a file to send with the next prompt (5 MB each, 10 MB total) |
| `/unattach [name]` | Drop staged files (all when no name given) |
| `/quit` | Exit bodek |

### File attachments (`@`)

Type `@` to attach a file. bodek searches the working tree and shows a
completion popup; `↑`/`↓` to choose, `⏎` or `⇥` to insert, `esc` to dismiss.

```
> summarize @internal/client/client.go and explain the protocol
```

odek resolves and inlines the file content **server-side** (wrapped in its
untrusted-content boundary), so attachments go through the same security model
as any other external input — bodek doesn't special-case them. (Saved sessions
are resumed via `/sessions` or `^R`, not `@`.)

When the agent requests approval for a dangerous operation, pick an outcome
from the panel and confirm — typing never answers by accident:

| Key | Action |
|-----|--------|
| `↑` / `↓` (or `←` / `→`) | Move the highlight (Approve / Deny / Trust class when offered) |
| `⏎` | Confirm the highlighted option |
| `Esc` | Deny (abort) |
| `Tab` | Expand/collapse the full command & description text |
| `PgUp` / `PgDn` / `^U` / `^D` | Scroll the transcript while the panel is open |

After three same-class approvals inside a minute the server engages **friction
mode**: the panel shows the recent-approval count, the trust shortcut is
withdrawn, and approving requires typing the literal word `approve` and
pressing `⏎` (a mistyped word resets — retyping is the point). Denying stays
one `Esc`.

---

## What you see

- **Streaming answers** rendered as Markdown ([glamour](https://github.com/charmbracelet/glamour)).
- **Tool activity** — every `tool_call`/`tool_result` shown live with a glyph
  per tool, a spinner, an argument preview, and a result excerpt rendered as a
  tree (`⎿`) — multi-line, blank-stripped, capped with a `+N more lines`
  footer, and tinted with a `✗` when the call fails.
- **Sub-agents** — delegations are labelled and their `subagent_log` activity
  nests beneath the delegating call, so a sub-agent's progress reads as its own
  branch of the step tree.
- **Security approvals** — odek's `danger` engine prompts surface as an inline
  panel; your answer is sent straight back over the socket.
- **Live reasoning** — the model's pre-tool thinking streams in dimmed text,
  with a running elapsed timer and cycling status while it works.
- **Command palette (`/`)** and **file attachments (`@`)** — live, navigable popups.
- **Context-aware progress** — while the agent works, a status line just
  above the input (right below your last message) shows what it's actually
  doing (`🧪 running tests`, `📖 reading client.go`, `🚀 pushing`) with a live
  elapsed timer.
- **Session browser** (`^R`) — resume, replay, delete, pin (`p`), rename
  (`r`), export a transcript (`e` markdown, `E` JSON), and search server-side
  (`/`); `n` loads the next page. Resuming sends a `session_switch` so the
  server-side memory buffer is restored before you type.
- **Auto-reconnect** — if the socket drops, bodek redials with backoff
  (500ms → 8s, 5 attempts) and re-adopts the session over the fresh socket;
  only a server that stays down leaves the `disconnected` badge.
- **Model switcher** (`^O`) — change the model for the next turn. The picker
  merges the server's configured model with its built-in profile catalog
  (`/api/profiles`), each annotated with its context window.
- **Live server snapshot** — a 25s heartbeat measures WebSocket round-trip
  latency and refreshes server uptime, connection count, and streaming state
  (the ⚡ badge beside the model); `/stats` surfaces the full link row.
- **Cancellation** (`Esc`) — abort a running turn via odek's cancel API.
- **Sandbox aware** — the header shows `🛡 sandboxed` or `⚠ host access`; pass
  `--sandbox` to run tool calls inside odek's Docker isolation.
- **Telemetry** — session token totals and last-turn latency in the chrome.
- **Cost tracking** — when odek has token prices configured (limits), the
  header shows the running session spend, each turn footer its estimated
  cost, and `/stats` rolls up the session (with the `max_cost_usd` cap when
  set); hidden entirely otherwise.
- **Fluent by default** — gradient wordmark and hairline, smooth braille
  spinner, smart autoscroll that never yanks you while you read history, and a
  scroll-position indicator.
- **Engine notices** — skill loads, memory merges, and agent signals appear as
  quiet status lines.
- **Version display** — the header shows bodek's own version next to the logo
  and the spawned odek's version next to the model name.
- **Update hint** — at startup, a quiet note appears when a newer bodek release
  is available (`bodek upgrade` installs it; dev builds are never nagged).

---

## Development

```bash
make build      # → bin/bodek
make run        # build and launch
make test       # go test -race ./...
make cover      # coverage report for internal packages
make lint       # golangci-lint (if installed)
make vet
make tidy
```

Continuous integration runs build, `go vet`, `golangci-lint`, and the race-
enabled test suite on every push (see [`.github/workflows`](.github/workflows)).
Tagged releases (`vX.Y.Z`) are built and published automatically by
[GoReleaser](https://goreleaser.com).

Project layout:

| Path | Responsibility |
|------|----------------|
| `cmd/bodek` | CLI entry point: flags, lifecycle, wiring |
| `internal/server` | Launch / attach to `odek serve`, resolve the auth token |
| `internal/client` | odek serve WebSocket protocol (transport + REST + decoding) |
| `internal/tokens` | Local persistence of per-session auth tokens |
| `internal/tui` | The Bubble Tea model, update loop, panels, and view |

### Architecture & testing

bodek is a pure client, so it is highly testable: the WebSocket protocol, REST
endpoints, token store, and the full Bubble Tea update/view loop are exercised
by unit and integration tests against an in-process `odek serve` stand-in.
Internal-package statement coverage is **~99%** (client 100%, tui 99%, tokens
98%, server 95% — the remainder is unreachable OS-error handling).

---

## License

MIT — see [LICENSE](LICENSE).
