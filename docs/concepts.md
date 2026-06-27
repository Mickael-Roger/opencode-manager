# Concepts

Four ideas explain almost everything about `opencode-manager`: **workspaces**,
**modules**, **templates**, and the **security principle** that ties them
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

A **workspace** is one isolated OpenCode environment, backed by a long-lived
container. Each workspace has:

- a **name**;
- a dedicated directory under the configured workspace root;
- a dedicated **home directory** (`home/`);
- the global `opencode.json` mounted read-only;
- the globally shared OpenCode commands, skills, agents, and plugins, mounted
  read-only;
- its selected **module** configuration;
- a generated **image** and a long-lived, attachable **container** that runs
  OpenCode interactively.

At the workspace root only `workspace.yaml` and `home/` are created. Environment
values, image/package requirements, module state, and generated OpenCode paths
are tracked through `workspace.yaml` and files under `home/`. You clone your
project repositories inside the workspace home directory.

The container runs a small entrypoint that starts `opencode` for the first
session and `opencode -c` when sessions already exist.

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

These are **mounted read-only** into every workspace at
`/home/debian/.config/opencode/`. Editing a file on the host propagates live to
all running workspaces — no copy, no recreation. Adding or removing an entry
takes effect the next time a workspace container is (re)created.

On startup `ocm` creates any missing entries so the mounts always have a source:
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
