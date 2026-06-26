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
- Module hooks run in two places. `install`/`uninstall` run **inside** the workspace container as the workspace user (sandboxed). Two optional hooks run **on the host** as part of an explicit user action: a prompt's `optionsCommand` populates a `select`/`multiselect` prompt's choices from host state (one option per stdout line, e.g. the `kubernetes` module lists host kube contexts), and a module-level `resolve` script (present == enabled) runs before the container install with the collected values as `OCM_*` env vars and prints extra `key=value` lines that are merged into the install env but **not** persisted to the manifest. `resolve` is how a module derives container input from host-only state it must not store (e.g. extracting the selected kubeconfig from `~/.kube/config`); it is re-run on every add and reconcile. Host hooks have full host access, unlike the sandboxed install/uninstall.
- A module.yml may declare `key: <prompt>` to become multi-instance: it can be installed several times per workspace (e.g. one `ssh` entry per host, one `aws` entry per profile), each distinguished by that prompt's value. The instance identity is `name:keyvalue` (`ModuleInstance.InstanceID`); the key prompt must be required and non-secret. Without `key` a module is a singleton and a second install replaces the first. The install/uninstall scripts must be instance-scoped by the key (e.g. ssh writes `~/.ssh/config.d/$OCM_HOST.conf`) so multiple entries coexist and each uninstall removes only its own.
- The `key` prompt of a multi-instance module may carry an `optionsCommand` (the only non-select prompt allowed to). It runs on the host to list accounts importable as new instances (e.g. `aws`/`outscale` list profiles, `ssh` lists `~/.ssh/config` host aliases). In the editor's add flow this drives an import picker: selecting one or more host accounts creates one instance each storing only the key value, with the actual credentials pulled by the module's `resolve` hook at install time (so secrets never reach the manifest); the picker also offers an "Add manually…" option that opens a single-page form (all fields at once) for an account not present on the host, whose typed values are stored as usual. The key prompt stays a free-text `string` so manual entry still works; `resolve` must emit nothing for an account it does not find on the host, leaving the manually-typed values in place.
- A module.yml may declare `restartServer: false` to mark itself environment-neutral: it writes only its own config files (e.g. `~/.kube/config`, `~/.aws/credentials`) that tools read live, never `~/.env`. Such a module is never bounced (the manager skips the server restart even if `~/.env` somehow changed — and logs a warning, since that means the manifest lies) and, crucially, its add/remove is allowed from the editor **while a task is running**, because nothing interrupts the in-flight session. Omitting the key (or setting `restartServer: true`) keeps the conservative default: the module is assumed to touch `~/.env`, so the manager bounces the server when `~/.env` changes and the editor blocks the edit until the workspace is idle. All current built-ins (`aws`, `git`, `kubernetes`, `outscale`, `ssh`) are `restartServer: false`.
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
- Built-in module sources live in the repo's top-level `modules/` directory (pure data, no Go files) and are currently `aws`, `git`, `kubernetes`, `outscale`, and `ssh`.
- The npm postinstall script creates the same user config directory used by Go's `os.UserConfigDir`, writes `config.yaml` only when absent, and syncs module directories from `modules/` into the user config, overwriting built-in modules so updates take effect while leaving user-authored modules untouched.
- Built-in modules are **not** embedded in the Go binary; they are installed and updated exclusively through the npm package.
