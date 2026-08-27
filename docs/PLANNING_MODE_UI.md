# Planning Mode — UI Integration Design (bodek)

Status: **implemented** on `feat/planning-mode-ui` (Option A strip, semantic
transcript rows, drawer tab after Events — decisions §8.1–8.3 resolved).

odek's planning system (`docs/PLANNING.md` in the odek repo) gives the engine a
structured task plan: one `plan` tool (`create`/`update`/`complete`/`get`), a
mutex-serialized store, and a protected `[Current plan:` system message that
survives trimming and restarts. Planning is **on by default**; kill switches are
CLI → env → global config. This document designs bodek's read-only surface onto
that state.

bodek is a pure front-end — everything here consumes existing odek protocol.
**No changes to odek are required or proposed.**

---

## 1. Engine contract (verified facts)

Protocol surfaces available today:

| Surface | Shape | Notes |
|---|---|---|
| `GET /api/sessions/{id}/plan` | `{session_id, version, found, steps:[{id,title,status,note?}]}` | Read-only, GET-only by contract (POST falls through to session mutators). 404 = unknown session; `found:false` = no parseable plan; all-done collapsed plan = version set, `steps: []`. Auth + rate limiting identical to sibling session endpoints (`handleSessionByID`). |
| Session WS `tool_call{name:"plan"}` | `Data` = full JSON args | Fires for every mutation; plan calls ride ordinary parallel tool batches. |
| Session WS `tool_result{name:"plan"}` | `Data` = rendered plan text | Pairs with its call via the existing LIFO name matcher. |
| Runtime events `plan_created`/`plan_updated` | counts + version only | Delivered via `GET /api/events` ring → **already visible in bodek's Events tab**. Not forwarded on the session WS. |

