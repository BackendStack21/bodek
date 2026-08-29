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
bodek --bel=false                                 # mute the attention bell (title still updates)
bodek --notify                                    # desktop notifications (OSC 9) on turn/approval events
bodek --theme ember-light                         # start with a theme (/theme switches live)
bodek --plain                                     # linear mode: transcript to scrollback (a11y, pipes)
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

bodek keeps its **own front-end settings** in `~/.bodek/config.json` (override
with `BODEK_CONFIG`): `theme`, `mouse`, `bel`, `notify`, `plain`. Switching
the theme with `/theme` persists it there automatically; the other values can
be written by hand and seed the matching flag defaults. Resolution order:
**flag → `BODEK_THEME` env (theme) → settings file → built-in default**.

### Key bindings

| Key | Action |
|-----|--------|
| `⏎` | Send the prompt (queues it while a turn is running) |
| `^K` | **The palette** — everything: commands, sessions, models, drawer tabs |
| `/` | Open the command palette (see below) |
| `@` | Attach a file (see below) |
| `alt+↑` / `alt+↓` | Jump to the previous / next turn |
| `alt+y` | Copy the **focused** turn's reply — the one you last jumped to (falls back to the latest reply) |
| `alt+r` | Re-send the last prompt (`/retry`) |
| `alt+f` | Search the transcript (`⏎` next match · `N` previous) |
| `^F` | Fold/unfold the most recent turn card (click any turn head with `--mouse`) |
| `tab` | Open/close the latest reasoning block (live turns auto-expand) |
| `^R` | Browse & resume saved sessions |
| `^O` | Switch the model |
| `^T` | Toggle extended thinking for the next turn |
| `^J` | Insert a newline in the input |
| `^L` | Clear the conversation (two-step confirm: `y` clears, any other key cancels) |
| `^E` | Toggle tool details — every step expands to its full output/logs |
| `^Y` | Copy the last reply to the clipboard (OSC 52 — needs a supporting terminal) |
| `Esc` | Cancel the running turn (queued prompts return to the input) |
| `↑` / `↓` / `PgUp` / `PgDn` / `^U` / `^D` | Scroll the transcript (arrows at the input's edge lines) |
| `^P` / `^N` | Recall previous prompts (prompt history) |
| `^G` / `End` (empty input) | Jump to the latest output |
| `F1` | Show the help card |
| `wheel` (with `--mouse`) | Scroll the transcript · click tool rows, turn heads, and the cockpit |
| `⏎` (disconnected, empty input) | Retry the connection |
| `^C` | Quit |

Prompts sent while a turn is running are **queued** and sent automatically
when the turn ends — the footer shows how many are waiting. While the
transcript is scrolled up mid-run, the footer flags `↓ new output`; press
`^G` to jump to the latest. If the connection drops, bodek retries with
backoff and, after giving up, keeps your draft and offers a manual retry
on `⏎` with an empty input.

**Every printable character always types.** No bare letter, digit, or
punctuation key is ever bound in the composer — actions live on chords and
non-character keys (`^K` palette, `alt+↑↓` turn jumps, `F1` help), so a
prompt can start with `?`, `[`, or any other character.

### Commands (`/`)

Type `/` at the start of the input for a command palette. `↑`/`↓` to choose,
`⇥` to complete, `⏎` to run, `esc` to dismiss. You can also just type the full
command and press `⏎`.

| Command | Action |
|---------|--------|
| `/help` | Show available commands and key bindings |
| `/clear` | Clear the conversation (two-step confirm; idle only) |
| `/copy` | Copy the last reply to the clipboard (OSC 52) |
| `/retry` | Re-send the last prompt (queues it if a turn is running) |
| `/theme [name]` | Switch the color theme at runtime and persist it (`ember-dark` · `ember-light` · `high-contrast` · `classic`) |
| `/stats` | Session metrics card (cost, cache, context gauge) |
| `/server` | Cockpit — server, link, budget & session in one card (or click the header) |
| `/sessions` | Browse, search, pin, rename, export & resume sessions |
| `/runs` | Headless REST runs — live status, remote approvals, cancel |
| `/run <prompt>` | Start a headless run (fresh session) and watch it in the runs tab |
| `/events` | The `odek.event/v1` runtime feed |
| `/plan` | Structured task plan of this session (live status) |
| `/memory` | Facts by target, pending-episode promote, consolidate |
| `/skills` | Skill provenance badges & promote |
| `/tools` | Tool registry with enabled state & MCP servers |
| `/config` | Sanitized config, lifetime usage, connections (kick) |
| `/model [name]` | Switch model (opens a picker with no argument) |
| `/thinking [on\|off]` | Toggle extended thinking for the next turn |
| `/cancel` | Cancel the running turn |
| `/attach <path>` | Stage a file to send with the next prompt (5 MB each, 10 MB total) |
| `/unattach [name]` | Drop staged files (all when no name given) |
| `/quit` | Exit bodek |

### The management drawer

`/sessions`, `/runs`, `/events`, `/plan`, `/memory`, `/skills`, `/tools`, and
`/config` all open tabs of **one drawer** with a shared grammar:

- `]` / `[` cycle tabs · `1`–`8` jump · `r` refresh · `esc` closes.
- **Every management row opens a detail view on `⏎`** — the full text
  behind the gate: a skill's description and provenance, a fact or pending
  episode's body, an MCP server's command/args/limits, raw JSON for nested
  config values. `↑`/`↓` scroll it, `esc`/`q` folds back (selection kept),
  and `p` promotes straight from the detail — no more promoting blind.
- **Sessions** — `/` search (server-side), `p` pin, `r` rename, `e`/`E`
  export md/json, `d` delete (`y` confirms — deletes are always two-step),
  `⏎` resume.
- **Runs** — live 3s poll, `A`/`D`/`T` remote approvals, `c` cancel,
  `p` refresh pending approvals, `e` drill into the run's event trail.
- **Events** — the `odek.event/v1` ring: `f` filter to this session, `x`
  clear filters (a runs-tab drill-in scopes it to one run).
- **Plan** — the engine's structured task plan (Telegram-parity renderer):
  summary badge (`v7 · 2/4 done · 1 blocked`) plus one row per step; `⏎`
  expands the selected step's full title/note. Read-only — the plan is
  steered by the model; while a run is active and a plan exists, a live
  `▸ plan 2/4 · <active step>` summary rides the busy line.
- **Memory** — `a`/`A` add user/env facts, `d` delete fact (`y` confirms),
  `p` promote a pending episode, `c`/`E` consolidate.
- **Skills** — provenance badges plus a dim description line; `p` promote
  (also from the detail view), `P` force-promote tainted.
- **Tools/Config** — registry + MCP servers; config values flatten one
  level (`sandbox.enabled`), nested values show raw JSON in the detail;
  lifetime usage, `d` kick a connection, `S` typed shutdown death-gate.

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

- **EMBER Terminal** — the WebUI's design language (electric amber on
  blue-charcoal) as terminal tokens; `BODEK_THEME=ember-light|high-contrast|classic`
  or `/theme` to switch live (persisted to `~/.bodek/config.json`), and
  `NO_MOTION=1` for a fully static UI.
