# opencode-manager — Architecture & Details

This document covers the design and configuration details of
`opencode-manager`. For a quick start, see [README.md](README.md).

## Status

This repository is in early implementation. The initial Go module, configuration
loading, workspace manifest foundations, runtime detection, and minimal TUI shell
are in place.

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
- Reusable workspace templates (a named module set) applied when creating a workspace.
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

At the workspace root, only `workspace.yaml` and `home/` should be created.
Environment values, image/package requirements, module state, and generated
OpenCode paths are tracked through `workspace.yaml` and files under `home/`.

## Module Model

Modules add capabilities to a workspace. A module is a self-contained directory
with a thin declarative `module.yml` and two executables that do all the work.
Modules live under a **category** directory (e.g. `cloud`, `infra`, `tools`) that
groups them in the editor:

```text
modules/cloud/aws/
  module.yml      # name, version, description, and the prompts to collect
  install         # executable: install packages, write files, export env vars
  uninstall       # executable: undo what install did
```

The category is taken from the parent directory name; it is purely organizational
(the module is still identified by its globally unique `name`). To add a module,
drop its directory under the relevant category (creating a new category directory
is fine).

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

Multi-instance modules (`aws`, `outscale`, `ssh`) can list the accounts already
configured on your host and import one or many in a single step. When you add an
entry, the editor shows the host accounts it found — AWS/Outscale profiles, SSH
host aliases — to import as separate instances, plus an **Add manually…** option
that opens a one-page form with every field (name, keys, region…) for an account
that is not on the host. Imported instances store only the account name in the
workspace manifest; their secrets are pulled from the host at install time and
never written to the manifest. This is wired with two host-side hooks: an
`optionsCommand` on the module's `key` prompt that lists importable accounts, and
a `resolve` script that reads the selected account's credentials from the host.

The whole module directory is bind-mounted read-only into every workspace at
`/opt/opencode-manager/modules`, mirroring the `category/module` layout (so the
`aws` module runs from `/opt/opencode-manager/modules/cloud/aws`). Built-in
modules ship in the top-level `modules/` directory grouped by category —
`cloud/aws`, `cloud/outscale`, `infra/kubernetes`, `tools/git`, `tools/ssh` — and
are installed into your module directory by the npm postinstall script; drop your
own module directories under a category alongside them.

In the editor (`e`), modules are shown as a category browser — a category header
with its modules indented beneath it — and `/` filters by name, description, or
category.

## Templates

A **template** is a reusable, named set of modules-with-values — the module
recipe for a kind of project, without any workspace-specific state (no container,
image, or home). Templates are managed on a dedicated page reached with
`:templates` (and `:workspaces` to return), which reuses the same module editor as
workspaces: creating or editing a template selects modules and collects their
prompt values exactly as for a live workspace, but applying it saves the recipe
instead of installing into a container.

Each template is stored as a single YAML file at
`<workspaceRoot>/templates/<slug>.yaml`, where `<slug>` is the name run through
the same `SafeName` slugging used for workspace directories. The file holds the
template name and a list of module instances identical in shape to a workspace
manifest's `modules:` entries (name, id, category, version, values).

When creating a workspace, the New Workspace name dialog is followed by an
optional **Pick Template** step (skipped when no templates exist). Choosing a
template copies its module instances into the new workspace's `workspace.yaml`
*before* the workspace is provisioned; the lifecycle's existing reconcile step
then installs those modules on first start — the same path that converges a freshly
recreated container to its manifest — so no separate install logic is needed.

## Configuration

The global config file defines where workspaces are stored and which container
runtime is used.

Suggested location:

```text
~/.config/opencode-manager/config.yaml
```

Example:

```yaml
workspaceRoot: /home/user/.local/share/opencode-manager
runtime: docker
useLocalOpenCodeAuth: false
hostNetwork: false
runtimeArgs:
  - --dns
  - 1.1.1.1
logLevel: warning
baseImage:
  name: docker.io/mroger78/ocm-base:latest
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

Set `hostNetwork: true` to run each container in the host's network namespace
(`--network host`) instead of an isolated one, so the agent and its tools can
reach services on the host loopback. The default `false` keeps each container
network-isolated. Because every container runs an OpenCode server on the loopback
interface, each workspace is assigned a unique loopback port (range `4096–4999`,
recorded as `openCodePort` in `workspace.yaml`) so the servers never collide when
they share the host loopback. The entrypoint and attach client read this port
from the `OCM_OPENCODE_PORT` environment variable.

`runtimeArgs` is an optional list of extra flags passed verbatim to the
`docker`/`podman create` command for every workspace container, inserted just
before the image name (`ContainerSpec.ExtraArgs`, appended last in the options
section of `createArgs`). It is an escape hatch for runtime options the manager
does not model natively (e.g. `--dns`, `--add-host`, `--device`, extra
`--volume`s); values are not validated, so a flag the runtime rejects surfaces as
a container-create error.

`logLevel` controls how much is written to the log file. It must be one of
`debug`, `info`, `warning` (default), or `error`. Logs are appended to
`~/.local/share/opencode-manager/logs/opencode-manager.log` rather than printed
to the terminal, so they never interfere with the TUI.

Generated workspace images always include `npx`, `uvx`, `git`,
`ripgrep`, and `jq`. Add project-specific extras with `baseImage.packages` and
`baseImage.commands`.

`baseImage.name` defaults to the published, prebuilt base image
`docker.io/mroger78/ocm-base:latest`, which already contains the full tooling
(`npx`, `uvx`, `git`, `ripgrep`, `jq`, `opencode`, `tokscale`, and the
manager scripts). With this default and no extras, `opencode-manager` simply
pulls that image instead of building a base locally, so the first start is fast.

Adding `baseImage.packages` or `baseImage.commands` builds a thin local overlay
on top of the prebuilt base (only the extras are applied). Pointing
`baseImage.name` at a different distro instead (e.g. `debian:stable-slim`) falls
back to building the complete base recipe locally from that image. In all cases
the resulting base is reused while the `baseImage` definition stays unchanged;
changing any field produces a new managed base image.

The base recipe lives as real files under `internal/runtime/buildcontext/`
(`Dockerfile`, `Dockerfile.overlay`, `Dockerfile.workspace`, and the manager
scripts). The GitHub Actions pipeline publishes `buildcontext/Dockerfile`
directly, and the binary embeds the same directory and builds it locally as a
fallback (passing the base image, extra packages, and commands as `--build-arg`
values), so the published image and the local build never drift.

When the TUI starts, it ensures the base image is available and shows
`Creating the base image...` while it is pulled or built.

## Global OpenCode Templates

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

Every cloud credential, Kubernetes context, SSH key, token, config file, tool,
command, skill, agent, or environment variable must be added by a selected
module.

Secrets may be stored as environment variables or plain text files inside the
workspace when a module needs them.

## Development

Run the current TUI shell with:

```sh
go run ./cmd/opencode-manager
```

The planned stack is:

- Go
- Bubble Tea
- Bubbles
- Lip Gloss
- Docker CLI
- Podman CLI

See [PROJECT.md](PROJECT.md) for phases and architecture.
