# Modules

Modules add capabilities to a workspace — tools, credentials, config files,
environment variables. A workspace starts empty; whatever it can reach, a module
put there. Add and remove them from the dashboard with `e` (see the
[TUI Guide](tui.md#edit-modules-e)).

For the model behind modules, see [Concepts → Modules](concepts.md#modules). To
build your own, see [Writing Modules](writing-modules.md).

## Built-in modules

Built-ins ship in the package and are installed into your module directory on
install, grouped by category.

### cloud

| Module | What it does | Multi-instance |
| --- | --- | --- |
| **aws** | Installs the AWS CLI and writes an isolated named profile + credentials to `~/.aws`. | Yes (per profile) |
| **gcp** | Installs the Google Cloud CLI (`gcloud`) and writes a named configuration (project, region, optional service account key) to `~/.config/gcloud`. | Yes (per configuration) |
| **outscale** | Installs `octl` (the Outscale CLI) and writes a named AK/SK profile to `~/.osc/config.json`. | Yes (per profile) |
| **ovh** | Installs the OVHcloud CLI (`ovhcloud`) and writes a named endpoint profile with API credentials to `~/.ovh.conf`. | Yes (per endpoint) |
| **scaleway** | Installs the Scaleway CLI (`scw`) and writes a named profile to `~/.config/scw/config.yaml`. | Yes (per profile) |

All can list profiles already configured on your host and import them; secrets
are pulled from the host at install time and never stored in the manifest. The
exception is **gcp**: `gcloud` keeps credentials in a separate account store, so
import resolves only the project/region — the service account key is entered in
the form.

### infra

| Module | What it does | Multi-instance |
| --- | --- | --- |
| **kubernetes** | Imports host kube contexts into the workspace `~/.kube/config` (one entry per context). `kubectl` is installed when the first context is added. | Yes (per context) |

### language

| Module | What it does | Notes |
| --- | --- | --- |
| **c** | C dev environment: GCC/build-essential, make, cmake, gdb, and `clangd` (LSP), all from apt. | — |
| **golang** | The Go toolchain (selectable version) plus `gopls`, `delve`, `goimports`, on `PATH`. | Bounces the OpenCode server |
| **nodejs** | Node.js (selectable version) via nvm plus `typescript` and `typescript-language-server`. | Bounces the OpenCode server |
| **python** | Python (selectable version) via uv plus `pyright` and `ruff`. | Bounces the OpenCode server |

Language modules that put a toolchain on `PATH` write to `~/.env`, so installing
or removing them restarts the OpenCode server (and is blocked while a task is
running). The `c` module only installs system packages, so it does not.

### source code

| Module | What it does | Multi-instance |
| --- | --- | --- |
| **git** | Clones a git repository into `~/workspace` (one entry per repo). The git identity (`user.name` / `user.email`) is imported from your host automatically. | Yes (per repo) |

### tools

| Module | What it does | Multi-instance |
| --- | --- | --- |
| **github** | Installs the GitHub CLI (`gh`) and optionally imports this host's `gh` auth (or takes a token). | No |
| **gitlab** | Installs the GitLab CLI (`glab`) and optionally imports this host's `glab` auth (or takes a token). | No |
| **ssh** | Adds an SSH key and host alias (written to `~/.ssh`) for this workspace. | Yes (per host) |

## Multi-instance modules

Some modules can be installed more than once per workspace — one AWS profile, one
SSH host, etc. In the editor's add flow these show an **import picker** listing
the matching accounts found on your host:

- **aws** / **gcp** / **outscale** / **ovh** / **scaleway** — host CLI profiles
- **ssh** — host aliases from `~/.ssh/config`
- **kubernetes** — host kube contexts from `~/.kube/config` (one instance per context)

Select one or more to import as separate instances, or choose **Add manually…**
to fill in an account that isn't on your host. Imported instances store only the
account name in the workspace manifest; their secrets are read from the host at
install time and never persisted.

The **git** module (in the **source code** category) is also multi-instance (one
entry per repository) but has no host list to import from, so its add flow goes
straight to a form where you enter the repository URL (ssh or https). Each repo is
cloned into `~/workspace` (the container's working directory) and is added and
removed independently; removing an entry stops the manager from re-cloning it but
leaves the cloned working tree on disk, so uncommitted work is never lost. SSH
clones auto-accept the remote host key on first contact. The git identity is
imported from your host's global `git config` (`user.name` / `user.email`) on
every install — nothing to type, and nothing is set when the host has no
identity.

## Server restart behaviour

A module that only writes its own config files (e.g. `~/.aws`, `~/.kube/config`,
`~/.ssh`) is **environment-neutral** (`restartServer: false`): tools read those
files live, so the OpenCode server is never bounced, and you can add or remove
the module **while a task is running**.

A module that exports environment variables (writes to `~/.env`) sets
`restartServer: true`: the server is restarted so the variables take effect, and
the editor blocks the change until the workspace is idle. Among the built-ins,
only `golang`, `nodejs`, and `python` restart the server.
