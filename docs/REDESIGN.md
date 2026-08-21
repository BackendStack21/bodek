# bodek 2.0 — "EMBER Terminal": A Holistic TUI Redesign

Status: **proposal for review** · Owner: design + engineering · Baseline: post-spec-v2 client (streaming deltas, sessions/pin/export, profiles, friction approvals, heartbeat)

---

## 1. Where we are — an honest critique

### What earns its keep (we keep it)

- **The chat-first transcript.** Prompt → live reasoning → tool tree → answer is the right mental model for an agent client. No dashboard will beat it.
- **Contextual narration** (`progress.go`). "🧪 running tests", "📖 reading client.go" is genuinely delightful and load-bearing for perceived speed.
- **The inline approval panel** — decisions happen where the context is visible, not in a modal void.
- **Footgun-averse key design.** Typed friction confirmation, `^G` instead of bare `G`, letters that never decide during approvals. This discipline is a brand asset.
- **Render discipline.** Coalesced streaming flushes, cached transcript prefix, sanitize-everything trust posture.

### What holds us back (the redesign drivers)

| # | Problem | Evidence |
|---|---------|----------|
| D1 | **Brand schizophrenia** | bodek is Charm purple/pink (`styles.go`); the WebUI is EMBER — electric amber on blue-charcoal, its own type scale, its own motion rules. One product, two personalities. |
| D2 | **No scalable IA** | Two full-screen panels today (sessions, models). Nine more integrations are coming (memory, skills, tools, runs, events, usage, connections, config, mcp). Full-screen takeover × 11 = disorientation. There is no navigation spine. |
| D3 | **Status fragmentation** | State lives in four places: header bar, busy status line, footer, `/stats` card. Token counts appear in header *and* stat lines *and* `/stats`. The cockpit concept doesn't exist. |
| D4 | **Approval queue is a correctness bug** | `m.approval` is a single pointer (`events.go`). odek runs parallel tools — a second `approval_request` silently overwrites the first. The user approves the wrong thing or the run deadlocks to timeout. |
| D5 | **Long turns bury the answer** | 20 tool steps push the answer off-screen. No per-turn collapse, no turn-to-turn navigation. Tool results render as capped plain text — diffs, JSON, test output all look the same. |
| D6 | **Discoverability cliff** | The power is in chords (`^R ^O ^T ^E ^P`) that are invisible until `/help` is discovered. Footer teaches 3 hints. |
| D7 | **Modality ladder is undocumented** | `esc` means cancel / close / deny / exit-edit depending on context. Each panel invents its own keys (`d`, `p`, `e`, `/`). Nothing systematic. |
| D8 | **Empty & error states are bare** | First run: banner + empty input. Disconnect: a notice line. Spawn failure: a Go error string. No recovery affordances. |
| D9 | **Flat visual hierarchy** | Answer prose, tool noise, reasoning, and chrome have similar weight. The eye has no anchor. |
| D10 | **No graceful degradation** | One dark theme; color-only signals; no light mode; no reduced-motion; narrow terminals get truncation instead of adaptation. |

---

## 2. Design pillars

1. **The transcript is the stage.** Chrome recedes; the answer is the hero. Everything outside the transcript earns pixels by being glanceable in under a second — or it moves into a drawer.
2. **One spine, no dead ends.** Every surface is reachable from one universal palette (`^K`). Every overlay shares one frame with one grammar. `esc` always climbs exactly one rung down the modality ladder. You can never be lost.
3. **Progressive disclosure, three depths.** *Glance* (one line) → *inspect* (expand) → *audit* (drawer/export). Applied uniformly to tool results, approvals, stats, and events.
4. **Teach at the point of need.** The status bar hints follow focus and state. Empty states demonstrate rather than describe. Nothing essential hides behind a memorized chord — chords are power moves, not gates.
5. **Calm by default, loud on signal.** Motion budget: spinner and gauge flashes only. Semantic color is reserved for meaning — risk, failure, budget pressure. Decoration is hairlines and spacing, never fills.
6. **Degrade gracefully.** Three width breakpoints, light + high-contrast themes, 16-color fallback, `NO_MOTION` switch. The design must survive a 70×24 terminal and a 250×60 one.

