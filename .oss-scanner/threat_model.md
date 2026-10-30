# Threat model — bodek

## What this project does and where untrusted input enters
- bodek is a Bubble Tea terminal UI for the odek agent. It spawns (or attaches to) a local `odek serve` instance and renders its live stream: reasoning, tokens, tool calls, approvals, skills, memory. All agent behaviour and security gating live in odek; bodek is a renderer.
- Untrusted: everything received from the odek server over HTTP/WebSocket — tool output, file contents, session titles, model text, config snapshots. Model and tool output is attacker-influenced (prompt injection), so it must be treated as hostile when displayed.
- Untrusted: release metadata and binaries fetched by the self-updater (`internal/update`).
- Trusted: the local operator at the keyboard, bodek's own settings files.

## Components that matter most / least
- Highest priority:
  - Output sanitization (`sanitize()` and everything that renders server strings in `internal/tui`): terminal escape/OSC injection, bidi/control characters, hyperlink spoofing.
  - Approval panel: approvals must not be forgeable, replayable, or applied to the wrong/stale request; friction mode must not be bypassable.
  - `internal/tokens`: storage and file permissions of per-instance server tokens.
  - `internal/client` and `internal/server`: talking to and spawning `odek serve` (token handling, attaching to an attacker-controlled port).
  - `internal/update`: download and verification of new binaries.
- Medium: `internal/filelock`, `internal/workspace`, `internal/settings`, `internal/watchdog`.
- Out of scope: `docs/` site assets, `scripts/`.

## How to exercise it
- `go test -race ./...`. `/usr/local/bin/bodek` is prebuilt in the image. No odek server or LLM credentials are available; use the package tests and fakes.

## How you rate severity
- Critical: content from the server causing code execution on the operator's machine, or a forged/misattributed approval that makes odek run a tool the operator did not approve; updater installing an unverified binary.
- High: terminal escape injection that can repaint the screen to spoof an approval or hide content; leaking the server token (to logs, other users, world-readable files, or a different server).
- Medium: DoS of the TUI from crafted server output, sanitizer gaps without a demonstrated spoofing impact.
- Low: hardening suggestions.

## Anything to leave alone
- Agent decisions and tool gating themselves (those belong to odek).
