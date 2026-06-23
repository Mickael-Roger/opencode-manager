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
- A module.yml may declare `key: <prompt>` to become multi-instance: it can be installed several times per workspace (e.g. one `ssh` entry per host, one `aws` entry per profile), each distinguished by that prompt's value. The instance identity is `name:keyvalue` (`ModuleInstance.InstanceID`); the key prompt must be required and non-secret. Without `key` a module is a singleton and a second install replaces the first. The install/uninstall scripts must be instance-scoped by the key (e.g. ssh writes `~/.ssh/config.d/$OCM_HOST.conf`) so multiple entries coexist and each uninstall removes only its own.
- Workspace containers are long-lived and tied to the workspace lifecycle.
- New workspaces seed OpenCode files from `~/.config/opencode-manager/opencode/` into `home/.config/opencode/` for `opencode.json`, `agent/`, `commands/`, `plugins/`, and `skills/`.
- `config.yaml` defines `baseImage.name`, `baseImage.packages`, and `baseImage.commands`; commands run during image build immediately after package installation.
- `config.yaml` supports `useLocalOpenCodeAuth` (default `false`); when `true`, `~/.local/share/opencode/auth.json` is mounted read-write into the same path in workspace containers.
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

## Packaging Notes

- npm packaging is defined by `package.json`, `bin/opencode-manager`, and `scripts/postinstall.js`.
- The npm package name is `@mickaelroger78/opencode-manager`.
- The npm package expects prebuilt binaries in `dist/opencode-manager-{linux,darwin}-{x64,arm64}`.
- `.github/workflows/package.yml` builds those binaries, uploads them as artifacts, packages the npm tarball, and publishes on `v*` tags using npm trusted publishing.
- Built-in module sources live in the repo's top-level `modules/` directory (pure data, no Go files) and are currently limited to `aws`, `git`, and `ssh`.
- The npm postinstall script creates the same user config directory used by Go's `os.UserConfigDir`, writes `config.yaml` only when absent, and syncs module directories from `modules/` into the user config, overwriting built-in modules so updates take effect while leaving user-authored modules untouched.
- Built-in modules are **not** embedded in the Go binary; they are installed and updated exclusively through the npm package.