---

## 3. The concept — a calm cockpit over a live engine

Two ideas carry the redesign:

**The Cockpit** — one persistent strip that answers, at a glance: *am I connected, what model, how full is my context, what is this costing me.* Everything else (uptime, conns, limits, usage) lives one keypress behind it in a popover. This replaces the current header/footer/`/stats` trichotomy.

**The Spine** — a universal command palette (`^K`) plus one tabbed drawer frame that hosts *all* management surfaces. This is how nine upcoming integrations enter the UI without nine modality explosions. Think Linear's ⌘K mapped to terminal idiom; think k9s, but calm.

---

## 4. Layout

### 4.1 Standard (80–119 cols) — the default canvas

```
┌────────────────────────────────────────────────────────────────────┐
│ ⬡ bodek ⟨glm-5.3 ⚡⟩ ● sandbox · $0.41        ctx ▓▓▓░░ 42% · ● 34ms │  ← cockpit (2 rows)
│ ══════════════════════════════════════════════════════════════════ │     amber rule
│                                                                    │
│  TRANSCRIPT — the stage                                            │
│                                                                    │
│  ▌you · fix the login bug                              2m ago      │  ← turn head: author+age
│                                                                    │
│  ▌odek · ⚡ 8.2s · 4 tools · ⇥9k ↦1k · $0.031                     │  ← turn head: telemetry
│    ⋯ reasoning · 12 lines                            [tab expand]  │  ← accordion (auto-collapsed)
│    ⌕ search_files  "login"                        143ms ✓          │  ← step line
│    ✎ patch  auth.go                       312ms ✓  +42 −7          │  ← typed renderer: diffstat
│    The bug was a stale session cookie on line 88. I've…            │  ← the hero: answer prose
│                                                                    │
│  ⏳ approval 1 of 2 · shell_exec · rm -rf build/        A · D · T   │  ← approval queue (D4 fix)
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ ❯ ask odek… · @file · /command · ⌘K everything               │  │  ← composer
│  │ 📎 notes.txt ✕                                                │  │  ← attachment chips
│  └──────────────────────────────────────────────────────────────┘  │
│  ● ready · ⇥12k ↦3k this session · rtt 34ms          ? help · ^K   │  ← status bar (context-aware)
└────────────────────────────────────────────────────────────────────┘
```

### 4.2 Wide (≥ 120 cols) — split drawer, opt-in

