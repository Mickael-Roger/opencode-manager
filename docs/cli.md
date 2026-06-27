# CLI Reference

`opencode-manager` is TUI-first, but ships a **minimal CLI** for automation and
scripting (for example, wiring workspaces into tmux). Running the binary with no
arguments launches the [dashboard](tui.md); with arguments it runs a command.

Both `opencode-manager` and the short alias `ocm` accept the same commands.

## `ocm` (no arguments)

Launches the interactive TUI dashboard.

```sh
ocm
```

## `ocm list`

Lists workspaces.

```sh
ocm list
```

## `ocm attach <workspace>`

Attaches the current terminal to the named workspace's OpenCode session, the same
as pressing `Enter` in the dashboard. Starts the container if needed.

```sh
ocm attach my-project
```

This is the building block for terminal automation — e.g. opening several
workspaces in separate tmux panes:

```sh
tmux new-window  'ocm attach api'
tmux split-window 'ocm attach frontend'
```

## Usage summary

```text
usage:
  opencode-manager list
  opencode-manager attach <workspace>
```

Anything else prints this usage message. Diagnostic logs go to a file, not the
terminal, so CLI output stays clean — see
[Configuration → Logging](configuration.md#logging).

## Scope

The first version deliberately keeps the CLI to `list` and `attach`. There is no
broad non-interactive workflow, no web UI, and no multi-user server mode; the
dashboard is the primary interface. A richer CLI is on the roadmap.
