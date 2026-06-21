# opencode-manager Project Plan

## Purpose

`opencode-manager` is a host-side Go TUI for creating and managing isolated OpenCode workspaces.

The problem it solves is excessive host access. Running OpenCode directly on a developer machine gives it access to local environment variables, configured cloud accounts, Kubernetes clusters, SSH keys, and other sensitive tools. Running OpenCode inside a generic VM or container removes that access, but forces the user to manually reconfigure every project.

`opencode-manager` provides a middle ground: one dedicated, long-lived container per workspace, configured only with the modules and credentials selected for that workspace.

## Goals

- Provide a TUI application written in Go, with a minimal CLI for automation.
- Support Linux and macOS.
- Manage multiple named OpenCode workspaces.
- Run OpenCode interactively inside an isolated per-workspace container.
- Attach the host terminal to the workspace container when entering a workspace.
- Support Docker and Podman, selected through global configuration.
- Store each workspace under a globally configured workspace root directory.
- Generate one container image per workspace from selected modules.
- Keep workspace containers long-lived until the workspace is deleted or stopped.
- Use modules to add tools, environment variables, config files, OpenCode commands, skills, agents, and plugins.
- Allow modules to include both declarative YAML and executable logic.
- Provide a main TUI page for attaching, stopping, editing, deleting, and updating workspaces.

## Non-Goals For The First Version

- No web UI.
- No broad non-interactive CLI workflow beyond `list` and `attach`.
- No multi-user server mode.
- No encrypted secret store.
- No automatic host environment passthrough.
- No forced single-repository model for workspaces.

## Security Model

The main security boundary is workspace isolation and explicit configuration.

A workspace starts with no implicit access to the host's cloud accounts, Kubernetes contexts, SSH keys, tokens, config files, or environment variables. Access is added only by selected modules.

Secrets may be written as environment variables or plain text files inside the workspace directory when a module requires it. This is acceptable for the project model because each workspace has a dedicated home directory and container. The application should still make these grants visible to the user during workspace creation and editing.

Modules are responsible for writing selected credentials and config files into the dedicated workspace home directory. For example, an SSH module can inspect host SSH configuration, let the user select one or more entries, then write only the selected keys and config entries into the workspace home directory.

## User Flow

### Main Menu

When the user runs `opencode-manager`, the TUI opens and displays known workspaces.

Available actions:

- Attach to an existing workspace.
- Create a new workspace.
- Edit a workspace configuration.
- Delete a workspace.
- Stop a workspace container.
- Update OpenCode inside a workspace image/container.

### Attaching To A Workspace

When attaching to a workspace:

- Ensure the workspace container exists.
- Start it internally if needed.
- Ensure the workspace container is running.
- Attach the host terminal to the container.

The container entrypoint runs `opencode session list`. If no session exists yet, it runs `opencode`; otherwise it runs `opencode -c` to continue the latest session.

### Creating A Workspace

The create flow asks the user to:

- Enter a workspace name.
- Select one or more modules.
- Fill in the values required by each selected module.
- Review the generated access/configuration summary.
- Confirm creation.

After confirmation, `opencode-manager` creates a new workspace directory, generates all module files, builds the workspace image, and creates the long-lived attachable container.

## Filesystem Layout

The global workspace root is configured by the user.

Example layout:

```text
<workspace-root>/
  workspaces/
    my-project/
      workspace.yaml
      home/
        .config/
          opencode/
            opencode.json
            agent/
            commands/
            plugins/
            skills/
        .ssh/
        .aws/
        .kube/
```

Only `workspace.yaml` and `home/` should exist at the workspace root. Environment values, image/package requirements, and module state are declared in `workspace.yaml`. OpenCode files live inside the dedicated home directory under `home/.config/opencode/`.

The dedicated workspace home directory is mounted as the container user's home directory. Users can clone any repositories they need inside this home directory.

## Global Configuration

The global configuration file controls host-level behavior.

