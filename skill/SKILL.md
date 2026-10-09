---
name: warm-compact
description: Use when the user asks not to compact this session, to allow or force compaction of this session, to put it back to the default, or asks about Warm Compact / herdr auto-compaction of idle sessions.
---

# Warm Compact: this session's setting

Warm Compact (a herdr plugin) compacts idle Claude Code sessions shortly before their prompt cache expires.
Each session follows the global default unless it has its own setting:

- `default`: follow the global default
- `auto`: compact when the context is at or over the size threshold
- `on`: compact whatever the size
- `off`: never compact this session

Set it for this session by running the plugin's `set` command; it targets this pane through `HERDR_PANE_ID`.

PowerShell:

    $dir = herdr plugin config-dir herdr.warm-compact
    & (Get-Content "$dir\exe-path") set off

bash:

    "$(cat "$(herdr plugin config-dir herdr.warm-compact)/exe-path")" set off

Replace `off` with what the user asked for. Report the line it prints. If the command fails (it says "Warm Compact is
not running; nothing was changed") or `exe-path` is missing, say Warm Compact is not running and suggest the
plugin's `restart` action or opening its panel from herdr.