Plan state model (engine): `steps[].status ∈ {pending, in_progress, done,
blocked}`; titles ≤200 chars, notes flattened; store caps from resolved config
(defaults 12 steps / 2000 render chars); monotonic `version` per effective
mutation (no-op mutations don't bump). The plan is advisory steering, not a
contract.

Two properties matter for the UI:

1. **WS carries triggers, REST carries truth.** The session WS has no dedicated
   plan event, but every mutation shows up as a `plan` tool_call/result pair.
   The structured view lives behind the REST GET. So: watch the WS, then fetch
   REST.
2. **REST survives restarts.** The endpoint parses the newest parseable plan
   message out of the persisted transcript (`loop.ExtractPlan`), so a fresh
   fetch after reconnect/attach resumes correct state with no replay work.

---

## 2. Design goals

- **Ambient visibility**: "what is the agent doing overall?" answerable at a
  glance, without opening anything.
- **Zero-footprint absence**: planning disabled, no plan yet, or an old engine
  without the endpoint ⇒ literally zero new pixels. Same discipline as the
  redundant-indicator removal rule.
- **Read-only**: REST is GET-only by engine contract; bodek never mutates plan
  state (steering lives with the model).
- **Order-faithful transcript**: plan tool steps stay timeline items in arrival
  order; we specialize rendering, not ingestion.
- **Everything model-derived through `sanitize()`** — titles/notes come off the
  wire.

---

## 3. Architecture

### 3.1 Client layer (`internal/client`) — `runs.go` sibling file: `plan.go`

```go
type PlanStep struct {
    ID     string `json:"id"`
    Title  string `json:"title"`
    Status string `json:"status"` // pending | in_progress | done | blocked
    Note   string `json:"note"`   // omitted when empty
}

type PlanSnapshot struct {
    SessionID string     `json:"session_id"`
    Version   int        `json:"version"`
    Found     bool       `json:"found"`
    Steps     []PlanStep `json:"steps"` // nil-safe: treated as empty
}
```

`Client.SessionPlan(sessionID string) (PlanSnapshot, error)` — plain GET,
`url.PathEscape(id)`, mirrors the `/api/sessions/{id}` call path. New
dependency-free code; tested against `httptest` fixtures like `spec_test.go`
(200 found, `found:false`, 404, malformed body).

### 3.2 State layer (`internal/tui/plan.go` — new file)

Model additions:

```go
plan          PlanSnapshot // last accepted snapshot
planFetchSeq  int          // request ordinal — guards out-of-order replies
planPolling   bool         // drawer plan tab visible (arms tick poll)
```

TEA messages: `planMsg{snap PlanSnapshot; seq int; err error}`,
`planTickMsg{seq int}` (reuses the runs-tab tick pattern).

Refresh triggers (all funnel into one debounced fetch):

1. **WS trigger**: any `tool_call`/`tool_result` with `Name == "plan"` schedules
   a fetch ~250 ms trailing-edge — collapses the common create→update burst of
   one iteration into a single request. Never fires while idle-to-plan changes
   (there are none: idle means no tool calls).
2. **Lifecycle triggers** (immediate, not debounced): tab activation, session
   switch (`SessionSwitch`), successful reconnect resume (`reconnect.go` hook),
   attach to an existing session.
3. **Fallback poll**: every 3 s while the drawer plan tab is visible only
   (identical lifecycle to `runPollEvery`). No blind timers when hidden.

Acceptance rule: a fetched snapshot replaces local state iff
`snap.Version >= m.plan.Version` and it belongs to the current session.
Monotonic guard makes duplicate/out-of-order responses harmless — ingestion
stays idempotent.

Error posture: silent degradation. A failed/404 fetch hides the strip, marks
the tab "unavailable", never surfaces as an error card. The feature must not
generate noise when an old `odek serve` lacks the route.

### 3.3 Event handling hook (`internal/tui/events.go`, minimal diff)

In the `tool_call` case: if `ev.Name == "plan"`, also call
`m.schedulePlanRefresh()`. That's the entire change to hot-path code — one line
plus the helper.

---

## 4. UX surfaces

### A. Transcript step specialization *(recommended, medium effort)*

Today each plan step renders as a generic row with JSON arg preview — and plan
calls fire *per status change*, so long runs would drown in rows like
`plan {"verb":"update","updates":[…]}`.

Specialize rendering, keep ingestion untouched:

```
✔ plan   create · 5 steps            [Ctrl+E reveals raw args/result]
🔄 plan   s2 → in_progress
✔ plan   s1 → done
```

Verbs map to compact glyphs: create `+N steps`, complete `id → done`,
update per-update list, get folded to nothing visible beyond the row glyph.
All rendered text still passes through `sanitize()`; `expandAll` (Ctrl+E)
keeps revealing the raw args/result exactly like other tools.

Open sub-question for the tally/stats: keep counting plan calls in `toolTotal`.
They are genuine tool calls; hiding them from stats would be dishonest.

### B. Live plan strip *(recommended, small)*

A single transient line adjacent to the busy indicator (thinking spinner /
last-tool line — placed below the last user input per established preference),
shown only when: run active **and** `plan.found`.

```
⠸ 🧠 thinking                      ▸ plan 2/5 · s3 wire flag parsing · ⛔1
```

- Content: `done/total`, first `in_progress` title truncated to fit, blocked
  count when nonzero. Version silently consistency-checks the poll cadence.
- Hidden when: no plan, all done (collapsed), disconnected, old engine.
- All-done confirmation rides the final WS trigger: strip is replaced by the
  normal idle state; the persistent record lives in the tab, not chrome.

### C. Drawer Plan tab *(recommended, medium)*

New `panelPlan` placed after Events (drawer tabs today: sessions, runs, events,
memory, skills, tools, config — models stays its own ^O overlay), giving
1 sessions · 2 runs · 3 events · 4 plan · 5 memory · 6 skills · 7 tools ·
8 config; digits shift by one from memory onward. Rendered exactly like the Telegram surface for
cross-surface consistency:

```
📋 Plan — v7 · 2/4 done · 1 blocked      ⏎ detail · esc fold/close

  ⬜ p1  scaffold command skeleton
  ✅ p2  wire flag parsing
  🔄 p3  resolve config precedence      note preview…
  ⛔ p4  license policy decision        blocked
```

- Header summary first; one row per step: status glyph, id, title (flattened,
  truncated), note preview.
- Detail submode follows house rules: `⏎` expands the selected row's full
  note/title through `sanitize()`; `esc`/`q` folds back; `p` promote is a
  no-op here (nothing to promote); tab switches reset the submode
  (`switchDrawerTab` already does this).
- Strictly read-only — no mutation controls exist to fake.
- Empty states: `found:false` → muted "no active plan in this session.";
  collapsed all-done → "✓ all steps done · vN"; unavailable → "plan endpoint
  unavailable".

### D. `/plan` slash command *(recommended, trivial)*

Registry entry ("structured task plan of this session") opening panelPlan. Free
autocomplete + palette listing. No inline printing variant until wanted — the
tab is cheap to open.

### E. Block notifications *(deferred)*

A `⛔` arriving mid-turn is arguably notable, but notices expire/distract and
the WS-triggered strip update already lands within ~300 ms. Skip v1;
revisit if long-run ergonomics demand it.

---

## 5. Edge cases

| Case | Handling |
|---|---|
| Old `odek serve` without the route | Silent degrade: strip hidden, tab unavailable. No retries while degraded except explicit tab activation. |
| Unknown session (404) pre-first-prompt | Same silent path; once the `session` event names us, normal behavior applies. |
| Out-of-order REST replies | Monotonic `version` guard + request seq mismatch discard. |
| Create→update burst in one iteration | Trailing-edge debounce (~250 ms) collapses to one fetch. |
| Parallel batch plan calls | Store serializes server-side; client pairs results via existing LIFO matcher unchanged. |
| Reconnect / restart resume | Fetch on reconnect-success — REST reads persisted transcript. |
| Session switch / attach | Clear snapshot before switching; immediate fetch after acceptance. |
| Hostile titles/notes | `sanitize()` on every render; server already flattens + caps lengths. |
| Collapsed all-done plan | `steps == []`, version intact — tab summary line, strip hidden. |
| Drawer width overflow | Tab strip already collapses to ellipsis + active — free. |
| Tests & flakiness | Drive `handleEvent`/msgs directly (newTestModel); no wall-clock assertions; poll ticks are explicit messages. |

---

## 6. Testing strategy (per AGENTS.md)

- `internal/client/plan_test.go`: fixtures mirroring `spec_test.go` — shape,
  `found:false`, 404 error mapping, malformed body tolerance.
- `internal/tui/plan_test.go`:
  - strip appears/hides across run-active × found × collapsed matrix;
  - WS trigger → scheduled refresh cmd observed (not executed);
  - monotonic guard rejects stale versions; duplicate WS triggers idempotent;
  - tab rendering incl. truncation, note preview, blocked glyph, detail expand,
    sanitize coverage with hostile strings;
  - reconnect/session-switch hooks issue fresh fetches.
- Regression rules: race detector suite green, no timing assertions, coverage
  stays at the package norm.

## 7. Milestones (strictly sequential)

| # | Scope |
|---|---|
| M-P1 | client types + `SessionPlan` + tui state, debounce, guards, hooks (transcript unaffected) |
| M-P2 | transcript semantic rendering for `plan` steps (+ stats decision documented above) |
| M-P3 | drawer Plan tab + detail submode + visible-poll lifecycle |
| M-P4 | live strip + `/plan` command + README/keybinding sync |

Each milestone ships README touch-ups in the same commit where user-visible
behaviour changes land (repo rule).

---

## 8. Open questions for brainstorm

1. **Strip vs chip**: dedicated mini-line next to the busy indicator (as drawn)
   vs appending `▸ plan 2/5` into the existing status line? The mini-line can
   hold the current step title; the chip costs zero layout risk.
2. **Transcript visibility**: semantic single-liners (proposed) vs suppressing
   plan rows entirely and surfacing only the final "plan updated" heartbeat?
   Suppression is quieter but breaks the "every act leaves evidence" property.
3. **Tab position**: accept digits 5–8 shifting (insert plan at 5) for
   semantic grouping?
4. Anything else wanted from v1 — e.g. copy-step-as-text action, or a jump
   from strip to tab?