Suggested default location:

```text
~/.config/opencode-manager/config.yaml
```

Example:

```yaml
workspaceRoot: /home/user/.local/share/opencode-manager
runtime: docker
useLocalOpenCodeAuth: false
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

The runtime must be either `docker` or `podman`.

When `useLocalOpenCodeAuth` is `true`, the host file
`~/.local/share/opencode/auth.json` is mounted read-write into the same path in
workspace containers. It defaults to `false`, so host OpenCode auth is not shared
unless explicitly enabled.

Generated workspace images always include `brew`, `npx`, `uvx`, `git`, `ripgrep`, and `jq`. Additional Debian packages are declared through `baseImage.packages`, and additional build steps are declared through `baseImage.commands`.

The managed base image is tagged from a stable hash of the base image definition and is reused while that definition stays unchanged. Workspace-specific images are built from the managed base image and add only workspace-specific setup such as the matching UID/GID user.

The TUI ensures the managed base image exists at startup and displays `Creating the base image...` while building it.

### OpenCode Preconfiguration

New workspaces seed their OpenCode configuration from:

```text
~/.config/opencode-manager/opencode/
```

Only these entries are copied into `home/.config/opencode/`:

- `opencode.json`
- `agent/`
- `commands/`
- `plugins/`
- `skills/`

Missing entries are ignored, and `opencode.json` defaults to `{}` when no preconfigured file exists.

## Workspace Manifest

Each workspace has a manifest that records generated state.

Example:

```yaml
name: my-project
runtime: docker
imageName: opencode-manager/my-project:latest
image:
  baseImage: debian:stable-slim
  packages:
    - htop
    - unzip
  commands:
    - update-ca-certificates
containerName: opencode-manager-my-project
homeDir: /configured-root/workspaces/my-project/home
env: {}
modules:
  - name: base
    version: 1
  - name: ssh
    version: 1
  - name: kubernetes
    version: 1
createdAt: 2026-06-20T00:00:00Z
updatedAt: 2026-06-20T00:00:00Z
```

## Module System

Modules are the core extension mechanism. They configure both the container environment and OpenCode behavior.

A module may provide:

- Debian packages for the workspace image.
- Environment variables.
- Files written into the workspace home directory.
- OpenCode config fragments.
- OpenCode commands.
- OpenCode skills.
- OpenCode agents and plugins.
- User prompts for required values.
- Executable hooks for dynamic discovery, validation, generation, and updates.

### Module Layout

Example:

```text
modules/ssh/
  module.yaml
  hooks/
    discover
    validate
    apply
    update
  templates/
    ssh_config.tmpl
  opencode/
    agent/
    commands/
    plugins/
    skills/
```

### Declarative Module File

`module.yaml` is used for static data such as packages, prompts, templates, and OpenCode additions.

Example:

```yaml
name: aws
version: 1
description: Configure isolated AWS CLI access.
packages:
  apt:
    - awscli
prompts:
  - name: profile
    label: AWS profile name
    type: string
  - name: region
    label: Default AWS region
    type: string
env:
  AWS_CONFIG_FILE: /home/opencode/.aws/config
  AWS_SHARED_CREDENTIALS_FILE: /home/opencode/.aws/credentials
files:
  - template: templates/config.tmpl
    destination: home/.aws/config
opencode:
  commands:
    - opencode/commands/aws.md
