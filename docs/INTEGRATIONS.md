# odek serve integration coverage

The contract: every endpoint and protocol message of `odek serve` (WEBUI.md)
has a bodek client method and a UI surface. This matrix is the audit trail —
update it when either side changes.

## REST endpoints

| Endpoint | Client method | UI surface |
|----------|--------------|------------|
| `GET /api/resources` | `Resources` | `@` completion popup |
| `GET /api/models` | `Models` | model picker, context gauge |
| `GET /api/profiles` | `Profiles` | model picker (merged catalog), gauge fallback |
| `GET /api/sessions` (bare) | `Sessions` | palette session entries |
| `GET /api/sessions?q&limit&offset` | `SearchSessions` | sessions tab (search, load-more) |
| `GET /api/sessions/{id}` | `SessionDetail` | resume (token bootstrap via `X-Session-Token`) |
| `POST /api/sessions/{id}` | `UpdateSession` | pin (`p`), rename (`r`) |
| `DELETE /api/sessions/{id}` | `DeleteSession` | sessions tab (`d`) |
| `GET /api/sessions/{id}/export` | `ExportSession` | sessions tab (`e` md, `E` json) |
| `POST /api/cancel` | `Cancel` | cancel REST fallback (WS cancel is primary) |
| `GET /api/limits` | `Limits` | cost rendering (`PricesFor`), cockpit budget |
| `GET /api/health` | `Health` | cockpit live snapshot (started-at) |
| `POST /api/prompt` | `StartRun` | `/run`, palette "run headless" (draft) |
| `GET /api/runs` | `Runs` | runs tab (3s poll while visible) |
| `GET /api/runs/{id}` | `RunDetail` | runs tab enter/refresh |
| `DELETE /api/runs/{id}` · `POST …/cancel` | `CancelRun` | runs tab (`c`) |
| `GET /api/runs/{id}/approvals` | — (in list/detail) | runs tab approval badge |
| `POST /api/runs/{id}/approvals/{aid}` | `AnswerRunApproval` | runs tab (`A`/`D`/`T`) |
| `GET /api/events` | `RuntimeEvents` | events tab (`f` session filter) |
| `GET /api/usage` | `Usage` | cockpit lifetime, config tab |
| `GET /api/connections` | `Connections` | config tab |
| `DELETE /api/connections/{id}` | `KickConnection` | config tab (`d`) |
| `GET /api/config` | `ConfigView` | config tab |
| `GET /api/mcp` | `MCPServers` | tools tab |
| `GET /api/memory` | `Memory` | memory tab |
| `POST /api/memory/facts` | `AddMemoryFact` | memory tab (`a` user, `A` env) |
| `DELETE /api/memory/facts` | `DeleteMemoryFact` | memory tab (`d`) |
| `POST /api/memory/episodes/promote` | `PromoteEpisode` | memory tab (`p`) |
| `POST /api/memory/consolidate` | `ConsolidateMemory` | memory tab (`c` user, `E` env) |
| `GET /api/skills` | `Skills` | skills tab (provenance badges) |
| `POST /api/skills/promote` | `PromoteSkill` | skills tab (`p`, `P` force) |
| `GET /api/tools` | `Tools` | tools tab |
| `POST /api/shutdown` | `Shutdown` | config tab (`S` + typed "shutdown" death-gate) |

## WebSocket protocol (`/ws`)

| Message | Direction | Client | UI |
|---------|-----------|--------|----|
| `prompt` (content/model/thinking/attachments) | → | `SendPrompt` | composer ⏎ |
| `approval_response` | → | `SendApproval` | approval queue (`A`/`D`/`T`, friction typing) |
| `ping` | → | `Ping` | 25s heartbeat |
| `cancel` | → | `SendCancel` | `esc` (REST fallback) |
| `session_switch` | → | `SessionSwitch` | session resume, reconnect re-adopt |
| `skill_prompt_response` | → | `SendSkillPromptResponse` | suggestion card (`alt+s`/`alt+x`) |
| `server_info` / `pong` | ← | — | cockpit, stream badge, RTT |
| `session` | ← | — | session/token capture, model sync |
| `token` / `token_delta` | ← | — | transcript answer (coalesced) |
| `thinking` / `thinking_delta` | ← | — | reasoning accordions |
| `tool_call` / `tool_result` | ← | — | step lines + typed renderers |
| `subagent_log` | ← | — | nested sub-agent logs |
| `usage` | ← | — | live context gauge |
| `done` | ← | — | turn head telemetry (tokens, cache) |
| `error` | ← | — | error bubble / cancel markers |
| `cancelled` | ← | — | clean cancel close-out |
| `approval_request` / `approval_ack` | ← | — | approval queue |
| `skill_event` / `memory_event` / `agent_signal` | ← | — | transient notes (+ suggestion card) |
