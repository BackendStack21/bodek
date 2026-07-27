# AGENTS.md — guidance for coding agents working on bodek

bodek is a **pure front-end**: a Bubble Tea TUI that streams from an
`odek serve` engine over WebSocket. It never re-implements agent behaviour
(tools, approvals, sandbox, skills, memory) — all of that lives in odek.
Keep it that way.

## Project layout

| Path | Responsibility |
|------|----------------|
| `cmd/bodek` | CLI entry point: flags, lifecycle, wiring |
| `internal/server` | Launch / attach to `odek serve`, resolve the auth token |
| `internal/client` | odek serve WebSocket protocol (transport + REST + decoding) |
| `internal/tokens` | Local persistence of per-session auth tokens |
| `internal/tui` | The Bubble Tea model, update loop, panels, and view |

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
  Never render raw remote content.
- The transcript model in `internal/tui`: each assistant `message` keeps a
  chronological `items []turnItem` timeline (reasoning blocks and step
  references interleaved). Preserve arrival order; don't regress to a
  single per-turn reasoning blob.
- Events arrive from `internal/client` already in chronological order —
  keep ingestion order-dependent and idempotent.

## Workflow rules for agents

- Don't run `git commit`/`git push` unless the user explicitly asks.
- Don't add dependencies without checking `go.mod` first and flagging it
  to the user.
- Keep `README.md` (key bindings, commands, feature list) and this file in
  sync when you change user-visible behaviour.
- CI (`.github/workflows`) runs build, vet, lint, and race tests on every
  push — a red pipeline means the commit checklist above was skipped.