```

### Executable Hooks

Hooks may be Go binaries, shell scripts, Python scripts, or any executable available on the host.

Hooks communicate with `opencode-manager` through JSON on stdin/stdout. This keeps modules language-agnostic.

Initial hook types:

- `discover`: inspect the host and return selectable values, such as SSH entries or Kubernetes contexts.
- `validate`: validate user-provided values before applying a module.
- `apply`: write generated files into the workspace directory.
- `update`: update module-generated assets for an existing workspace.

The main application is responsible for passing only the workspace path, module configuration, and relevant global settings to hooks.

## Container Runtime

The runtime layer abstracts Docker and Podman.

Required operations:

- Check runtime availability.
- Build a workspace image.
- Create a container.
- Start a container internally when attach needs it.
- Stop a container.
- Delete a container.
- Inspect container state.
- Execute commands in a running container.
- Stream logs when needed.

The first implementation should use runtime CLIs (`docker` and `podman`) instead of daemon APIs. This keeps the implementation simple and works well on Linux and macOS.

## Image Strategy

Each workspace has one generated image built from a managed base image.

The managed base image is derived from the globally configured Debian base image. Selected modules contribute packages and setup steps. Rebuilding the managed base image is required when package-level module configuration changes.

OpenCode update should be exposed as a first-class TUI action. The implementation can start simple by executing the configured OpenCode install/update command inside the workspace container, then later support rebuilding images with a newer OpenCode version.

## Attach Management

Attaching to a workspace connects the host terminal to the long-lived workspace container. The container is created with an interactive TTY and runs a small OpenCode entrypoint in `/home/debian/workspace`.

The process CLI intentionally exposes only two commands: `list` and `attach <workspace>`.

## Suggested Go Package Layout

```text
cmd/opencode-manager/
internal/config/
internal/tui/
internal/workspace/
internal/module/
internal/runtime/
internal/opencode/
internal/files/
modules/
```

Responsibilities:

- `cmd/opencode-manager`: application entrypoint.
- `internal/config`: global config loading and validation.
- `internal/tui`: Bubble Tea views, forms, menus, and actions.
- `internal/workspace`: workspace registry, manifests, lifecycle orchestration.
- `internal/module`: module loading, prompts, hooks, application, and validation.
- `internal/runtime`: Docker and Podman drivers.
- `internal/opencode`: OpenCode config generation and attach logic.
- `internal/files`: safe filesystem helpers.
- `modules`: built-in modules shipped with the application.

## Recommended TUI Library

Use the Charm ecosystem:

- Bubble Tea for the TUI runtime.
- Bubbles for common components.
- Lip Gloss for styling.

## Initial Built-In Modules

Recommended first modules:

- `base`: common Debian packages such as `git`, `curl`, `ca-certificates`, `openssh-client`, and shell utilities.
- `opencode`: install and configure OpenCode inside the image.
- `ssh`: select host SSH entries and write selected keys/config into the workspace home directory.
- `git`: configure git identity inside the workspace.
- `aws`: write isolated AWS config and credentials.
- `kubernetes`: select Kubernetes contexts and write an isolated kubeconfig.
- `github`: configure GitHub CLI or token-based access.

## Implementation Phases

## Implementation Progress

Status as of 2026-06-20:

- Completed initial Go module setup.
- Added dependencies for Bubble Tea, Lip Gloss, and YAML encoding.
- Added global config loading with defaults and validation.
- Added workspace manifest types and YAML persistence helpers.
- Added workspace registry listing and manifest scaffolding.
- Added Docker/Podman runtime CLI availability abstraction.
- Added a minimal TUI shell that lists workspaces, checks runtime health, refreshes with `r`, and exits with `q`.
- Reworked the TUI shell into a simpler layout with one main workspace list box, top-line command shortcuts, a bottom slash-command bar, command autocomplete, and delete confirmation.
- Added one-key shortcuts for every slash command.
- Implemented the first `/create` flow: prompt for a workspace name, create `workspace.yaml`, create `home/`, write initial `home/.config/opencode/opencode.json`, and refresh the workspace list.
- Reworked delete confirmation into a centered destructive-action popup in the TUI.
- Reworked create prompt into a centered simple popup matching the delete confirmation style.
- Added Docker/Podman lifecycle operations for image build, container create, start, stop, and status inspection.
- Container creation runs with the current host UID/GID, and the generated image creates a matching container user/home setup.
- Wired TUI workspace status display, attach, stop, and delete actions.
- Workspace creation now provisions the runtime side too: after writing `workspace.yaml`, it builds the image and creates the container.
- Implemented delete: confirmation now removes the container, removes the workspace image, and deletes the workspace directory.
- Generated images install OpenCode from npm (`npm install -g opencode-ai`) and verify `opencode --version` during build. (The previous `curl -fsSL https://opencode.ai/install | bash` installer was dropped because it resolves the version via the GitHub API, which is rate-limited and intermittently returns 504s, breaking image builds; the npm registry is reliable and already used for `tokscale`.)
- Attach recovers from old/broken containers by recreating the container from the rebuilt image if the first internal start attempt fails.
- Attach only starts/provisions when the selected container is not already running; otherwise it attaches to the running container.
- Containers run an OpenCode entrypoint in `/home/debian/workspace`; it starts `opencode` when no sessions exist and `opencode -c` otherwise.
- Added initial config, workspace, and runtime tests.
- Added validation for invalid workspace slugs and malformed manifests.
- Reworked the TUI to follow the k9s look-and-feel and key bindings: a three-zone header (context info, keyboard menu, logo), a titled workspace table with a highlighted cursor row, a breadcrumb line, and the k9s default ("stock") skin colors.
- Adopted k9s key bindings: `:` enters command mode, `/` filters the workspace list, `?` toggles help, `Enter` (or `:attach`) attaches, `s` opens a shell in the container, `t` starts or stops the container based on its current status, `d` describes the selected workspace, `e` edit, `u` update, `c` create, `ctrl-d` delete (guarded), `j`/`k`/`g`/`G` and `ctrl-f`/`ctrl-b` navigate, and `q`/`ctrl-c` quit. Command words are typed without a leading slash (for example `:attach`, `:shell`, `:create my-app`, `:q`).
- Added a runtime `ExecCommand` and a workspace `Shell` action that ensures the container is running, then opens an interactive `/bin/bash` inside it via `docker/podman exec -it`.
- Restyled the confirmation/create/help popups as k9s dialogs: the title is embedded in the top border, with `OK`/`Cancel` buttons that are focusable (Tab/←/→ cycles, Enter activates the focused button, Esc cancels) and use the k9s dialog focus colors.
- Made describe a full page (like k9s pushes a describe view) instead of a popup: pressing `d` replaces the workspace table with a `Describe(name)` panel and adds a `describe` breadcrumb; Esc or `q` returns to the list.
- Installed `tokscale` in the managed base image and added an OpenCode token-usage synthesis to the describe page: when the container is running, opening describe runs `tokscale --json --client opencode` (and `--today`) inside it via a non-TTY `ExecOutput`, then shows total and daily tokens, cost, and message counts. Usage is fetched asynchronously and cached per workspace; stopped containers show a hint instead.
- Added a live per-workspace OpenCode activity status to the central dashboard. A manager-owned OpenCode plugin (`opencode-manager-status.js`, embedded and seeded into the global `plugins/` template directory, mounted read-only into every workspace) listens to the OpenCode event bus and writes an aggregate status file to `home/.local/state/opencode-manager/status.json`. Because the workspace home is bind-mounted, the manager reads that file directly from the host with no exec or network call. The plugin maps events to states (`message`/`tool` → working, `permission.asked` → needs-approval, `session.idle` → idle, `session.error` → error) and heartbeats every 10s so a stale file means OpenCode is no longer running. The TUI now polls statuses every 2s on a ticker, shows an `ACTIVITY` column (unused / working / waiting / approval / error / asleep), adds the activity to the describe page, surfaces an `Attention` summary in the header (`N need approval, M waiting`), and rings the terminal bell when a workspace newly transitions into the approval state. A workspace that has never been used shows `unused`: the status file is created the first time OpenCode boots in a workspace and persists afterwards, so its absence (with the container stopped) is a reliable "never used" signal read from the host.
- Made detaching from a workspace non-destructive and robust. Previously the container's main process *was* OpenCode (attached via `docker attach`), so Ctrl-C reached OpenCode and exiting it stopped the container, and the shared-TTY attach left the terminal in a flaky state across runs ("read escape sequence", stalled relaunches). This is solved with a **client/server split** using OpenCode's native `serve`/`attach` commands. The container's main process is a persistent headless server (`opencode serve --hostname 127.0.0.1 --port 4096`); attaching runs a fresh TUI **client** against it (`docker exec -it … /usr/local/bin/opencode-manager-attach`, which calls `opencode attach http://127.0.0.1:4096 --dir /home/debian/workspace [-c]`). Because the client is a separate process from the server, **Ctrl-C exits only the client** and the server keeps running in the background — no multiplexer, no key remapping, and the client draws directly to the real terminal so glyphs/UTF-8 render natively (earlier `tmux`/`screen`/`dtach` approaches each broke on nesting, redraw, or glyph mangling). The status-reporter plugin loads in the **server**, so the activity dashboard reflects work even with no client attached, and re-attaching reconnects to the same live session. Each workspace container is network-isolated, so binding `127.0.0.1:4096` is private to it (no host port published). The attach wrapper waits for the server to be listening, then asks it over HTTP whether a session exists to decide `--continue` vs a fresh session. OpenCode is installed from npm (`opencode-ai`). Because the container command and base image changed, the base image revision is bumped (forcing a one-time rebuild) and a container created from an outdated image is automatically recreated on next start (detected by comparing the container's image ID to the workspace image's current ID).

