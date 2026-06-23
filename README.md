# opencode-manager

`opencode-manager` is a Go TUI for managing isolated OpenCode workspaces.

It lets you run OpenCode inside a dedicated per-project container and attach to it from the host. Each workspace gets its own home directory, generated OpenCode configuration, selected tools, selected credentials, and long-lived container.

## Why

Running OpenCode directly on a developer machine can give it access to too much: environment variables, cloud accounts, Kubernetes clusters, SSH keys, tokens, and local config files.

Running OpenCode in a generic VM or container gives it access to almost nothing, but then every project needs to be manually reconfigured.

`opencode-manager` solves this by creating one isolated environment per project. Each workspace receives only the modules and credentials explicitly selected for that project.

## Status

This repository is in early implementation. The initial Go module, configuration loading, workspace manifest foundations, runtime detection, and minimal TUI shell are in place.

See [PROJECT.md](PROJECT.md) for the implementation plan.

## Planned Features

- TUI application written in Go, with a minimal CLI for automation.
- Linux and macOS support.
- Docker and Podman support.
- Global configuration for workspace root, runtime, base image, and module directories.
- One long-lived container per workspace.
- One generated image per workspace.
- Host TUI attaches to the workspace container.
- The workspace container runs a small entrypoint that starts `opencode` for the first session and `opencode -c` when sessions already exist.
- Dedicated home directory per workspace.
- Module-based configuration for packages, environment variables, config files, commands, skills, agents, and plugins.
- Module hooks implemented as Go binaries, scripts, or other executables.
- Main TUI actions to create, attach, edit, stop, delete, and update workspaces.
- CLI commands limited to `list` and `attach`.

## Concept

```text
Host machine
  opencode-manager TUI
Workspace container
  opencode
  Dedicated home directory
  /home/debian/workspace
  Selected tools
  Selected credentials
  Selected OpenCode commands, skills, agents, and plugins
```

## Workspace Model

Each workspace has:

- A name.
- A dedicated directory under the configured workspace root.
- A dedicated home directory.
- A global `opencode.json` mounted read-only at `home/.config/opencode/opencode.json`.
- Globally shared OpenCode commands, skills, agents, and plugins, mounted read-only.
- Selected module configuration.
- A generated image.
- A long-lived container.
- A long-lived attachable container that runs OpenCode interactively.

Users can clone any needed repositories inside the workspace home directory.

At the workspace root, only `workspace.yaml` and `home/` should be created. Environment values, image/package requirements, module state, and generated OpenCode paths are tracked through `workspace.yaml` and files under `home/`.

## Module Model

Modules add capabilities to a workspace. A module is a self-contained directory
with a thin declarative `module.yml` and two executables that do all the work:

```text
modules/aws/
  module.yml      # name, version, description, and the prompts to collect
  install         # executable: install packages, write files, export env vars
  uninstall       # executable: undo what install did
```

`module.yml` only declares metadata and the values to ask the user for:

```yaml
name: aws
version: 1
description: Install the AWS CLI and write an isolated profile + credentials.
prompts:
  - { name: profile, label: AWS profile name, type: string, required: true }
  - { name: region,  label: Default region, type: string, default: eu-west-3 }
  - { name: secret_key, label: Secret access key, type: secret }
```

Everything else — installing packages (the scripts run as the workspace user and
may use passwordless `sudo`), writing config files into the home directory, and
exporting environment variables — is the `install` script's job. Prompt values
are passed to the scripts as `OCM_*` environment variables (e.g. `OCM_PROFILE`).
To export an environment variable, append `export VAR=value` to `~/.env`; a
supervisor process sources it so the variable reaches the OpenCode server.

Modules are a **runtime layer**, not an image layer: adding or removing a module
on a running workspace just runs its `install`/`uninstall` inside the live
container — no image rebuild and (usually) no restart. Edit a workspace's modules
from the dashboard with `e`.

The whole module directory is bind-mounted read-only into every workspace at
`/opt/opencode-manager/modules`. Built-in modules (`git`, `ssh`, `aws`,
`github`, `kubectl`, and a `hello` example) ship with opencode-manager and are
extracted into your module directory at startup; drop your own module
subdirectories alongside them.

