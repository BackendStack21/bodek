# bodek configuration reference

bodek (the terminal UI) keeps its own front-end settings, completely separate
from odek's server-side configuration (`~/.odek/config.json` — model, MCP,
memory, skills; see the [odek docs](https://github.com/BackendStack21/odek)).

## Settings file

Location: `~/.bodek/config.json` — override with the `BODEK_CONFIG`
environment variable. The file is optional; a missing file means "everything
defaulted". A *malformed* file is surfaced as an error at startup rather than
silently ignored.

```json
{
  "theme": "ember-dark",
  "bel": false,
  "notify": true,
  "plain": false
}
```

| Key | Type | Default | Flag | What it does |
|-----|------|---------|------|--------------|
| `theme` | string | `ember-dark` | `--theme` | Color palette: `ember-dark` · `ember-light` · `high-contrast` · `classic`. `/theme <name>` switches live **and persists it here**. |
| `bel` | bool | `true` | `--bel` | Ring the terminal bell when a turn completes or an approval is waiting (`--bel=false` mutes; the title still updates). |
| `notify` | bool | `false` | `--notify` | Raise desktop notifications (OSC 9) on turn completion and pending approvals. |
| `plain` | bool | `false` | `--plain` | Linear mode: no alt-screen; agent events print to the terminal's native scrollback (screen readers, pipes, logs). |
| — (flag only) | string | `normal` | `--verbosity` | Startup noise dial: `quiet` (engine traces hidden), `normal`, `detailed` (`^E` expand-all view). `/verbosity` switches at runtime; never persisted to this file. |

Unset keys fall back to their defaults — the file only ever stores choices
you actually made (`/theme` writes `theme`; the rest you write by hand).
A leftover `"mouse"` key from older bodek builds is ignored: the
alt-screen always reports the mouse so the wheel can scroll.

## Resolution order

Every setting resolves the same way:

```
explicit flag  →  BODEK_THEME env (theme only)  →  settings file  →  built-in default
```

`/theme` persists to the settings file, so the next launch starts where you
left off unless a flag or `BODEK_THEME` overrides it.

## Environment variables

| Variable | Affects | Behavior |
|----------|---------|----------|
| `BODEK_THEME` | startup theme | Startup palette override (still beaten by an explicit `--theme`). |
| `BODEK_CONFIG` | settings path | Alternative path for the settings file (used by tests and shared-dotfile setups). |
| `NO_COLOR` | all rendering | Any non-empty value degrades the color profile to plain text in every mode — standard nocolor.org semantics. |
| `NO_MOTION` | animations | Any non-empty value replaces animated spinners/progress with single static frames. |
| `DEEPSEEK_API_KEY` / `OPENAI_API_KEY` / `ZAI_API_KEY` / … | odek (not bodek) | Provider keys read by the `odek serve` engine bodek spawns. Set `ODEK_PROVIDER` to match. `ODEK_API_KEY` is a v1 selected-provider override only. |

## odek-side configuration

Everything the agent does is configured on the odek side
(`~/.odek/config.json` → `./odek.json` → `ODEK_*` env vars): provider, model,
provider keys, MCP servers, memory, skills, sandbox. bodek inherits it
unchanged — see the
[odek repository](https://github.com/BackendStack21/odek) for the full
server configuration reference.
