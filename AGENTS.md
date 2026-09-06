# AGENTS.md — guidance for coding agents working on bodek

bodek is a **pure front-end**: a Bubble Tea TUI that streams from an
`odek serve` engine over WebSocket. It never re-implements agent behaviour
(tools, approvals, sandbox, skills, memory) — all of that lives in odek.
Keep it that way.

## Project layout

| Path | Responsibility |
|------|----------------|
| `cmd/bodek` | CLI entry point: flags, lifecycle, `version` / `upgrade` subcommands |
| `internal/server` | Launch / attach to `odek serve`, resolve the auth token |
| `internal/client` | odek serve WebSocket protocol (transport + REST + decoding) |
| `internal/tokens` | Local persistence of per-session auth tokens |
| `internal/tui` | The Bubble Tea model, update loop, panels, and view |
| `internal/update` | Self-upgrade: fetch and swap in the latest GitHub release binary |

## Commands

```bash
make build   # compile → bin/bodek
make test    # go test -race ./...   (always use the race detector)
make vet     # go vet ./...
make lint    # golangci-lint (config: .golangci.yml)
make fmt     # go fmt ./...
make cover   # coverage report for internal packages
```

## Before every commit — mandatory checklist

Run all of these and make them pass before committing:

1. `make fmt`
2. `make vet`
3. `make lint` — must report `0 issues`
4. `make test` — the full race-enabled suite, all packages green

Never commit with failing tests or lint findings. If a change breaks an
existing test, fix the code or deliberately update the test — never delete
or weaken a test just to get green.

## Commit messages — Conventional Commits (CZ)

