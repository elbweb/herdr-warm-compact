# herdr-warm-compact

A [herdr](https://herdr.dev) plugin that compacts idle Claude Code sessions shortly before their prompt
cache expires, so coming back costs a short summary instead of an uncached re-read of the whole context.

When a large Claude Code session sits idle, its prompt cache eventually expires (after one hour on a
subscription's main conversation, five minutes on usage credits or an API key). Resuming afterwards re-reads
the entire context uncached. Warm Compact compacts the session a few minutes before that point, while the
cache is still warm. Every session shows where it stands in herdr's sidebar, and you can say per session
whether it may be compacted.

Not affiliated with Anthropic or herdr.

## Install

Requires herdr 0.9.3 or newer and a Go toolchain (the plugin is built on install).

    herdr plugin install elbweb/herdr-warm-compact

The plugin starts once with herdr and keeps running. To update, run the install again, then the plugin's
`restart` action, which asks the running copy to stop and starts a fresh one.

Run the Stop action ("Warm Compact: stop") before disabling or uninstalling the plugin: the running copy is
a separate process and otherwise keeps working until it notices its executable is gone or herdr has been
unreachable for five minutes.

On a machine with an attack-surface-reduction rule (for example Windows Defender ASR), allow herdr's plugins
folder so the freshly built binary may run.

## The one setting

- **Default** (global): `auto` or `off`, applied to every session without its own setting.
- **Per-session setting:**
  - `default`: follow the global default
  - `auto`: compact when the context is at or over `min_tokens`
  - `on`: compact whatever the size
  - `off`: never compact this session

A session on a 5-minute cache is never compacted, even under `on`, unless `five_minute_ttl = true`.
The setting belongs to the Claude session, not the pane, so a resumed conversation keeps it.

## Seeing and controlling it

All of these change the same per-session setting, at any time.

1. **The panel.** A terminal UI listing every Claude session, grouped by workspace, with its tokens, setting,
   cache lifetime and status. It is 36 columns wide whatever its window, so it reads the same in a phone client
   such as Collie. Arrow keys select a row; Enter, Space, a click or a tap cycles its setting; "compact now" and
   "skip this time" are per row. The header shows the global default (`d` switches it) and any config error;
   `?` shows every key, plugin uptime and the last event. `Tab` switches to **Settings**: every key of
   `config.toml` (below) with its value, marked `(default)` when the file does not set it. Enter cycles a
   choice or opens an editor (Enter saves, or Ctrl+S for the multi-line `instructions`; Esc cancels), and `r`
   resets a key to its default. Only that key's line changes, so the rest of the file keeps its comments,
   and a value the plugin would reject is refused with the reason before anything is written. Open it with
   the plugin's `open-panel` action ("Warm Compact: panel"), as a popup or, with `panel = "tab"` in the
   plugin's config, as a tab that stays open (and so shows in a phone client). To put the action on a key, add
   to herdr's `config.toml` (`prefix+a` is free in herdr's defaults):

       [[keys.command]]
       key = "prefix+a"
       type = "plugin_action"
       command = "herdr.warm-compact.open-panel"
       description = "Warm Compact panel"

   From a shell, `herdr plugin pane open --plugin herdr.warm-compact --entrypoint panel --placement tab` opens
   it as a tab, and asking Claude to "open the warm compact panel" does the same.
2. **From inside Claude.** Tell a session "don't compact this one"; the [skill](#the-claude-skill) runs the
   plugin's `set` command for that session's pane.
3. **The sidebar token** `$compact` (below).
4. **A key (optional).** The `toggle` action ("Warm Compact: cycle this session's setting") works on a pane and
   can be bound to a key in herdr's config.

The plugin's actions: `open-panel` ("Warm Compact: panel"), `toggle` ("Warm Compact: cycle this session's
setting"), `restart` ("Warm Compact: restart") and `stop` ("Warm Compact: stop").

### The sidebar token

Add `$compact` to your agent rows in herdr's `config.toml`:

```toml
[ui.sidebar.agents]
# state + tab title + warm-compact countdown on line 1; agent and workspace dimmed on line 2
rows = [
  ["state_icon", "tab", { token = "$compact", rules = [
    { starts_with = "⚠", fg = "#f7768e", bold = true },
    { starts_with = "·", fg = "#f7768e", dim = true },
    { starts_with = "✗", fg = "#f7768e", bold = true },
    { starts_with = "⏳", fg = "#e0af68" },
    { equals = "off", dim = true },
  ] }],
  [{ token = "agent", dim = true }, { token = "workspace", dim = true }],
]
```

Values:

| Token | Meaning |
|---|---|
| `⏱ 38m` | armed: minutes until it compacts, updated once a minute (`⏱ 38m on` when the session has its own setting) |
| `⚠ 0:42 DRAFT` | warning window; alternates with a `·` prefix every second so it reads as flashing. `DRAFT` means text in the prompt box will be stashed and restored |
| `⏳ compacting` | compaction in progress, including stashing and restoring a draft |
| `off` / `on` / `auto` | a session with its own setting that is not currently armed |
| `✗ <reason>` | a failure; stays until you act, and also raises a toast |

Sessions that are not armed and have no setting of their own show nothing (under `show = "armed"`). Tokens expire after a few minutes, so if
the plugin dies they disappear instead of freezing.

## Global limits

`config.toml` in the plugin's config dir (`herdr plugin config-dir herdr.warm-compact`). Every key is
optional; the values below are the defaults. The file is re-read when it changes; if it fails to parse, the
last good settings stay in force and the panel header shows `✗ config: <error>`.

```toml
default = "auto"         # auto | off
min_tokens = 175000      # for auto
lead = "5m"              # compact this long before the cache expires (1 h TTL: at 55 idle minutes)
warning = "60s"          # flashing + one toast this long before compacting
hold_if_active = "60s"   # postpone while the pane's screen changed this recently
five_minute_ttl = false
compact_timeout = "10m"
show = "armed"           # sidebar: "armed" = every armed session; "warnings" = only the warning window,
                         # failures and overridden sessions
panel = "popup"          # how the open-panel action opens the panel: "popup", or "tab" (stays open, so a
                         # phone client such as Collie lists it)
instructions = """
The user stepped away and will resume later. Keep open decisions, the current task and its next step,
file paths and commands in play, and anything the user said they want.
"""
```

`instructions` is the default compaction instruction, passed to `/compact`; change it to suit you.

## The Claude skill

`skill/SKILL.md` teaches Claude to change its own session's setting when you ask ("don't compact this one").
Link or copy the `skill` folder to `~/.claude/skills/warm-compact`; it then loads in every session. It runs the
plugin's `set <default|auto|on|off>` command for the session's own pane.

## How it decides

- **Arming.** Each time a pane goes idle, the tail of the session's transcript is read once. The session is
  armed when its setting allows compaction (for `auto`, the context is at least `min_tokens`) and its cache
  lifetime is 1 hour (or `five_minute_ttl` is on). The deadline is the last request time plus the cache
  lifetime minus `lead`. Anything that takes the pane out of idle disarms it.
- **Warning.** `warning` before the deadline the token starts flashing and one toast appears. Setting the
  session to `off`, or "skip this time", cancels.
- **Checks.** At the deadline it stops at the first failed check and leaves the reason on the session's row:
  the pane is still idle, the transcript has no newer entry, the screen has not changed within
  `hold_if_active`, no subagent or background task is running, the session is still eligible.
- **Compacting.** If the prompt box holds a draft, it is stashed with Ctrl+S (and the box verified empty);
  then `/compact <instructions>` is typed, and sent only once the box shows it. When the session is idle again within `compact_timeout`,
  the draft is restored, and the transcript is re-read to confirm the context shrank. A draft that cannot be
  restored is reported (`✗ draft not restored`); if you typed something new meanwhile, nothing is overwritten
  and the draft stays in Claude's stash.

## What it never does

- Act on a working or blocked pane.
- Retry: any failure is reported and left for you.
- Compact a 5-minute-cache session unless you allow it with `five_minute_ttl = true`.
- Spawn work per event or per pane: it is one resident process driven by herdr's events, with one minute
  tick; no per-event or per-pane processes.

If the plugin is not running, no countdowns appear anywhere, the panel says "not running", and `set` and
`toggle` refuse rather than silently doing nothing; the `restart` action starts it again. `herdr plugin log list --plugin herdr.warm-compact`
shows its startup process.

## Caveats

Session facts (timestamp, context size, cache lifetime) are read from Claude Code's transcript files
(under `$CLAUDE_CONFIG_DIR/projects` when `CLAUDE_CONFIG_DIR` is set, else `~/.claude/projects`), an
internal format that may change; an unrecognised transcript shows `✗ transcript unreadable` and the session
is never armed. Whether herdr's right-click menus list plugin panes or actions is unverified; use the panel
command or the actions above.

## Licence

MIT. See [LICENSE](LICENSE).
