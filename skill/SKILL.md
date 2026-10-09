---
name: warm-compact
description: 'Use when the user says "don''t compact this session", "never compact this", "compact this session", "allow compaction", "reset compaction to default", "open the warm compact panel", or asks about Warm Compact or herdr auto-compaction of idle sessions. Sets this session''s Warm Compact setting (default, auto, on, off), or opens the Warm Compact panel as a tab.'
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

## Opening the panel

When the user asks to open the Warm Compact panel (for example from a phone, where herdr's actions are out of
reach), open it as a tab in this pane's workspace; it stays open, so a phone client such as Collie lists it.

PowerShell:

    $ws = (herdr pane get $env:HERDR_PANE_ID | ConvertFrom-Json).result.pane.workspace_id
    herdr plugin pane open --plugin herdr.warm-compact --entrypoint panel --placement tab --workspace $ws --cwd $HOME --no-focus

bash:

    ws=$(herdr pane get "$HERDR_PANE_ID" | sed -n 's/.*"workspace_id":"\([^"]*\)".*/\1/p')
    herdr plugin pane open --plugin herdr.warm-compact --entrypoint panel --placement tab --workspace "$ws" --cwd "$HOME" --no-focus

Tell the user it is open in a tab named "Warm Compact"; `q` closes it.