Use [Conventional Commits](https://www.conventionalcommits.org) semantics:

```
<type>(<scope>): <short imperative summary>

<optional body: what and why, not how>
```

- **Types**: `feat` (user-visible behaviour), `fix` (bug), `refactor`,
  `perf`, `test`, `docs`, `chore`, `ci`, `build`.
- **Scope**: usually the package, e.g. `tui`, `client`, `server`, `tokens`.
- Summary: lowercase, imperative, no trailing period, ≤ 72 chars.
- Breaking changes: `!` after the scope and a `BREAKING CHANGE:` footer.
- One logical change per commit; don't mix refactors with features.

Examples from this repo's history:

```
fix(tui): interleave reasoning blocks with tool calls chronologically
feat(tui): compact tool steps with Ctrl+E details toggle
```

## Testing expectations

- Internal-package coverage is ~99% — keep it there. Any new behaviour
  needs a test; any bug fix needs a regression test.
- TUI tests drive the model directly: construct a `Model` (see
  `newTestModel` in `internal/tui`), feed `client.Event`s through
  `handleEvent`/`Update`, and assert on `m.msgs`, rendered output
  (`renderMessage`, `plain()`), or key handling (`key("ctrl+e")`).
- Don't assert on exact timings or spinner frames — keep tests non-flaky.
- The client and server packages are tested against an in-process
  `odek serve` stand-in; reuse those fixtures.

## Code conventions

- Standard Go style, `go fmt` clean, golangci-lint clean (`.golangci.yml`).
- Match the surrounding file's comment density and naming; exported API is
  rare here — most identifiers stay unexported.
- **Minimal diffs**: change only what the task requires. No drive-by
  refactors, renames, or reformatting.
- **Security**: anything rendered from the wire (tokens, tool output, file
  contents) must go through `sanitize()` — see `internal/tui/model.go`.
  Never render raw remote content. Tool results arrive as JSON envelopes
  wrapped in untrusted-content markers: decode the envelope and fold the
  wrappers away before display — don't render either verbatim.
- The transcript model in `internal/tui`: each assistant `message` keeps a
  chronological `items []turnItem` timeline (reasoning blocks, step
  references, and reply segments interleaved — one think→reply cycle per
  segment pair, each rendered independently). Preserve arrival order; don't
  regress to a single per-turn reasoning blob or a single trailing answer
  card, and don't reintroduce a separate in-transcript thinking placeholder
  alongside it. `msg.content` stays the "\n\n"-joined blob of all reply
  segments (appendReply maintains it) for export, stats, and hand-built
  messages; turn markers (`**Cancelled.**` etc.) attach to the last reply.
  The calm default hides reasoning previews and tool responses: the intent
  rail and step result bodies paint only under `^E` (details) or a
  deliberate expand — `tab` opens one reasoning block; click or tab's
  fallback expands one step; the result peek is gone. While a turn
  streams, its head line carries the run's elapsed counter right-aligned
  at the viewport edge (the `runStart` clock, whole seconds, dropped on
  finalize — the sealed telemetry row sits under the reply). A deliberately opened
  live rail still holds completed sentences (or a short frozen stem) so a
  fast `thinking_delta` stream cannot ticker it; failed steps still
  auto-expand once on the live path. The status line
  stays a quiet `reasoning` / `composing` label while those surfaces own
  the words; live tool steps use a static `▸` (one spinner: the status
  line). A finished step keeps its sealed `dur` in the right rail — after
  the typed chip when one exists — so per-tool times outlive completion;
  resumed history (`dur` 0) shows none. `beginWireTurn` must not
  `GotoBottom` — `refresh()` already
  sticks when the reader is at the bottom, and a yank fights the
  "↓ new output" contract. Render-only layers
  (intent rail, turn receipt, live
  swarm band, sub-agent chip strip, swarm receipt rail) must not reorder
  `items[]` — the parallel-tool swarm is a consecutive overlay on unfinished
  parent steps and dissolves when one leftover remains. Sub-agent children
  live on `step.agents` and always paint as chips; `agentSel` (1-based, 0 =
  none) focuses one mini-card. The swarm verdict is a receipt rail, never
  stuffed into `msg.content`.
- Events arrive from `internal/client` already in chronological order —
  keep ingestion order-dependent and idempotent. `listen()` drains every
  pending frame into one `eventBatchMsg` so a `thinking_delta` firehose
  cannot run View per fragment (that filled the 256-deep Events buffer
  and tripped odek's 30s write timeout — connection lost after any
  prompt). `ingestWireBatch` re-arms `listen()` as a top-level cmd, never
  nested inside handleEvent's Batch. The client also merges consecutive
  thinking/token deltas (16 at a time) before enqueueing. The header
  connection lamp stays lit while a turn runs (`◉`); idle is `●`,
  reconnect `◌`, down `○`. Progress stays on the status line — an empty
  corner was read as a dropped socket. The header `ctx` gauge is the
  parent conversation window: odek ≥ v2.3 sends `windowTokens` (last
  parent prompt) plus `maxContextTokens` (runtime model limit — beats
  `/api/models`). `done.inputTokens` is billing spend including charged
  sub-agents — never a gauge. Pre-v2.3 still deltas `contextTokens`.
  Absent/zero `windowTokens` holds the last fill.
- `internal/tui` is split by responsibility: `model.go` holds the core
  model, `events.go` event handling, `input.go` key/text input,
  `approval.go` the approval flow, `chrome.go` drawer-sheet / shelf /
  header-instrument / session-home layout, `reconnect.go` socket recovery.
  Put new code in the matching file instead of growing `model.go`.
- First-run home (`welcome` in `banner.go`) is the working directory plus
  one next-action tip. Branding lives in the header — do not reintroduce a
  splash wordmark. After `^L`, `sessionHome` keeps last prompt / receipt /
  recents; `/new` returns to first-run.
- Composer newline is `⇧⏎` (`shift+enter`, also `alt+enter` / `^J`).
  Enable kitty disambiguate (flag 1) and xterm `modifyOtherKeys=2` from
  `Init` — after alt-screen — so Cursor/xterm.js encodes Shift+Enter
  (`CSI 27 ; 2 ; 13 ~`) instead of CR. Never enable kitty "report all
  keys". `FilterShiftEnter` rewrites those CSI sequences (and the
  remapped `^C` / `^K` / esc) back into KeyMsgs — Codium/Cursor encode
  Ctrl+C as CSI once those modes are on. `RestoreEnhancedKeys` clears
  leftovers on startup and shutdown. Alt-screen always enables mouse
  cell-motion so the wheel scrolls (and clicks hit turn heads / answer
  cards / steps / the queue). A left-click on an answer card (or a
  collapsed summary) copies that turn's `msg.content` — final or the
  partial stream — and parks `focusIdx` so a follow-up `alt+y` copies
  the same card. Success is a footer `✓ Copied` flash only (`noticeTTL`,
  generation-guarded expire — no transcript notice). Turn heads still fold;
  step headers and chips still win on their own lines; raw `/help`
  cards are not copy targets. Local clipboard helpers (`pbcopy` /
  `wl-copy` / `clip`) must run as a plain `tea.Cmd` — `tea.ExecProcess`
  releases the alt-screen and flickers on every copy. OSC 52 (SSH /
  helper-less) still uses `tea.Exec`. `tea.Exec` (attention, OSC 52) must return `afterExec`
  so `restoreAfterExec` re-arms mouse and Shift+Enter — Bubble Tea's
  RestoreTerminal does not. `--plain` skips mouse reporting.