## Configuration

The global config file defines where workspaces are stored and which container runtime is used.

Suggested location:

```text
~/.config/opencode-manager/config.yaml
```

Example:

```yaml
workspaceRoot: /home/user/.local/share/opencode-manager
runtime: docker
useLocalOpenCodeAuth: false
logLevel: warning
baseImage:
  name: debian:stable-slim
  packages:
    - htop
    - unzip
  commands:
    - update-ca-certificates
moduleDirs:
  - /home/user/.config/opencode-manager/modules
```

`runtime` must be either `docker` or `podman`.

Set `useLocalOpenCodeAuth: true` to mount the host file
`~/.local/share/opencode/auth.json` read-write into the same path in each
workspace container. The default `false` keeps auth isolated from the host.

`logLevel` controls how much is written to the log file. It must be one of
`debug`, `info`, `warning` (default), or `error`. Logs are appended to
`~/.local/share/opencode-manager/logs/opencode-manager.log` rather than printed
to the terminal, so they never interfere with the TUI.

Generated workspace images always include `brew`, `npx`, `uvx`, `git`, `ripgrep`, and `jq`. Add project-specific extras with `baseImage.packages` and `baseImage.commands`.

`opencode-manager` builds a managed base image from the `baseImage` definition and reuses it while that definition stays unchanged. Changing `baseImage.name`, `baseImage.packages`, or `baseImage.commands` produces a new managed base image tag.

When the TUI starts, it ensures the managed base image exists and shows `Creating the base image...` while the image is being built.

### Global OpenCode Templates

OpenCode configuration is shared across all workspaces from the global config
directory:

```text
~/.config/opencode-manager/
├── AGENTS.md
├── opencode.json
├── agents/
├── commands/
├── plugins/
└── skills/
```

These entries are **mounted read-only** into every workspace container at
`/home/debian/.config/opencode/`. Editing a file on the host propagates live to
all running workspaces — no copy is made and no recreation is needed. Adding or
removing a template takes effect the next time a workspace container is
(re)created.

On startup, `opencode-manager` creates any missing entries (so the mounts always
have a source):

- `AGENTS.md` and the `agents/`, `commands/`, `plugins/`, `skills/` directories
  are created empty.
- `opencode.json`, which OpenCode requires to be non-empty, is seeded with a
  minimal valid config:

  ```json
  {
    "$schema": "https://opencode.ai/config.json"
  }
  ```

Existing files are never overwritten, so your edits are preserved. Per-project
overrides are still possible via an `opencode.json` in the workspace project
directory.

## Security Principle

A workspace starts with no implicit host access.

Every cloud credential, Kubernetes context, SSH key, token, config file, tool, command, skill, agent, or environment variable must be added by a selected module.

Secrets may be stored as environment variables or plain text files inside the workspace when a module needs them.

## Installation

Install the npm package with:

```sh
npm install -g @mickaelroger78/opencode-manager
```

The global install links the `opencode-manager` command into npm's global binary
directory, so that directory must be in your shell `PATH`.

For a project-local install, npm links the command under `node_modules/.bin`
instead of your shell `PATH`:

```sh
npm install @mickaelroger78/opencode-manager
npm exec -- opencode-manager
```

You can also run it without installing globally:

```sh
npx @mickaelroger78/opencode-manager
```

The package ships prebuilt Linux and macOS binaries for x64 and arm64. During
installation it creates the global `opencode-manager` config directory, writes a
default `config.yaml` when one does not already exist, and copies bundled
modules into the default `modules/` directory without overwriting existing
entries.

Docker or Podman must be installed separately on the host.

## Development

Run the current TUI shell with:

```sh
go run ./cmd/opencode-manager
```

Minimal CLI:

```sh
opencode-manager list
opencode-manager attach <workspace>
```

The planned stack is:

- Go
- Bubble Tea
- Bubbles
- Lip Gloss
- Docker CLI
- Podman CLI

See [PROJECT.md](PROJECT.md) for phases and architecture.