- **The palette (`^K`)** — every surface one fuzzy search away, every row
  teaching its chord.
- **Turn cards** — telemetry rides the turn head, `^F` folds noisy turns,
  `alt+↑`/`alt+↓` jump turn-to-turn, reasoning accordions auto-expand live
  and collapse on the next turn, and `^E` expands every tool step's details.
- **Typed tool renderers** — diffs tint with diffstats, file reads get line
  numbers, JSON indents, test runs show pass/fail verdicts.
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
  with a running elapsed timer and cycling status while it works. Long turns
  keep every think→reply pair intact: each reasoning block is followed by its
  own answer card, in arrival order.
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
- **Skill suggestions** — when odek's learn loop proposes a skill, a passive
  card above the composer answers on `alt+s` (save) / `alt+x` (skip); it never
  blocks sending, and auto-save governs real persistence.
- **Server shutdown** — the config tab's `S` requires typing the literal word
  `shutdown` (the approval-friction pattern); the socket drop that follows is
  expected state, with `⏎` starting a fresh instance in spawn mode.
- **Live server snapshot** — a 25s heartbeat measures WebSocket round-trip
  latency and refreshes server uptime, connection count, and streaming state
  (the ⚡ badge beside the model); `/stats` surfaces the full link row.
- **Cancellation** (`Esc`) — abort a running turn via odek's cancel API.
- **Sandbox aware** — the header shows `🛡 sandboxed` or `⚠ host access`; pass
  `--sandbox` to run tool calls inside odek's Docker isolation.
- **Telemetry** — a pressure-tinted context-window gauge in the header
  (`ctx █▉░░░ 38% 380/1k`, eighth-block fill, green→amber→red), per-turn
  token/latency footers, and the full session roll-up in `/stats`.
- **Cost tracking** — when odek has token prices configured (limits), the
  header shows the running session spend, each turn footer its estimated
  cost, and `/stats` rolls up the session (with the `max_cost_usd` cap when
  set); hidden entirely otherwise.
- **Fluent by default** — gradient wordmark and hairline, smooth braille
  spinner, smart autoscroll that never yanks you while you read history, and a
  scroll-position indicator.
- **Engine notices** — skill loads, memory merges, and agent signals appear as
  quiet status lines. Nothing lingers: info traces fade after 3s, and
  errors, warnings, and disconnect notes autoclose after 10s (connection
  state stays visible in the header badge).
- **Attention when backgrounded** — turn completion and pending approvals set
  the terminal window title (`✓ done — <model>` / `⚠ approval needed —
  <model>`) and ring the bell (`--bel=false` mutes); `--notify` adds OSC 9
  desktop notifications. Fires only on terminal states — never per token.
- **Linear mode (`--plain`)** — skips the alt-screen entirely: agent events
  print as append-only text in the terminal's native scrollback above a
  minimal input chrome (`▸` tool calls, `[think]`, `[error]`, `⚠ approval`,
  `❯` your prompts, `✓ done · N tools · Xs · N tok`). Severity never rides
  color alone, which makes this the accessible surface for screen readers —
  and the natural one for pipes: `bodek --plain < task > run.log`. Streamed
  fragments stay suppressed; the reply lands whole when the turn ends.
  `NO_COLOR` degrades the entire EMBER palette to plain text in every mode.
- **Diff-aware tool steps** — expanding a step (`^E`) renders its output by
  shape: unified diffs tint (`+` green, `-` red, hunk headers steel, file
  markers dim) with a `+N −M` chip on the step head, fenced ` ```diff `
  blocks tint inside prose without mis-styling the surrounding text, file
  reads get line numbers, JSON pretty-prints, and test runs summarize
  pass/fail on the step line.
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
