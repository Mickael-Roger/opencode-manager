# Project-Specific Agent Guidelines

## Overview

- Purpose: Build `opencode-manager`, a Go TUI for managing isolated OpenCode workspaces backed by per-workspace containers.
- Primary docs: `PROJECT.md` and `README.md`.
- Target platforms: Linux and macOS.

## Rules

- Keep project documentation in English.
- Prefer small, explicit implementation steps that match `PROJECT.md` phases.
- Preserve the TUI-only product direction unless requirements change.
- Treat Docker and Podman as first-class runtime targets.
- Do not assume implicit access to host credentials, environment variables, Kubernetes contexts, SSH keys, or cloud accounts.
- Model workspace access through explicit modules.

## Architecture Notes

- The host runs `opencode-manager` and attaches to the workspace container.
- Each workspace container runs `/usr/local/bin/opencode-manager-entrypoint`, which executes `opencode session list` and chooses `opencode` for the first session or `opencode -c` when sessions already exist.
- The current implementation does not use OpenCode client/server mode.
- Modules combine declarative YAML with optional executable hooks.
- Workspace containers are long-lived and tied to the workspace lifecycle.
- New workspaces seed OpenCode files from `~/.config/opencode-manager/opencode/` into `home/.config/opencode/` for `opencode.json`, `agent/`, `commands/`, `plugins/`, and `skills/`.
- `config.yaml` defines `baseImage.name`, `baseImage.packages`, and `baseImage.commands`; commands run during image build immediately after package installation.
- Generated workspace images must include `brew`, `npx`, `uvx`, `git`, `ripgrep`, and `jq` by default.
- Managed base images are tagged from a stable hash of `baseImage` definition and reused until that definition changes.
- The TUI ensures the managed base image exists at startup and shows `Creating the base image...` while it is built.

## Implementation Notes

- Go module path: `github.com/mickael-menu/opencode-manager`.
- TUI stack: Bubble Tea and Lip Gloss.
- YAML library: `go.yaml.in/yaml/v4`.
- Runtime execution must use structured `exec.CommandContext` calls, not shell command strings.
- Run `gofmt -w cmd internal` and `go test ./...` after Go changes.
- Do not run code review or security-review subagents unless the user explicitly requests them.
