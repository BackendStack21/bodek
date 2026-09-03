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
- Events arrive from `internal/client` already in chronological order —
  keep ingestion order-dependent and idempotent.
- `internal/tui` is split by responsibility: `model.go` holds the core
  model, `events.go` event handling, `input.go` key/text input,
  `approval.go` the approval flow, `reconnect.go` socket recovery. Put
  new code in the matching file instead of growing `model.go`.
- The TUI reconnects with backoff and resumes the session after a socket
  drop (`reconnect.go`) — don't break that by assuming a single
  connection per run.
- Streaming renders are coalesced into one flush per 80ms for
  performance; batching new redraw paths the same way keeps the TUI
  responsive.
- The slash-completion popup holds key capture while open; typed keys
  must keep flowing to the input. Route keys through the popup first,
  then fall through to normal input handling.
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
- Server-initiated wake turns (odek ≥ v1.40, `system_initiated` on the
  session frame) open a streaming card from the wire (`openWakeTurn` in
  `events.go`): without it every streamed event drops, because cards only
  opened on the local send path. The card carries the `systemWake` marker
  (renders `⬡ odek · wake`); wake turns are never rendered as user
  messages, and a wake frame arriving during an operator turn opens
  nothing. If the stamped frame is missed (reconnect race, wire quirk),
  `ensureWireTurn` lazily opens the card from the first streamed event —
  idle-plus-stream proves a server-initiated turn, since every operator
  turn starts with a local send — keeping the wake marker when `bg_wake`
  armed it, healing the (normally unreachable) busy-without-card state,
  and labelling a stampless stream as a plain remote card instead of
  dropping it.

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
