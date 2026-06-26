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
ocm                      # launch the TUI dashboard
ocm list                 # list workspaces
ocm attach <workspace>   # attach to a workspace session
```

From the dashboard you create, attach, edit (`e`), stop, delete, and update
workspaces — all from the keyboard.

## Configuration

The global config lives at `~/.config/opencode-manager/config.yaml` and sets the
workspace root, container runtime (`docker` or `podman`), base image, and module
directories. The defaults written on install work out of the box; see
[ARCHITECTURE.md](ARCHITECTURE.md#configuration) for every option.

## Learn more

- [ARCHITECTURE.md](ARCHITECTURE.md) — design, workspace & module model, full configuration, security principle.
- [PROJECT.md](PROJECT.md) — implementation plan and roadmap.
