# Configuration

`opencode-manager` is configured by a single global file:

```text
~/.config/opencode-manager/config.yaml
```

A working default is written on install, so `ocm` runs out of the box. This page
documents every option.

## Full example

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

## Options

### `workspaceRoot`

Directory under which every workspace is stored. Each workspace gets its own
subdirectory containing `workspace.yaml` and `home/`.

### `runtime`

Container runtime. Must be either `docker` or `podman`. Set it to match what you
have installed.

### `useLocalOpenCodeAuth`

When `true`, the host file `~/.local/share/opencode/auth.json` is mounted
**read-write** into the same path in every workspace container, so workspaces
share your host OpenCode login. Default `false` keeps auth isolated from the host
— in keeping with the [security principle](concepts.md#security-principle).

### `hostNetwork`

When `true`, each workspace container shares the **host's network namespace**
(`docker`/`podman --network host`) instead of getting an isolated one. Default
`false`.

Enable it when the agent (or its tools) needs to reach services listening on the
host's loopback — a local model server, a database, a dev server, etc. With an
isolated network those `127.0.0.1` services are unreachable from inside the
container.

Inside every container an OpenCode server runs on the loopback interface. With
isolated networking they could all share one port, but under host networking they
share the host's loopback, so a fixed port would collide. To avoid that, each
workspace is assigned a **unique loopback port** (range `4096–4999`), recorded as
`openCodePort` in its `workspace.yaml` and reused across restarts. Existing
workspaces created before this option are assigned a port automatically on their
next start. The assignment happens regardless of `hostNetwork`, so toggling the
option never requires a migration.

> **Note:** host networking is opt-in because it weakens isolation — the
> container can reach (and bind) anything on the host's network interfaces. It
> also has limited support on Docker Desktop (macOS/Windows); on Linux with
> docker/podman it works as expected.

### `runtimeArgs`

Optional list of extra flags passed **verbatim** to the `docker`/`podman create`
command used to create each workspace container. They are inserted in the options
section, just before the image name, so they can extend (or override) anything the
manager sets itself. This is an escape hatch for runtime options the manager does
not model natively — no code change required. For example:

```yaml
runtimeArgs:
  - --dns
  - 1.1.1.1
  - --add-host=db:10.0.0.5
  - --device=/dev/fuse
```

Each entry is one argument token, exactly as you would type it on the command
line: a flag and its value are **separate list items** (`- --dns` then
`- 1.1.1.1`), or combined with `=` in a single item (`- --add-host=db:10.0.0.5`).
The list is applied to every workspace; an empty entry is rejected.

> **Note:** values are passed through untouched and are not validated by the
> manager — a flag your runtime rejects will surface as a container-create error.

### `logLevel`

How much is written to the log file. One of `debug`, `info`, `warning`
(default), or `error`.

#### Logging

Logs are **appended to a file**, not printed to the terminal, so they never
interfere with the TUI:

```text
~/.local/share/opencode-manager/logs/opencode-manager.log
```

### `moduleDirs`

List of directories scanned for [modules](modules.md). The built-in modules are
installed into the first one on package install; add your own module directories
here to extend the catalogue.

## Base image

The `baseImage` block controls the container image workspaces are built from.

```yaml
baseImage:
  name: docker.io/mroger78/ocm-base:latest
  packages:
    - htop
    - unzip
  commands:
    - update-ca-certificates
```

### `baseImage.name`

The base image to use. Defaults to the published, prebuilt
`docker.io/mroger78/ocm-base:latest`, which already contains the full tooling
(`npx`, `uvx`, `git`, `ripgrep`, `jq`, `opencode`, `tokscale`, and the manager
scripts). With this default and no extras, `ocm` simply **pulls** that image
instead of building one, so the first start is fast.

### `baseImage.packages` / `baseImage.commands`

Project-wide extras layered on top of the base.

- Adding `packages` or `commands` builds a thin **local overlay** on top of the
  prebuilt base — only the extras are applied.
- `commands` run during the image build immediately after the packages are
  installed (e.g. `update-ca-certificates`).

Pointing `baseImage.name` at a different distro instead (e.g.
`debian:stable-slim`) falls back to building the **complete** base recipe locally
from that image.

### Always included

Generated workspace images always include `npx`, `uvx`, `git`, `ripgrep`, and
`jq`, regardless of configuration. Use `packages`/`commands` only for additional
tools.

### How images are reused

A managed base image is tagged from a stable hash of the `baseImage` definition
and **reused** until that definition changes; changing any field produces a new
managed base image. The published image and the local-build fallback share the
same recipe (under `internal/runtime/buildcontext/`), so they never drift.

When the TUI starts it ensures the base image is available, showing
`Creating the base image...` while it is pulled or built.

## Related

- [Concepts → Shared OpenCode config](concepts.md#shared-opencode-config) — the
  read-only OpenCode templates shared into every workspace.
- [Troubleshooting](troubleshooting.md) — when the base image won't build or the
  runtime can't be found.
