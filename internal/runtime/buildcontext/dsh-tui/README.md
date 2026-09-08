# dsh-tui

`dsh-tui` is the OpenTUI/Solid frontend for a running DeepSeek Harness Web host.
It never reads DSH persistence files or implements agent behavior locally.

## Usage

The client opens or creates a DSH session, renders its event stream, sends and
cancels prompts, and supports workspace-safe `--continue`/`--session` selection.
It exposes session and model pickers with `Ctrl+L` and `Ctrl+M`, respectively.
Use `@` in the composer for DSH-backed file and directory references. Model
routes always come from DSH's catalog; no provider or model list is local.

Slash commands are dynamic: every host command DSH advertises for the session
(including plugin commands such as `/agent-teams`) is discovered through
`commands/list`, completed with its description and input hint, and executed
through `commands/execute`. TUI-local commands (`/model`, `/sessions`, `/new`,
`/continue`, `/help`, `/quit`) win name collisions. `Ctrl+P` opens the command
palette.

The sidebar shows a context meter (`used / context-window tokens (percent)`)
derived from DSH's `contextPressure` projection and updated live via
`session/control`, and — when DSH's MCP client plugin is loaded — an MCP
section listing each configured server with a colored activation dot.

While attached, `dsh-tui` writes a 10-second manager heartbeat at
`$HOME/.local/state/opencode-manager/deepseek-status.json`. OCM uses it to
show `starting`, `working`, `waiting`, `sleeping`, or `error` in the workspace
dashboard; closing the TUI marks the DSH client off after the heartbeat expires.

Submitted composer prompts are retained across dsh-tui restarts for up-arrow
recall in `$HOME/.local/state/opencode-manager/dsh-prompt-history.json`. The
file is owned by the workspace user and written with mode `0600`.

When the `dsh-agent-teams` plugin is installed, `Ctrl+X T` or
`/agent-teams-dag` opens a task-DAG overlay (tasks layered by dependencies,
state markers, assignees, member progress) fed by the plugin's
`/plugins/dsh-agent-teams/state` panel route; `Enter` refreshes it.

Inside an OCM workspace, `dsh-tui` builds its launch URL from
`OCM_DSH_PORT` and `$DSH_HOME/web-token`:

```sh
dsh-tui
dsh-tui --continue
dsh-tui --session SESSION_ID
bun run smoke
```

`--dsh-url` remains available for an explicitly managed external host.

`bun run smoke` intentionally creates a test session and sends a prompt. Run it
only against a development DSH profile.

The official DSH Cordis client is browser-bound in `0.1.2-rc.1`; its external
Bun bootstrap is not published. The cookie-authenticated Remote fallback is
therefore isolated under `src/dsh/remote-gateway.ts` until an official external
client API is available.