- The management drawer is a bottom sheet: keep ~8 transcript rows above
  it (`sheetTranscriptMin`); full-bleed only when the terminal cannot
  fit transcript + sheet. Layout-only — tab grammar (`]`/`[`/`⏎`/`esc`)
  stays. Approvals render as a card above a live composer: `A`/`D`/`T`
  decide, other printables type a follow-up draft (friction still
  captures typing into `apprTyped`). The unfocused queue is a shelf
  chip; `^Q` unfolds the strip (`qfocus`).
- The TUI reconnects with backoff and resumes the session after a socket
  drop (`reconnect.go`) — don't break that by assuming a single
  connection per run.
- Streaming renders are coalesced into one flush per 80ms for
  performance; batching new redraw paths the same way keeps the TUI
  responsive. Live reply segments glamour-render on that flush (not only
  at turn end). The spinner animates the status line only — transcript
  rebuilds for live transcript clocks (streaming head counter, step timers)
  coalesce at 250ms (`tailClockFlushMsg`); the lane ticks whenever a message
  streams, not only when steps run.
  Finished message blocks and done tool steps cache their render output;
  invalidate per message/step on expand, not the whole prefix.
- The slash-completion popup holds key capture while open; typed keys
  must keep flowing to the input. Route keys through the popup first,
  then fall through to normal input handling.
- ESC closes the topmost window, then inspect chrome, then (if busy)
  arms cancel. Order: confirm disarm → palette → drawer edit/detail/tab
  → cockpit → find → `@`/`/` popup → queue strip → approval (collapse
  expand, then deny) → skill chip / `^E` / open thinking / agent
  focus / expanded step / help card → cancel gate. Do not let a leftover
  overlay swallow ESC without dismissing.
- Management drawer tabs (memory/skills/tools/config — and jobs) have a detail
  submode: `⏎` expands the selected row (skill description, full fact
  text, MCP args, raw config JSON — everything through `sanitize()`),
  `esc`/`q` folds back, `p` promotes in place. Tab switches reset it
  (`switchDrawerTab`); keep that reset when adding new open paths.
- The jobs tab pairs with a REST lifecycle watcher (`jobs_tab.go`): the
  TUI polls `/api/jobs` (10s in background, 3s while the tab is visible)
  and diffs status transitions into transient notes. odek ≥ v1.40 also
  pushes `bg_job` frames on start/exit — `handleEvent` routes them through
  `kickJobsFetch()` for an immediate snapshot, watcher tick as fallback;
  `bg_wake` frames become transient notes. Generation counters
  (`jobsSeq`/`jobsWatchSeq`) drop stale ticks — keep both chains
  generation-guarded when touching the cadence.
- The narration-line plan strip (`planStripLabel` in `plan.go`) patches
  on `plan` tool_call (`applyPlanMutation`) so the count moves on that
  frame. REST (`GET /api/sessions/{id}/plan`) confirms after
  `tool_result` (250ms debounce) — a fetch on the call races the store
  and paints the previous snapshot. `planDirty` drops every non-confirm
  reply (create leaves `planVer` stale, so a version-newer poll can still
  be the pre-write store). Only the tool_result confirm fetch may land —
  that is also how a rejected write reverts the patch. Live poll is 1s
  while `busy`, 3s on the `/plan` tab; `planLiveKick` arms it from
  `sendPrompt` / `beginWireTurn` via `planFollowup`, never as a Tick
  batched into submit. Generation counters
  (`planDebSeq`/`planReqSeq`/`planPollSeq`) drop stale ticks — keep
  them when touching the cadence.
- Server-initiated wake turns (odek ≥ v1.40, `system_initiated` on the
  session frame) open a streaming card from the wire (`openWakeTurn` in
  `events.go`): without it every streamed event drops, because cards only
  opened on the local send path. The card carries the `systemWake` marker
  (renders `⬡ odek · wake`); wake turns are never rendered as user
  messages, and a wake frame arriving during an operator turn opens
  nothing. With odek's `turn_started` protocol the dedicated frame is the
  primary signal for every turn (wake → wake-marked card, foreign
  operator turn → plain remote card); the stamped session frame and
  `ensureWireTurn`'s lazy open (first streamed event while idle) remain
  as fallbacks, so a turn must be missed by all three paths to drop.

## Workflow rules for agents

- **Never modify the odek project from a bodek session.** bodek is a pure
  front-end; if a fix requires changes in odek (protocol, `odek serve`,
  engine behaviour), do not edit that repo — instead summarize the required
  change and suggest it to the user as a task for an odek session.
- Don't run `git commit`/`git push` unless the user explicitly asks.
- Don't add dependencies without checking `go.mod` first and flagging it
  to the user.
- Keep `README.md` (key bindings, commands, feature list) and this file in
  sync when you change user-visible behaviour.
- CI (`.github/workflows`) runs build, vet, lint, and race tests on every
  push — a red pipeline means the commit checklist above was skipped.
