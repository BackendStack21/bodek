# bodek

[![CI](https://github.com/BackendStack21/bodek/actions/workflows/ci.yml/badge.svg)](https://github.com/BackendStack21/bodek/actions/workflows/ci.yml)
[![Release](https://github.com/BackendStack21/bodek/actions/workflows/release.yml/badge.svg)](https://github.com/BackendStack21/bodek/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/BackendStack21/bodek.svg)](https://pkg.go.dev/github.com/BackendStack21/bodek)
[![Go Report Card](https://goreportcard.com/badge/github.com/BackendStack21/bodek)](https://goreportcard.com/report/github.com/BackendStack21/bodek)

**A beautiful [Bubble Tea](https://github.com/charmbracelet/bubbletea) terminal interface for the [odek](https://github.com/BackendStack21/odek) agent.**

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

## Quick start

```bash
# 1 · Install the engine and the TUI
go install github.com/BackendStack21/odek/cmd/odek@latest
go install github.com/BackendStack21/bodek/cmd/bodek@latest

# 2 · Provide an LLM key (odek v2: provider env key)
export DEEPSEEK_API_KEY=<your-key>

# 3 · Chat
bodek
```

Prefer a compiled binary? Grab one from the
[releases page](https://github.com/BackendStack21/bodek/releases) — see
[Install](#install).

**Contents:** [What & why](#what--why) · [Install](#install) ·
[Usage](#usage) · [Features](#features) · [Using bodek](#using-bodek) ·
[Configuration](#configuration) · [Security model](#security-model) ·
[Troubleshooting](#troubleshooting) · [Development](#development)

---

## What & why

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
│   TUI client │                                 │  tools, sandbox)  │
└──────────────┘                                 └──────────────────┘
```

---

## Install

**Prerequisite:** bodek is only the front-end — you also need the `odek`
engine. See [odek's install instructions](https://github.com/BackendStack21/odek)
(provider env key such as `DEEPSEEK_API_KEY` / `ZAI_API_KEY` — see odek's [PROVIDERS.md](https://github.com/BackendStack21/odek/blob/main/docs/PROVIDERS.md)).

### Prebuilt binaries

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

# Provide an LLM key (odek v2: provider env key)
export DEEPSEEK_API_KEY=<your-key>

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
bodek --verbosity quiet                           # calmer start: info notes hidden, compact steps
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
`ODEK_*` env vars — so bodek inherits whatever you've already set up. bodek's
own front-end settings are separate; see [Configuration](#configuration).

---

## Features

### Interface

- **EMBER Terminal** — the WebUI's design language (electric amber on
  blue-charcoal) as terminal tokens; four themes (`ember-dark` ·
  `ember-light` · `high-contrast` · `classic`), switchable live with `/theme`
  and persisted to `~/.bodek/config.json`.
- **The palette (`^K`)** — every command, session, model, and drawer tab one
  fuzzy search away; every row teaches its chord.
- **Turn cards** — telemetry rides the turn head, a coding receipt
  (`touched 4 · +82 −19 · tests ✓`) scans what the turn changed, `^F`
  folds noisy turns to that receipt, `alt+↑`/`alt+↓` jump turn-to-turn,
  reasoning renders as an intent rail (last sentences, `beat N/M`), and
  `^E` expands every tool step's full details.
- **Typed tool renderers** — diffs tint with a `+N −M` chip, file reads get
  line numbers, JSON pretty-prints, and step lines earn typed chips from
  structured output only: test verdicts (`✓ 5 passed · 2 skipped`, go
  coverage), git commits/pushes (`⎇ a1b2c3d`, `↑ main`), lint results,
  compiler warning counts, HTTP statuses, and search hit counts. Prose like
  "Build passed" never goes green.
- **Streaming answers** rendered as Markdown
  ([glamour](https://github.com/charmbracelet/glamour)).
- **Tool activity** — every `tool_call`/`tool_result` shown live with a glyph
  per tool, a spinner, and a result peek (`⎿`, first 1–2 typed-renderer
  beats) so a finished step is scannable without `^E`. Running steps speak
  the same progress copy as the status line (`🧪 running tests`) and tick
  their own elapsed clock. Two or more in-flight calls wrap in a parallel
  swarm band that shrinks as members finish and dissolves on the last
  leftover. Full output stays behind expand.
- **Fluent by default** — gradient wordmark, smooth braille spinner, smart
  autoscroll that never yanks you while you read history, and a
  scroll-position indicator.
- **Version display & update hint** — bodek's version rides the header next
  to the logo (the spawned odek's next to the model); a quiet note appears at
  startup when a newer release is available (`bodek upgrade` installs it;
  dev builds are never nagged).

### Working with the agent

- **Live reasoning** — the model's pre-tool thinking streams as an intent
  rail (last two sentences, never flattened) with elapsed time. The clock
  freezes when that think cycle yields (a tool or the reply). A turn that
  thinks more than once labels each block `beat 2/3` — one beat is one
  think→act cycle. Tab / `^E` still unfolds the stored full block. Long
  turns keep every think→reply pair intact: each reasoning block is
  followed by its own answer card, in arrival order.
- **Context-aware progress** — while the agent works, a status line right
  below your last message shows what it's actually doing (`🧪 running
  tests`, `📖 reading client.go`, `🚀 pushing`) with a live elapsed timer.
- **Sub-agents** — a delegation paints an always-on chip strip under the
  parent step (`⟳ SA1 explore · ✓ SA2 lint · ✗ SA3 types`), so you can
  see who is running or who failed without expanding. Click a chip or
  `tab` (on a swarm turn) focuses one agent: identity + live beat
  (current tool, step, budget, cost). `^E` / expand still dumps that
  agent's logs, artifacts, and the framed result. Tasks the wire hasn't
  confirmed yet show as `◌` pending chips from the `delegate_tasks`
  argument. Glyphs: `✓` success, `◐` partial, `✗` error, `⊘` cancelled,
  `⏱` timeout, `×` lost on disconnect. The parent rollup still counts
  failures (`2/3 · 1 ✗ · 8.1k tok`). A finished swarm turn closes with a
  receipt rail (`sub-agents: 5 ✓ · 1 ✗ — #4 error`), not a markdown
  marker in the answer. Wire v2 (odek): queued chips, quiet profile/risk
  on the focused card, budget horizons (`it 9/15`, `12s/30m`), per-task
  cost, artifacts (`⎘`), and policy denials (`⊘ N denied`) — sub-agents
  are deny-not-prompt. `ctrl+s` (or `/stop <SA#>`, two-step) stops one
  running agent; `/agents` uses the same chip grammar and `o` jumps to
  the transcript chip.
- **Model switcher** (`^O`) — change the model for the next turn. The picker
  lists the server's `/api/models` catalog (configured model marked current,
  plus the provider listing), each annotated with its context window.
- **Session browser** (`^R`) — resume, replay, delete, pin (`p`), rename
  (`r`), export a transcript (`e` markdown, `E` JSON), and search server-side
  (`/`); `n` loads the next page. Resuming sends a `session_switch` so the
  server-side memory buffer is restored before you type.
- **Session home** — first-run teaches `type a task` and `^K palette`.
  After `^L`, the cleared transcript keeps the last prompt and coding
  receipt so the session is still oriented; `/new` returns to the
  first-run splash. The footer leads with a mode pill
  (`composer` / `approval` / `jobs` / …).
- **Auto-fitting composer** — the input box rests at three rows and grows
  with your prompt (multi-line or a single long line, wide-char aware) up to
  twelve rows or what the terminal can spare; it shrinks back after send,
  history recall, and `/`-commands. A one-row shelf above it carries staged
  files, the folded queue count (`^Q` unfolds the strip), a `↓ new output`
  hint, and a pending skill chip. Prompts wrap in the transcript at the
  viewport width — long lines are never clipped from view.
- **Skill suggestions** — when odek's learn loop proposes a skill, a
  passive shelf chip answers on `alt+s` (save) / `alt+x` (skip); it never
  blocks sending, and auto-save governs real persistence.
- **Engine notices** — skill loads, memory merges, and actionable agent
  signals appear as quiet status lines; internal housekeeping (context
  trims, tool execution times) stays silent. Info traces fade after 3s;
  errors, warnings, and disconnect notes autoclose after 10s.
- **Just-in-time hints** — the first time a state appears (a held prompt,
  a sub-agent swarm, a multi-step turn), a one-time 💡 tip teaches its key,
  then stays silent for the run. Features surface the moment they matter;
  no keybinding table required.
- **Session home dashboard** — after `/clear`, the home card orients: the
  last prompt and receipt, the context gauge, up to three recent sessions
  (titles sanitized), and one action line pointing at the `^K` hub.
- **Verbosity dial** — `/verbosity` (or `--verbosity quiet|normal|detailed`)
  sets the whole noise policy in one stroke: quiet hides engine traces,
  detailed switches on the `^E` expand-all view, normal is the default
  (`^E` stays a manual override in any dial state). Your own commands
  always answer, and errors, warnings, and hints show in every state.
- **Cancellation** (`Esc`) — abort a running turn via odek's cancel API.
- **Provider-failure cards** — when a turn dies mid-flight (stream stall
  under parallel load, HTTP 429 after the retry budget, dropped connection,
  timeout), the failure is classified and attached to the turn in plain
  language — never a raw LLM-internal error line. The card and the footer
  show the recovery path: `⏎` on the empty input re-sends the preserved
  prompt (`/retry` or `alt+r` work too).

### Safety

- **Inline approvals** — odek's `danger` engine prompts sit as a card
  above the still-usable composer: `A`/`D`/`T` decide, every other letter
  types a follow-up draft. See [Approvals](#approvals).
- **Friction & expiry** — repeated same-class approvals require typing
  `approve`; every request is time-boxed, autocloses on expiry, and can
  never collect an approval for a prompt the engine already abandoned.
- **Death-gates everywhere** — deletes are two-step, `/stop` and `^L` are
  two-step, and the server shutdown requires typing the literal word
  `shutdown`. See the [Security model](#security-model).

### Telemetry & cost

- **Context gauge** — a pressure-tinted context-window gauge in the header
  (`ctx █▉░░░ 38% 380/1k`, eighth-block fill, green→amber→red). Live
  `plan N/M` and `● N jobs` / `✗ job` instruments ride the same bar when
  a plan or background job is active.
- **Per-turn footers & `/stats`** — token counts and latency ride every turn
  head (`⚡` latency, `⌂` context, `↳` output tokens, `⚒` tools); `/stats`
  opens a sheet that rolls up the session (cost, cache, context). The `⎇`
  glyph is reserved for git commits in the transcript.
- **Cost tracking** — when odek has token prices configured, the header shows
  the running session spend, each turn footer its estimated cost, and
  `/stats` adds the `max_cost_usd` cap when set; hidden entirely otherwise.
- **Live server snapshot** — a 25s heartbeat measures WebSocket round-trip
  latency and refreshes server uptime, connection count, and streaming state
  (the ⚡ badge beside the model); `/server` opens the full cockpit card.

### Accessibility & plain mode

- **Linear mode (`--plain`)** — skips the alt-screen entirely: agent events
  print as append-only text in the terminal's native scrollback above a
  minimal input chrome (`▸` tool calls, `[think]`, `[error]`, `⚠ approval`,
  `❯` your prompts, `✓ done · N tools · Xs · N tok`). Streamed fragments stay
  suppressed; the reply lands whole when the turn ends.
- **Severity never rides color alone** — state always carries a glyph, which
  makes `--plain` the accessible surface for screen readers — and the
  natural one for pipes: `bodek --plain < task > run.log`.
- **`NO_COLOR`** degrades the entire EMBER palette to plain text in every
  mode; **`NO_MOTION=1`** swaps animated spinners for static frames.

### Connectivity & lifecycle

- **Spawn or attach** — bodek launches a private `odek serve` by default, or
  attaches to a running one with `--url` (+`--token`).
- **Auto-reconnect** — if the socket drops, bodek redials with backoff
  (500ms → 8s, 5 attempts) and re-adopts the session over the fresh socket;
  only a server that stays down leaves the `disconnected` badge (empty input
  + `⏎` retries manually).
- **Attention when backgrounded** — turn completion and pending approvals
  set the terminal window title (`✓ done — <model>` / `⚠ approval needed —
  <model>`) and ring the bell (`--bel=false` mutes); `--notify` adds OSC 9
  desktop notifications. Fires only on terminal states — never per token.
- **Sandbox aware** — the header shows `🛡 sandboxed` or `⚠ host access`;
  pass `--sandbox` to run tool calls inside odek's Docker isolation.
- **Wake turns** — when a background job finishes while the session is
  idle (odek ≥ v1.40), the engine wakes the model on its own; bodek opens
  the turn from the wire, marks the card `⬡ odek · wake`, and streams the
  model's report like any other turn — never rendered as a user message.
  Card opening is self-healing: the `turn_started` announcement is the
  primary signal (wake-marked for system turns, a plain remote card for
  turns prompted from other clients), with the wake stamp and a
  first-streamed-event fallback behind it — a turn cannot silently
  vanish from the transcript.

---

## Using bodek

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
| `alt+f` | Search the transcript (`⏎`/`n` next match · `N` previous · scrolling stays live) |
| `^F` | Fold/unfold the most recent turn card (click any turn head with `--mouse`) |
| `tab` | Focus the next sub-agent chip on a swarm turn; otherwise open/close the latest reasoning block; with neither, toggle the latest step's expansion |
| `^R` | Browse & resume saved sessions |
| `^O` | Switch the model |
| `^Q` | Unfold the queue strip from the composer shelf (`↑↓`/`jk` select · `←→`/`hl` move · `d d` two-step delete · `esc`/`⏎` folds it back; full manager: `/queue`) |
| `^S` | Stop the running sub-agent (two-step confirm: `y` stops, any other key continues) |
| `^T` | Toggle extended thinking for the next turn |
| `^J` | Insert a newline in the input |
| `^L` | Clear the conversation (two-step confirm: `y` clears, any other key cancels) |
| `^E` | Toggle tool details — every step expands to its full output/logs |
| `^Y` | Copy the last reply to the clipboard (local helper — `pbcopy`/`wl-copy`/`clip` — with OSC 52 fallback) |
| `Esc` | Close the topmost window (palette, drawer, find, `@`, queue, stats, expanded details, help, skill chip). Bare composer: cancel the running turn (two-step: `y` confirms). Approvals: collapse the expanded command, then deny. |
| `↑` / `↓` / `PgUp` / `PgDn` / `^U` / `^D` | Scroll the transcript (arrows at the input's edge lines) |
| `^P` / `^N` | Recall previous prompts (prompt history) |
| `^G` / `End` (empty input) | Jump to the latest output |
| `F1` | Show the help card |
| `wheel` (with `--mouse`) | Scroll the transcript · click tool rows, turn heads, and the cockpit |
| `⏎` (disconnected, empty input) | Retry the connection |
| `⏎` (after a failed turn, empty input) | Re-send the failed prompt |
| `^C` | Quit (confirm: `y` or a second `^C`) |

**Every printable character always types.** No bare letter, digit, or
punctuation key is ever bound in the composer — actions live on chords and
non-character keys (`^K` palette, `alt+↑↓` turn jumps, `F1` help), so a
prompt can start with `?`, `[`, or any other character.

### The prompt queue

Prompts sent while a turn is running are **queued** and sent automatically
when the turn ends — a transient note acknowledges each hold, and the count
rides both the busy status line and the footer (one drains per turn-end).
Queued prompts stay visible in a **strip directly above the input area**: one
row per prompt (long prompts collapse to a single line) with per-row `▲ ▼ ✕`
controls (`--mouse`) to reorder or delete, and a `^Q` keyboard focus mode for
the same actions (`↑↓` select, `←→` move, `d` delete). For full management,
`/queue` opens a dedicated window: `↑↓` select, `←→`/`hl` change priority,
`d`→`y` delete, and `⏎` sends the selected prompt ahead of the queue when
idle. The strip collapses to zero rows when the queue is
empty. While the transcript is scrolled up mid-run, the footer flags
`↓ new output`; press `^G` to jump to the latest.

### Commands (`/`)

Type `/` at the start of the input for a command palette. `↑`/`↓` to choose,
`⇥` to complete, `⏎` to run, `esc` to dismiss. You can also just type the
full command and press `⏎`.

| Command | Action |
|---------|--------|
| `/help` | Show available commands and key bindings |
| `/clear` | Clear the conversation (two-step confirm; idle only) |
| `/new` | Start a fresh session — new ID, empty context; the old one stays resumable via `/sessions` (idle only) |
| `/copy` | Copy the last reply to the clipboard |
| `/export` | Save the session transcript next to you — `/export [md|json]` (markdown by default, never overwrites) |
| `/retry` | Re-send the last prompt (queues it if a turn is running) |
| `/queue` | Manage the prompt queue — priority, delete, send now (the full manager over the `^Q` strip) |
| `/theme [name]` | Switch the color theme at runtime and persist it (`ember-dark` · `ember-light` · `high-contrast` · `classic`) |
| `/verbosity [quiet\|normal\|detailed]` | One-dial noise policy: quiet hides engine traces & starts steps compact, detailed switches on the `^E` expand-all view; bare `/verbosity` cycles |
| `/stats` | Session metrics sheet (cost, cache, context gauge) |
| `/server` | Cockpit — server, link, budget & session in one card (or click the header) |
| `/sessions` | Browse, search, pin, rename, export & resume sessions |
| `/runs` | Headless REST runs — live status, remote approvals, cancel |
| `/run <prompt>` | Start a headless run (fresh session) and watch it in the runs tab |
| `/events` | The `odek.event/v1` runtime feed |
| `/jobs` | Background jobs — live status, output viewer, `s` stop (requires odek ≥ v1.38) |
| `/plan` | Structured task plan of this session (live status) |
| `/memory` | Facts by target, pending-episode promote, consolidate |
| `/skills` | Skill provenance badges & promote |
| `/tools` | Tool registry with enabled state & MCP servers |
| `/config` | Sanitized config, lifetime usage, connections (kick) |
| `/model [name]` | Switch model (opens a picker with no argument) |
| `/thinking [on\|off]` | Toggle extended thinking for the next turn |
| `/cancel` | Cancel the running turn |
| `/stop <SA#>` | Stop one running sub-agent (bare `/stop` lists them) |
| `/agents` | Sub-agent registry — live 3s poll, `c` stop (two-step), `o` jump to transcript |
| `/attach <path>` | Stage a file to send with the next prompt (5 MB each, 10 MB total) |
| `/unattach [name]` | Drop staged files (all when no name given) |
| `/quit` | Exit bodek |

### The management drawer

`/sessions`, `/runs`, `/agents`, `/jobs`, `/events`, `/plan`, `/memory`,
`/skills`, `/tools`, and `/config` all open tabs of **one drawer** that
sits as a **bottom sheet** — about eight transcript rows stay visible
above it (full-bleed only when the terminal is too short) — with a
shared grammar:

- `]` / `[` cycle tabs · `1`–`9` jump · `0` jumps to the tenth (config) ·
  `r` refresh · `esc` closes.
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
- **Agents** — the serve instance's sub-agent registry, live-polled every 3s;
  `c` stop the highlighted row (two-step, same gate as `/stop`), `o` jump to
  the delegating transcript step, `⏎` the full registry record — trust,
  budget, cost, and artifact lines included.
- **Jobs** — the session's background commands (odek ≥ v1.38), live-polled
  every 3s; a background watcher surfaces starts as transcript notes and
  exits as **alert-tier notes naming the command** — with a bell / desktop
  notification (same gates as turns) so a finished job is never missed,
  even with the tab closed. odek ≥ v1.40 also pushes `bg_job` frames: the
  snapshot refreshes the moment a job starts or exits, the watcher tick
  stays as the fallback. `⏎` opens the job's output viewer (`f`
  pages further output), `s` stops a running job (two-step, same gate as
  `/stop`). Jobs are session-scoped: other sessions' jobs never appear.
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

### File attachments

Type `@` to attach a file. bodek searches the working tree and shows a
completion popup; `↑`/`↓` to choose, `⏎` or `⇥` to insert, `esc` to dismiss.

```
> summarize @internal/client/client.go and explain the protocol
```

odek resolves and inlines the file content **server-side** (wrapped in its
untrusted-content boundary), so attachments go through the same security
model as any other external input — bodek doesn't special-case them. (Saved
sessions are resumed via `/sessions` or `^R`, not `@`.)

### Approvals

When the agent requests approval for a dangerous operation, the card sits
above the composer. `A` / `D` / `T` (and `⏎` / `Esc`) decide; every other
letter types into the draft so a follow-up survives the gate:

| Key | Action |
|-----|--------|
| `↑` / `↓` (or `←` / `→`) | Move the highlight (Approve / Deny / Trust class when offered) |
| `⏎` | Confirm the highlighted option |
| `Esc` | Deny (abort) |
| `Tab` | Expand/collapse the full command & description text |
| `PgUp` / `PgDn` / `^U` / `^D` | Scroll the transcript while the panel is open |

After three same-class approvals inside a minute the server engages
**friction mode**: the panel shows the recent-approval count, the trust
shortcut is withdrawn, and approving requires typing the literal word
`approve` and pressing `⏎` (a mistyped word resets — retyping is the point).
Denying stays one `Esc`.

Approvals are time-boxed by the engine (60s by default), and an expired
request is dead — odek fails the tool call and picks an alternative path.
The panel shows a live `expires in Ns` countdown (red in the last 10
seconds) and autocloses expired requests with an expiry notice, so a stale
form can never collect an approval for a prompt the engine already abandoned.

---

## Configuration

bodek keeps its **own front-end settings** in `~/.bodek/config.json`
(override the location with `BODEK_CONFIG`): `theme`, `mouse`, `bel`,
`notify`, `plain`. Switching the theme with `/theme` persists it there
automatically; the other values can be written by hand and seed the matching
flag defaults. Resolution order:
**flag → `BODEK_THEME` env (theme) → settings file → built-in default**.

The full settings reference — every key, every `BODEK_*`/display env var,
and an example file — lives in
[docs/CONFIGURATION.md](docs/CONFIGURATION.md). odek's server-side
configuration is unaffected and stays in `~/.odek/config.json`.

---

## Security model

bodek is a renderer, not an enforcer — and that's the point. All agent
behaviour, danger gating, and sandboxing live in odek; the TUI's job is to
never become the weakest link:

- **Everything from the wire is sanitized.** Any remote-rendered content —
  tool output, file contents, titles, server strings — passes through
  `sanitize()` before it touches the screen. Raw terminal escapes from the
  server can't repaint your session.
- **Untrusted content stays fenced.** Tool results arrive as JSON envelopes
  wrapped in odek's untrusted-content markers; bodek decodes the envelope and
  folds the wrappers away — the content is displayed as data, never executed,
  and prompt-injection text inside it renders like any other text.
- **Approvals can't be faked or stale.** The panel is inline and keyed, the
  trust shortcut dies under friction mode, and expired requests autoclose —
  a dead prompt can't collect an approval. See [Approvals](#approvals).
- **Destructive actions are deliberately slow.** Session/fact deletes are
  two-step (`y`), stopping a sub-agent is two-step, clearing the
  conversation is two-step, and shutting the server down requires typing
  `shutdown` letter by letter.
- **Server config is read-only** — the config tab renders a sanitized
  snapshot; mutation stays with the operator on disk.
- **Tokens stay local.** The per-instance token is adopted automatically on
  spawn and stored only on your machine (`internal/tokens`); attaching asks
  you to supply it explicitly.

---

## Troubleshooting

- **`bodek` can't find `odek`** — the engine must be on your `PATH`, or pass
  `--odek-bin /path/to/odek`.
- **Auth errors when attaching** — copy the *full* token URL `odek serve`
  printed (`--url 'http://…/?token=…'`) or pass the token via `--token`.
  Spawned instances adopt the token automatically.
- **Clipboard (`^Y` / `alt+y`) pastes something stale** — locally bodek pipes
  through `pbcopy`/`wl-copy`/`clip` when installed; without one (or over SSH)
  it falls back to OSC 52, which some terminals ignore — pass it through or
  install a helper. The copy note always says which path ran.
- **Mouse scrolling eats text selection** — that's the `--mouse` trade-off
  (terminals can't have both). Launch without it when you need to copy.
- **Colors look wrong** — try `/theme classic`, check `TERM`; `NO_COLOR=1`
  forces a colorless render everywhere.
- **Connection dropped mid-turn** — bodek retries with backoff (5 attempts)
  and re-adopts the session; if it gives up, your draft is kept — press `⏎`
  on an empty input to reconnect.
- **Where are my settings?** — `~/.bodek/config.json` (override with
  `BODEK_CONFIG`). See [docs/CONFIGURATION.md](docs/CONFIGURATION.md).
- **Windows** — use Windows Terminal or another ANSI/OSC-capable
  emulator; desktop notifications depend on OSC 9 support.

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

Continuous integration runs build, `go vet`, `golangci-lint`, and the
race-enabled test suite on every push (see
[`.github/workflows`](.github/workflows)). Tagged releases (`vX.Y.Z`) are
built and published automatically by
[GoReleaser](https://goreleaser.com).

Project layout:

| Path | Responsibility |
|------|----------------|
| `cmd/bodek` | CLI entry point: flags, lifecycle, `version` / `upgrade` subcommands |
| `internal/server` | Launch / attach to `odek serve`, resolve the auth token |
| `internal/client` | odek serve WebSocket protocol (transport + REST + decoding) |
| `internal/tokens` | Local persistence of per-session auth tokens |
| `internal/settings` | Front-end settings (`~/.bodek/config.json`) |
| `internal/update` | Self-upgrade: fetch and swap in the latest GitHub release binary |
| `internal/tui` | The Bubble Tea model, update loop, panels, and view |

### Architecture & testing

bodek is a pure client, so it is highly testable: the WebSocket protocol,
REST endpoints, token store, and the full Bubble Tea update/view loop are
exercised by unit and integration tests against an in-process `odek serve`
stand-in. Internal-package statement coverage is **~99%** (client 100%,
tui 99%, tokens 98%, server 95% — the remainder is unreachable OS-error
handling).

### For contributors

- [AGENTS.md](AGENTS.md) — the contributor contract: commands, the
  mandatory pre-commit checklist, testing expectations, and code
  conventions.
- [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md) — the audit matrix mapping
  every `odek serve` REST endpoint and WebSocket message to its client
  method and UI surface. Update it when either side changes.
- [docs/CONFIGURATION.md](docs/CONFIGURATION.md) — the front-end settings
  and environment variable reference.

---

## License

MIT — see [LICENSE](LICENSE).
