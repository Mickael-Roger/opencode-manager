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
- A generated or preconfigured `home/.config/opencode/opencode.json`.
- Generated or preconfigured OpenCode commands, skills, agents, and plugins.
- Selected module configuration.
- A generated image.
- A long-lived container.
- A long-lived attachable container that runs OpenCode interactively.

Users can clone any needed repositories inside the workspace home directory.

At the workspace root, only `workspace.yaml` and `home/` should be created. Environment values, image/package requirements, module state, and generated OpenCode paths are tracked through `workspace.yaml` and files under `home/`.

## Module Model

Modules are responsible for adding capabilities to a workspace.

A module can provide:

- Debian packages for the workspace image.
- Environment variables.
- Files written into the workspace home directory.
- OpenCode config fragments.
- OpenCode commands.
- OpenCode skills.
- OpenCode agents and plugins.
- TUI prompts for required values.
- Executable hooks for discovery, validation, generation, and updates.

Example modules include `ssh`, `git`, `aws`, `kubernetes`, `github`, and `opencode`.

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

Generated workspace images always include `brew`, `npx`, `uvx`, `git`, `ripgrep`, and `jq`. Add project-specific extras with `baseImage.packages` and `baseImage.commands`.

`opencode-manager` builds a managed base image from the `baseImage` definition and reuses it while that definition stays unchanged. Changing `baseImage.name`, `baseImage.packages`, or `baseImage.commands` produces a new managed base image tag.

When the TUI starts, it ensures the managed base image exists and shows `Creating the base image...` while the image is being built.

### OpenCode Preconfiguration

New workspaces copy preconfigured OpenCode files from:

```text
~/.config/opencode-manager/opencode/
```

Supported entries are copied into the workspace at `home/.config/opencode/`:

- `opencode.json`
- `agent/`
- `commands/`
- `plugins/`
- `skills/`

Missing entries are ignored. If `opencode.json` is absent, the workspace starts with a default `{}` file.

## Security Principle

A workspace starts with no implicit host access.

Every cloud credential, Kubernetes context, SSH key, token, config file, tool, command, skill, agent, or environment variable must be added by a selected module.

Secrets may be stored as environment variables or plain text files inside the workspace when a module needs them.

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
