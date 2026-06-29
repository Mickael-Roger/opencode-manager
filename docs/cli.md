# CLI Reference

`opencode-manager` is TUI-first, but ships a **resource-oriented CLI** for
automation and scripting. Running the binary with no arguments launches the
[dashboard](tui.md); with a subcommand it runs non-interactively.

Both `opencode-manager` and the short alias `ocm` accept the same commands, and
the commands follow a `kubectl`-style `ocm <resource> <verb>` shape.

```sh
ocm                              # launch the TUI dashboard
ocm workspaces list              # manage workspaces
ocm templates list               # inspect templates
ocm modules list                 # inspect the module catalog
ocm config view                  # global configuration
ocm doctor                       # environment preflight
ocm version                      # print the ocm version
```

Most resources have short aliases: `workspaces` → `ws`, `templates` → `tmpl`,
`modules` → `mod`, and `list` → `ls`. So `ocm ws ls` is `ocm workspaces list`.

## Global flags

| Flag | Description |
| --- | --- |
| `-o, --output table\|json` | Output format for read commands. `table` (default) is human-readable; `json` is a stable contract for scripts. |
| `-h, --help` | Help for any command, e.g. `ocm workspaces run --help`. |

Diagnostic logs go to a file, not the terminal, so CLI output stays clean — see
[Configuration → Logging](configuration.md#logging).

## Workspaces (`ocm workspaces`, alias `ws`)

| Command | Description |
| --- | --- |
| `ws list` (`ls`) | List workspaces with status, activity, module count, and age. |
| `ws get <ws>` | Show one workspace's details, status, OpenCode version, installed modules, and token usage. |
| `ws create <name>` | Create a workspace. `--template <t>` applies a template's modules; `--start` builds the image and starts the container. |
| `ws delete <ws>` (`rm`) | Delete the workspace, its container, and its image. `--force`/`-f` skips the confirmation prompt. |
| `ws start [ws]` | Start a container (building the image if needed). `--all` starts every workspace. |
| `ws stop [ws]` | Stop a running container. `--all` stops every workspace. |
| `ws restart [ws]` | Stop then start. `--all` for every workspace. |
| `ws update [ws]` | Update OpenCode to the latest release inside the container. `--all` for every workspace. |
| `ws version <ws>` | Print the OpenCode version running in the workspace. |
| `ws attach <ws>` | Attach the terminal to the workspace's OpenCode session (same as `Enter` in the dashboard). |
| `ws shell <ws>` (`sh`) | Open an interactive shell inside the container. |
| `ws exec <ws> -- <cmd>` | Run a one-off command inside the container. |
| `ws run <ws> --prompt …` | Run a **non-interactive** OpenCode turn and print the result (headless). |

### Examples

```sh
# Create a workspace from a template and start it immediately.
ocm workspaces create api --template backend --start

# Inspect it, as JSON, for a script.
ocm ws get api -o json | jq '.tokenUsage.totalTokens'

# Run a command in the sandbox.
ocm ws exec api -- go test ./...

# Headless agent run — usable from CI, cron, and git hooks.
ocm ws run api --prompt "Summarize the open TODOs in this repo"
echo "review this diff" | ocm ws run api --prompt-file -

# Attach interactively (the building block for tmux automation).
tmux new-window  'ocm ws attach api'
tmux split-window 'ocm ws attach frontend'
```

`ocm workspaces run` executes a single OpenCode turn inside the workspace project
directory and exits, printing the agent's output. The prompt comes from
`--prompt`, `--prompt-file <path>`, or stdin (`--prompt-file -`).

## Templates (`ocm templates`, alias `tmpl`)

| Command | Description |
| --- | --- |
| `templates list` (`ls`) | List templates with their module count and last-updated age. |
| `templates get <name>` | Show a template's modules and their values. |
| `templates delete <name>` (`rm`) | Delete a template. `--force`/`-f` skips confirmation. |

Templates are **created and edited from the dashboard** (`:templates`, then `c`).
The CLI exposes them read-only plus delete, and `ws create --template` applies
one to a new workspace.

## Modules (`ocm modules`, alias `mod`)

| Command | Description |
| --- | --- |
| `modules list` (`ls`) | List the available module catalog. `--workspace <ws>`/`-w` lists the modules installed in a workspace instead. |
| `modules remove <id> -w <ws>` (`rm`) | Remove an installed module from a workspace. `--force`/`-f` skips confirmation. |

Module **installation** is interactive (it collects per-module prompt values,
some of them secret or dynamically discovered) and is done from the dashboard
(`e`) or seeded via a template. The CLI lists the catalog, lists what a workspace
has installed, and removes instances by id (the value shown in `modules list -w`).

## Configuration (`ocm config`)

| Command | Description |
| --- | --- |
| `config view` | Print the effective configuration (`-o json` for the parsed form). |
| `config path` | Print the path to `config.yaml`. |
| `config edit` | Open `config.yaml` in `$EDITOR`, then re-validate it on save. |

## Diagnostics

| Command | Description |
| --- | --- |
| `ocm doctor` | Check the container runtime is available, the config and base image, and how many workspaces exist. |
| `ocm version` | Print the `opencode-manager` version. |
| `ocm completion <shell>` | Generate a shell completion script (bash, zsh, fish, powershell). |

## Exit status

Commands exit non-zero on failure (workspace not found, runtime unavailable, a
module/lifecycle error, an invalid `--output`, etc.), so they compose in scripts
and CI pipelines.
