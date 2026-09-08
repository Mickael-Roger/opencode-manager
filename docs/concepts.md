# Concepts

Five ideas explain almost everything about `opencode-manager`: **workspaces**,
**agent runtimes**, **modules**, **templates**, and the **security principle** that ties them
together.

## Security principle

> A workspace starts with **no implicit host access**.

Every cloud credential, Kubernetes context, SSH key, token, config file, tool,
command, skill, agent, or environment variable must be added by a **selected
module**. Nothing from your host leaks in unless a module brings it.

Secrets a module needs may be stored as environment variables or plain-text
files **inside the workspace** — never silently shared from the host. This is the
whole point: a careless agent prompt can only reach what you deliberately handed
to that one workspace.

## Workspaces

A **workspace** is one isolated coding-agent environment, backed by a long-lived
container. Each workspace has:

- a **name**;
- a dedicated directory under the configured workspace root;
- a dedicated **home directory** (`home/`);
- a one-way copy of shared OpenCode configuration, including `opencode.json`,
  commands, skills, agents, and plugins;
- its selected **module** configuration;
- a generated **image** and a long-lived, attachable **container**;
- OpenCode by default and, when enabled, DeepSeek Harness with its `web` profile.

At the workspace root only `workspace.yaml` and `home/` are created. Environment
values, image/package requirements, module state, and generated OpenCode paths
are tracked through `workspace.yaml` and files under `home/`. You clone your
project repositories inside the workspace home directory.

The container runs a supervised OpenCode server. DeepSeek Harness configuration
and state are stored below `home/.config/deepseek/`; OCM will supervise its Web
server as a separate loopback runtime.

## Agent runtimes

Agent runtimes consume the workspace's files, tools, credentials, and isolation;
they are not modules. A workspace records `defaultRuntime` and enabled `runtimes`
in `workspace.yaml`. Existing manifests without these fields behave exactly as
before: OpenCode is enabled and remains the default.

```yaml
defaultRuntime: opencode
runtimes:
  opencode:
    enabled: true
  deepseek:
    enabled: true
```

The runtimes have independent configuration and session state but see the same
project filesystem and module-provisioned capabilities. Each DeepSeek-enabled
workspace starts `dsh web --port <port> --no-open`. DSH emits a new startup URL
with a token on every restart; OCM stores just that token, with owner-only
permissions, at `home/.config/deepseek/web-token`. Attaching a DeepSeek
workspace opens the embedded `dsh-tui` terminal client against that server
(see the [TUI guide](tui.md#attach-enter)).

## Shared DeepSeek Harness config

DeepSeek-enabled workspaces receive a one-way copy of non-secret configuration
from `~/.config/opencode-manager/deepseek/`. For example, place `settings.yaml`,
`cordis.patch.yml`, or other managed patches there. Source changes reconcile into
each enabled workspace at `/home/debian/.config/deepseek/` while OCM is running.

OCM synchronizes `profiles/<name>/package.json` so you can centrally add or
remove profile modules. It ignores runtime state: `.credentials.yaml`, `.env`,
sessions, `node_modules/`, and package lockfiles are never copied.
After each managed workspace start, OCM runs `dsh plugin --profile <name> install
--no-frozen-lockfile` and each workspace maintains its own lockfile. OAuth/Codex logins and DSH
sessions are likewise never overwritten by configuration synchronization.

## Modules

**Modules** add capabilities to a workspace. A module is a self-contained
directory with a declarative `module.yml` plus executables that do the work,
grouped under a **category** (e.g. `cloud`, `infra`, `tools`, `language`):

```text
modules/cloud/aws/
  module.yml      # name, version, description, prompts to collect
  install         # set up packages, files, env vars
  uninstall       # undo what install did
```

Key properties:

- **Runtime layer, not image layer.** Adding or removing a module on a running
  workspace just runs its `install`/`uninstall` inside the live container — no
  image rebuild and (usually) no restart.
- **Categories are organisational.** A module is identified by its globally
  unique `name`; the category just groups it in the editor.
- **Multi-instance modules** (e.g. `aws`, `outscale`, `ssh`) can be installed
  several times per workspace — one per profile / host — and can import the
  accounts already configured on your host.

The whole module directory is bind-mounted read-only into every workspace at
`/opt/opencode-manager/modules`. See [Modules](modules.md) for the built-ins and
[Writing Modules](writing-modules.md) to author your own.

## Templates

A **template** is a reusable, named set of modules-with-configuration — your
recipe for "this kind of project needs AWS + Git + Kubernetes, set up like so",
with no workspace-specific state (no container, image, or home).

When you create a workspace you can pick a template, and the new workspace starts
with exactly those modules already installed. Templates are stored as
`<workspaceRoot>/templates/<name>.yaml`. See [Templates](templates.md).

## Shared OpenCode config

OpenCode configuration is shared across all workspaces from this source tree:

```text
~/.config/opencode-manager/opencode/
├── AGENTS.md
├── opencode.json
├── agents/
├── commands/
├── plugins/
└── skills/
```

The manager copies this tree one way into each workspace at
`/home/debian/.config/opencode/` during provisioning, at startup, and after a
host-side source change while it is active. Host changes win and workspace
changes never flow back. A per-workspace journal lets source removals remove only
entries that the manager previously copied. Generated top-level state such as
package manifests, lockfiles, and `node_modules` is intentionally local.

On startup `ocm` creates any missing entries in the shared source:
empty `AGENTS.md` and `agents/`, `commands/`, `plugins/`, `skills/` directories,
and a minimal valid `opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json"
}
```

Existing files are never overwritten. Per-project overrides are still possible
via an `opencode.json` in the workspace project directory.

## Token accounting

Each workspace's all-time input / output / cache-read token usage is measured
with [tokscale](https://www.npmjs.com/package/tokscale) inside the container —
refreshed when a workspace starts and each time it finishes a turn. The
dashboard shows a compacted **TOKENS I/O/C** column; the full breakdown is on the
describe page (`d`). See [TUI Guide](tui.md#the-tokens-column).