Current limitations:

- Module loading and hooks are not implemented yet.
- OpenCode client/server mode is intentionally not used.
- Created containers are not started automatically by `create`; attach with `a` or `:attach` when ready to enter the workspace.
- Module selection is not part of the create flow yet.

Security review notes before implementing container lifecycle actions:

- Tighten manifest validation for runtime, image names, container names, home paths, and terminal control characters.
- Sanitize or visibly escape user-controlled strings before rendering them in the TUI.
- Normalize and validate `workspaceRoot` before writing workspace data.
- Keep runtime and OpenCode command execution structured with `exec.CommandContext` arguments, not shell strings.
- Treat module values as potentially sensitive because workspace manifests are plaintext.

### Phase 1: Foundation

- [x] Initialize Go module.
- [x] Add config loading.
- [x] Add workspace registry and manifest types.
- [x] Add Docker and Podman runtime interfaces.
- [x] Add basic TUI shell with workspace list.
- [x] Add main workspace box with top-line shortcuts.
- [x] Add bottom slash-command bar with autocomplete.
- [x] Add one-key shortcuts for each slash command.
- [x] Add delete confirmation modal placeholder.

### Phase 2: Workspace Lifecycle

- [x] Create workspace directories.
- [x] Generate initial `home/.config/opencode/opencode.json`.
- [x] Build a workspace image.
- [x] Create, start, stop, and delete containers.
- [x] Attach host terminal to the workspace container.

### Phase 3: Module System

- [ ] Load declarative `module.yaml` files.
- [ ] Render module templates.
- [ ] Execute module hooks with JSON input/output.
- [ ] Add module prompts to the TUI creation flow.
- [ ] Store selected module values in workspace state.

### Phase 4: Built-In Modules

- [ ] Implement `base`.
- [ ] Implement `opencode`.
- [ ] Implement `git`.
- [ ] Implement `ssh`.
- [ ] Implement one cloud module, preferably `aws`.
- [ ] Implement `kubernetes`.

### Phase 5: Update And Edit Flows

- [ ] Edit workspace module values.
- [ ] Re-run module application.
- [ ] Rebuild workspace image when needed.
- [ ] Update OpenCode inside an existing workspace.
- [x] Show workspace status and runtime health in the main TUI.

## Open Questions

- The first version needs to decide whether generated config files are overwritten automatically or whether modules must support merge behavior.
- The first version needs a clear failure recovery story for partially created workspaces.
