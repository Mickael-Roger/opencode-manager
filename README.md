# opencode-manager

```text
 ██████╗  ██████╗███╗   ███╗
██╔═══██╗██╔════╝████╗ ████║
██║   ██║██║     ██╔████╔██║
██║   ██║██║     ██║╚██╔╝██║
╚██████╔╝╚██████╗██║ ╚═╝ ██║
 ╚═════╝  ╚═════╝╚═╝     ╚═╝
        opencode-manager
```

> **`ocm` is k9s for [OpenCode](https://opencode.ai).**
> One terminal dashboard to create, attach, edit, and tear down OpenCode
> sessions — each one running in its own isolated, per-project container.

📖 **[Read the documentation](https://mickael-roger.github.io/opencode-manager/)** — installation, getting started, concepts, TUI/CLI guides, and modules.

<p align="center">
  <img src="docs/assets/ocm-demo.gif" alt="ocm in action" width="100%">
</p>

## What it does

Coding agents are powerful because they can touch your whole machine — which is
also the danger. Run OpenCode directly and one careless prompt can reach your
cloud accounts, Kubernetes clusters, SSH keys, and tokens.

`ocm` gives each project **its own isolated container**, configured with only the
tools and credentials you pick for it. You manage them all from a single screen:
one keystroke spins a workspace up, drops you into its session, or shuts it down.

<p align="center">
  <img src="docs/assets/ocm-opencode.png" alt="OpenCode TUI inside a workspace" width="100%">
  <br>
  <em>Press <code>Enter</code> on any workspace to drop straight into its OpenCode TUI, running inside the isolated container.</em>
</p>

<p align="center">
  <img src="docs/assets/ocm-edit-kubernetes.png" alt="Adding Kubernetes contexts to a workspace" width="100%">
  <br>
  <em>Press <code>e</code> to add exactly the tools and credentials a project needs — here, importing host Kubernetes contexts (accounts) into a single workspace.</em>
</p>

## Install

```sh
npm install -g @mickaelroger78/opencode-manager
```

This installs two interchangeable commands, `opencode-manager` and its short
alias `ocm`. It also creates the config directory, writes a default
`config.yaml`, and copies the built-in modules — without overwriting anything you
already have.

Requirements: **Docker or Podman** installed on the host, and npm's global bin
directory on your `PATH`.

> No global install? Use `npx @mickaelroger78/opencode-manager`.

## Quickstart

1. **Install** (see above).

2. **Add your OpenCode config.** Every workspace shares the OpenCode templates in
   your global config directory. Copy your existing `opencode.json`, skills,
   commands, agents, and plugins into it:

   ```sh
   cd ~/.config/opencode-manager

   cp /path/to/your/opencode.json .
   cp -r /path/to/your/skills/*   skills/
   cp -r /path/to/your/commands/* commands/
   cp -r /path/to/your/agents/*   agents/
   cp -r /path/to/your/plugins/*  plugins/
   # optional shared instructions:
   cp /path/to/your/AGENTS.md     .
   ```

   These are mounted read-only into every workspace, so editing them on the host
   updates all your workspaces live. (`ocm` creates this directory and an empty
   `opencode.json` for you on first run, so the layout already exists.)

3. **Launch the dashboard.**

   ```sh
   ocm
   ```

4. **Create a workspace**, then add the modules it needs (AWS, Git, Kubernetes,
   SSH…) so the agent gets exactly that project's credentials and nothing else.
   Attach with `Enter` and you're coding.

## Usage

```sh
ocm                              # launch the TUI dashboard
ocm workspaces list              # list workspaces (alias: ocm ws ls)
ocm workspaces attach <ws>       # attach to a workspace session
ocm workspaces create <name> --template backend --start
ocm ws exec <ws> -- go test ./... # run a command in the sandbox
ocm ws run <ws> --prompt "..."   # headless OpenCode run (CI/scripts)
```

From the dashboard you create, attach, edit (`e`), stop, delete, and update
workspaces — all from the keyboard.

The CLI mirrors the dashboard with a `kubectl`-style `ocm <resource> <verb>`
surface (`workspaces`/`ws`, `templates`/`tmpl`, `modules`/`mod`, plus `config`,
`doctor`, and `version`), with `-o json` on read commands for scripting. See the
**[CLI reference](https://mickael-roger.github.io/opencode-manager/cli/)** for the
full command list.

The dashboard table includes a **TOKENS I/O/C** column showing each workspace's
all-time input / output / cache-read token usage (compacted as `k`/`M`/`B`, e.g.
`12.3k/4.5k/89k`).
It is measured with [tokscale](https://www.npmjs.com/package/tokscale) inside the
container — refreshed when a workspace starts and each time it finishes a turn —
and the full breakdown is on the describe page (`d`).

### Templates

A **template** is a reusable, named set of modules-with-configuration — your
recipe for "this kind of project needs AWS + Git + Kubernetes, set up like so".

- Type `:templates` to open the templates page (and `:workspaces` to go back).
- On it, `c` creates a template (name it, then pick its modules just like the
  workspace module editor), `e` (or `Enter`) edits one, and `^d` deletes one.
- When you create a workspace, after naming it you get an optional **Pick
  Template** step: choose one and the new workspace starts with exactly those
  modules already installed (choose *None* to start empty). The picker is skipped
  when you have no templates yet.

Templates are stored as `<workspaceRoot>/templates/<name>.yaml`.

## Configuration

The global config lives at `~/.config/opencode-manager/config.yaml` and sets the
workspace root, container runtime (`docker` or `podman`), base image, and module
directories. The defaults written on install work out of the box; see
[ARCHITECTURE.md](ARCHITECTURE.md#configuration) for every option.

## Learn more

- [**User documentation**](https://mickael-roger.github.io/opencode-manager/) — installation, getting started, concepts, TUI & CLI guides, modules, and troubleshooting.
- [ARCHITECTURE.md](ARCHITECTURE.md) — design, workspace & module model, full configuration, security principle.
- [PROJECT.md](PROJECT.md) — implementation plan and roadmap.