The drawer opens as a 38–44 col right pane beside a ≥ 70 col transcript, so a live run stays visible while you browse runs/events/sessions. Toggle with `w`; remember per profile. On standard widths the drawer overlays full-area (today's behavior, refined).

```
│  TRANSCRIPT (≥70 cols)                    │ ╞═ runs ════════════╞ │
│  ▌you · ship it                           │ │ ▶ 3m  running  1.2k │ │
│    ✎ patch  ci.yml   +8 −1                │ │ ✓ 9m  completed 12k │ │
│    Tests pass. Rolling out…               │ │   ↳ approve pending │ │
│                                            │ │   [A]pprove [D]eny  │ │
```

### 4.3 Narrow (< 80 cols) — survive gracefully

Cockpit collapses to one row (model chip + gauge + status dot). Composer drops to 2 rows + placeholder teaching. Turn heads merge into one line. Drawer always full-overlay. Stat lines shed segments by the existing drop-priority machinery (`statLine`).

---

## 5. The Cockpit (kills D3, D9)

**Persistent row** (left → right): brand · model chip (`⚡` streaming badge, `⌥⏎`/click to switch) · sandbox dot · session cost (when priced) ··· context gauge (amber > 60%, red > 85%, one flash on `context_trimmed` signal) · session tokens · connection dot (green / amber > 1s RTT / red lost).

**Popover** (`h`, or click the cockpit): server card — version, uptime, ws connections, stream state; budget card — limits from `/api/limits` + spend-vs-cap bar; lifetime usage from `/api/usage`; connection list with kick. `/stats` becomes an alias that opens this popover on the session tab, which keeps per-turn history (turn count, thinking ratio, cache totals) — nothing is lost, everything is finally in *one* place.

Typography of the cockpit: values bright, labels muted, glyphs amber. Numbers never repeat elsewhere except the per-turn heads (which are about *that* turn, not the session).

---

## 6. The transcript — turn cards & typed renderers (kills D5, D9)

**Turn cards.** Every exchange gets a head row: author glyph + age (user) or telemetry summary (assistant: latency, tools, tokens, cost). Per-turn stat lines move from the *foot* to the *head* — you scan what a turn cost *before* reading it. Collapse a turn (`c` or click head) to its head row + final sentence. Jump between turns with `[` / `]`.

**Reasoning accordions** adopt the WebUI's proven rule: auto-expand while its turn is live (with auto-follow), auto-collapse when the next turn starts; manually-opened history stays open; resumed transcripts start collapsed. (Today: always-capped excerpt — close, but the live auto-expand is what makes thinking models feel fast.)

**Typed tool renderers.** The step line stays one-line (glyph · name · arg · duration · status). What changes is *inspect depth*: expanding picks a renderer by tool/shape —

| Renderer | Trigger | Inspect view |
|----------|---------|--------------|
| **diff** | `patch`, `batch_patch`, `write_file` | unified diff, `+`/`−` tinted, per-file diffstat in the step line (`+42 −7`) |
| **file** | `read_file`, `batch_read` | line-numbered excerpt with current-line marker |
| **tree** | `tree`, `glob`, `search_files` (paths) | indented tree, matching lines dimmed |
| **json** | `json_query`, JSON-shaped results | collapsible key path view |
| **tests** | shell containing `go test`/`pytest`/`jest` | pass/fail summary line, failing test names extracted |
| **fallback** | everything else | today's capped plain text |

All renderers are pure functions `(data, width) → []string` with golden tests — no new dependencies (diff parsing from `diff` tool output and our own arg shapes; shell test-summary parsing is a small matcher like `progress.go`).

**Answer emphasis.** Final answer prose is the only full-brightness block in a turn; steps and reasoning sit one step dimmer. Implemented as a per-role brightness rule in the theme, not new colors.

---

## 7. The Spine — palette + drawer (kills D2, D6, D7)

### 7.1 Command palette (`^K`)

One fuzzy-searchable surface over: slash commands · sessions (resume) · models (switch) · drawer tabs · actions (cancel run, clear, export transcript, kick connection, shutdown server…). Hand-rolled scorer (~60 lines, no deps). Typing `/` inside it filters to commands (today's palette becomes a mode). Arrow keys + `⏎`; `esc` closes to exactly where you were.

The palette is also the *teaching* surface: every entry shows its chord (`resume session  ^R`) — users graduate from palette to chords naturally.

### 7.2 The drawer — one frame, seven tabs

```
╞═ sessions · runs · events · memory · skills · tools · config ═════╞
│  /search (where supported)                    [1–7] tabs · esc ✕  │
│  …tab content, windowed rows, per-tab footer hints…                │
```

| Tab | Maps to (WEBUI.md) | Key capabilities |
|-----|--------------------|------------------|
| sessions | `/api/sessions` + CRUD/export | current panel upgraded: search, pin, rename, export, load-more, per-session tokens |
| runs | `/api/prompt`, `/api/runs/*` | start headless run, live status/tokens/elapsed (3s poll while visible), cancel, **inline approve/deny/trust** via the remote approval bridge |
| events | `/api/events` | `odek.event/v1` ring, filter by run/session; arg hashes & redacted fields only (by design) |
| memory | `/api/memory*` | facts by target with add/remove, pending-review episode queue with promote, consolidate |
| skills | `/api/skills` + promote | provenance badges (`needs review`, `untrusted`), usage counts, promote (force-gated) |
| tools | `/api/tools`, `/api/mcp` | registry with enabled state; MCP servers with limits (env withheld server-side) |
| config | `/api/config`, `/api/health`, `/api/usage`, `/api/connections` | sanitized config view, lifetime usage, live connections with kick, server shutdown (confirm-gated, typed) |

Drawer keys are uniform: `1–7` jump, `]`/`[` cycle, `/` search, `⏎` act, `d` delete-with-confirm, `esc` close. Tab-specific keys live in each tab's footer only.

### 7.3 Modality ladder (documented, enforced)

```
palette  >  drawer  >  approval queue  >  popups(@,/)  >  composer  >  transcript
```
`esc` always steps down one rung. `^K` works from every rung. Quit (`^C`) confirms only when a run is live or the composer holds a draft.

---

## 8. Approval queue (kills D4 — ship this first, even standalone)

Replace the single `m.approval` pointer with a FIFO queue:

- Panel shows **`1 of N`**, the current request (risk chip · plain-language description · verbatim command, `tab` to expand), and remaining count.
- Keys: `A` approve · `D` deny · `T` trust (when `allow_trust`) · `tab` expand · transcript scroll still works. WebUI keyboard parity.
- **Friction unchanged**: typed literal `approve`, trust withdrawn, count shown — our existing implementation is correct and stays.
- Batch requests from the server render as one card per command with per-command risk classes (the server already classifies every command in `parallel_shell`/`batch_patch`).
- Trust/friction state survives queue advancement; `approval_ack` advances the queue with a one-line confirmation.

---

## 9. Composer & status bar

**Composer**: existing textarea, plus — attachment chips row (`📎 name ✕`, drag-free terminal idiom for `/attach`), queue-depth indicator, placeholder that teaches (`ask… · @file · /command · ^K everything`), `⌥⏎` = send-with-thinking toggle preview. Multi-line indicator when the draft spans rows.

**Status bar**: one row, two jobs. Left: connection + run state + context-aware hints (the *only* place hints live; they change with focus — composer focused → send keys; transcript scrolled → jump keys; drawer open → tab keys). Right: session tokens, RTT, `? help · ^K`. This absorbs today's footer and kills the double status line.

---

## 10. Empty, first-run, and recovery states (kills D8)

- **First run** (no sessions, fresh server): a welcome card — connection summary (from `server_info`: version, model, sandbox, stream), three suggested prompts as sendable rows, one-line "⌘K"-style tour hint. No animation, no wall of text.
- **Disconnected**: a recovery card, not a notice — retry (`r`), server log path (`o` to open hint), the attach URL, and "your draft is safe" reassurance.
- **Spawn failure** (`odek` not found): remediation panel — install hint, `--odek-bin` flag, `--url` attach alternative. Never a raw Go error string.
- **Empty drawer tabs** explain themselves ("no headless runs yet — start one with `r`") and teach the creating action.

---

## 11. Visual language — EMBER Terminal (kills D1, D9, D10)

**Token mapping** from the WebUI (`ui/style.css` → `styles.go`), faithful to the brand:

| WebUI token | Terminal value | Use |
|-------------|----------------|-----|
| `--amber #ffb224` | `#FFB224` | primary accent: brand, selection, answer emphasis |
| `--amber-hi #ffc95e` / `--amber-lo #ff8a3d` | same | gradient endpoints (banner), user turn bar |
| `--bg-2 #10131a` | *(n/a — transparent)* | TUIs sit in the user's terminal; surfaces are spacing + hairlines, not fills |
| `--line rgba(152,170,200,.15)` | `#2E3242` | hairlines: rules, borders, tree glyphs |
| `--text` / `--text-2` | `#E7E9EE` / `#9AA0AE` | body / muted |
| semantic green/yellow/red | `#34D399` `#FBBF24` `#F87171` | status, pass/fail, risk (unchanged) |

- **Purple retires as primary**; it remains available in a `classic` theme (one config value, mechanical token swap) for existing users. Pink user-bars become amber-warm; assistant turns neutral-bright — the *answer* owns the accent.
- **Hierarchy by weight + brightness, not hue count.** Bold for heads, bright for answers, muted for machinery, faint for chrome. The eye gets exactly one anchor per region.
- **Themes**: `ember-dark` (default) · `ember-light` (parity with WebUI light tokens: `#F6F4EF` base, `#D98E00` amber) · `high-contrast` (pure B/W + amber) · `classic` (today's palette). 16-color terminals: lipgloss `colorprofile` degrades automatically (dep already present).
- **Motion budget**: spinner, gauge flash on threshold crossing, selection blink. `NO_MOTION=1` freezes all of it. Nothing else animates — ever.
- **Emoji discipline**: emoji stay only in `progress.go` narration (they're load-bearing charm); chrome and labels use monochrome glyphs. Today's mix of emoji+glyphs in chrome reads noisy at small sizes.

---

## 12. Adaptive behavior summary

| Breakpoint | Cockpit | Composer | Drawer | Stat lines |
|------------|---------|----------|--------|-----------|
| narrow < 80 | 1 row: model + gauge + dot | 2 rows, teaching placeholder | full overlay | essentials only (drop-priority exists) |
| standard 80–119 | 2 rows, full cluster | 3 rows + chips | full overlay | default |
| wide ≥ 120 | 2 rows | 3 rows + chips | **split pane** (opt-in `w`) | default + link detail |

---

## 13. Implementation plan

Grounded in the current code; each phase ships green (tests + lint) and independently useful.

### Phase 0 — Foundations (~1 week)
- Theme tokenization: `styles.go` rebuilt as named EMBER tokens + `theme` variant struct; `classic` alias. No layout changes yet.
- **Approval queue** (D4) — correctness fix, shippable standalone: queue state, `1 of N` panel, A/D/T keys, friction unchanged. Tests: parallel `approval_request` ordering.
- Keymap router: one table (context → keys → action) driving both handling and hint rendering; ladder documented in `/help`.
- Width breakpoint helpers + relayout hardening; property test: no viewport overflow at 60…250 cols.

### Phase 1 — The Stage (~1–1.5 weeks)
- Turn cards: head rows, collapse (`c`), jump (`[`/`]`), stat-line relocation; transcript prefix cache reworked accordingly.
- Reasoning accordion with the live auto-expand rule.
- Typed renderers (diff, file, tree, json, tests) as pure functions + golden tests; wired into `renderStep` expansion.
- Cockpit consolidation + popover (`h`); `/stats` becomes the popover's session tab. Status bar merge (absorb footer).

### Phase 2 — The Spine (~1 week)
- Command palette (`^K`): entries registry (commands, sessions, models, actions), fuzzy scorer, chord teaching.
- Drawer frame + `sessions`/`runs`/`events` tabs; runs polling + remote approvals bridge; events filter.
- Split pane on wide terminals (opt-in).

### Phase 3 — Management (~1 week)
- `memory`, `skills`, `tools`, `config` tabs (facts CRUD, episode promote, consolidate, skill promote, MCP listing, config view, connections kick, shutdown confirm).
- Health/usage data into the cockpit popover.

### Phase 4 — Polish (~0.5–1 week)
- First-run, disconnect, and spawn-failure states.
- Light + high-contrast themes; `NO_MOTION`; 16-color pass.
- Golden layout snapshots at 3 breakpoints; README + `/help` refresh.

**Testing strategy**: golden renders (plain-text) per breakpoint; property tests for layout math; interaction tests for queue/palette/drawer grammar; existing integration stand-ins extended with a queue + drawer scenario. No new dependencies.

---

## 14. Risks & explicit trade-offs

- **Split pane in Bubble Tea** (two viewports, one frame) is the highest-complexity item → opt-in, Phase 2, cut-line to full-overlay if it fights the framework.
- **Renderer cost** — typed renderers run on expand only; step lines cache; the existing prefix-cache pattern absorbs the rest.
- **Theme churn** — token swap is mechanical, but muscle memory for colors is real → `classic` ships on day one.
- **Palette vs chords** — palette-first can slow experts; mitigated by chord labels in every palette row (graduation path, not lock-in).
- **A11y in terminals is bounded** — we do: no color-only signals (glyphs always accompany state), reduced-motion, high-contrast, and screen-reader-friendly plain output via `--plain` (stretch: transcript-only stdout mode).

## 15. Success criteria

- Any surface ≤ `^K` + 3 keystrokes; every key chord discoverable in ≤ 1 look (`?`).
- Parallel approvals: zero dropped/overwritten requests (regression test as the gate).
- Long-turn scanability: answer visible within 1 screen of the last tool step via collapse/jump.
- One place to read connection/model/context/cost state (the cockpit), verified by a "state inventory" test that fails on duplication.
- Narrow-terminal usability: full core loop (prompt → approve → read answer) at 70×24.
