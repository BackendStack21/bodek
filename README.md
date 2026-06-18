# bodek

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

```
┌──────────────┐   WebSocket (RFC 6455, JSON)   ┌──────────────────┐
│    bodek     │ ◄────────────────────────────► │   odek serve      │
│ (Bubble Tea) │   tokens · tools · approvals    │  (ReAct engine,   │
│   TUI client │                                 │   tools, sandbox) │
└──────────────┘                                 └──────────────────┘
```

---

## Install

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
bodek                                 # launch odek serve and start chatting
bodek --sandbox                       # run tool calls inside odek's Docker sandbox
bodek --url http://127.0.0.1:8080     # attach to an already-running odek serve
bodek --odek-bin ./odek               # use a specific odek binary
bodek -- --prompt-caching             # pass extra flags through to `odek serve`
```

Configuration (model, base URL, API key, MCP servers, memory, skills) is read
by `odek serve` from its usual chain — `~/.odek/config.json` → `./odek.json` →
`ODEK_*` env vars — so bodek inherits whatever you've already set up.

### Key bindings

| Key | Action |
|-----|--------|
| `⏎` | Send the prompt |
| `@` | Open file/session reference completion (see below) |
| `^J` | Insert a newline in the input |
| `^T` | Toggle extended thinking for the next turn |
| `^L` | Clear the conversation |
| `PgUp` / `PgDn` / wheel | Scroll the transcript |
| `^C` | Quit |

### File attachments / `@` references

Type `@` in the input to attach context. bodek queries odek's resource index
live and shows a completion popup; `↑`/`↓` to choose, `⏎` or `⇥` to insert,
`esc` to dismiss.

| Reference | Resolves to |
|-----------|-------------|
| `@path/to/file` | The file's contents, inlined into your prompt |
| `@sess:<id>` | A saved session transcript |

```
> summarize @internal/client/client.go and compare it with @sess:20260618-ab12
```

odek resolves and inlines the referenced content **server-side** (wrapped in
its untrusted-content boundary), so attachments go through the same security
model as any other external input — bodek doesn't special-case them.

When the agent requests approval for a dangerous operation, answer inline:

| Key | Action |
|-----|--------|
| `a` | Approve once |
| `d` | Deny |
| `t` | Trust this risk class for the session (when offered) |

---

## What you see

- **Streaming answers** rendered as Markdown ([glamour](https://github.com/charmbracelet/glamour)).
- **Tool activity** — every `tool_call`/`tool_result` shown live with a glyph
  per tool, a spinner, an argument preview, and a one-line result.
- **Security approvals** — odek's `danger` engine prompts surface as an inline
  panel; your answer is sent straight back over the socket.
- **Live reasoning** — the model's pre-tool thinking streams in dimmed text,
  with a running elapsed timer and cycling status while it works.
- **`@` autocomplete** — a live, navigable popup of files and sessions.
- **Telemetry** — session token totals and last-turn latency in the chrome.
- **Fluent by default** — gradient wordmark and hairline, smooth braille
  spinner, smart autoscroll that never yanks you while you read history, and a
  scroll-position indicator.
- **Engine notices** — skill loads, memory merges, and agent signals appear as
  quiet status lines.

---

## Development

```bash
make build      # → bin/bodek
make run        # build and launch
make vet
make tidy
```

Project layout:

| Path | Responsibility |
|------|----------------|
| `cmd/bodek` | CLI entry point: flags, lifecycle, wiring |
| `internal/server` | Launch / attach to `odek serve`, resolve the auth token |
| `internal/client` | odek serve WebSocket protocol (transport + event decoding) |
| `internal/tui` | The Bubble Tea model, update loop, and view |

---

## License

MIT — see [LICENSE](LICENSE).
