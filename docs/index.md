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

![ocm in action](assets/ocm-demo.gif)

## Why it exists

Coding agents are powerful because they can touch your whole machine — which is
also the danger. Run OpenCode directly and one careless prompt can reach your
cloud accounts, Kubernetes clusters, SSH keys, and tokens.

`opencode-manager` (`ocm`) gives every project **its own isolated container**,
configured with only the tools and credentials you explicitly grant it through
**modules**. You manage them all from a single keyboard-driven dashboard: one
keystroke spins a workspace up, drops you into its OpenCode session, or tears it
down.

## How this documentation is organised

| Section | What you'll find |
| --- | --- |
| [Installation](installation.md) | Requirements and how to install `ocm`. |
| [Getting Started](getting-started.md) | From zero to your first coding session. |
| [Concepts](concepts.md) | Workspaces, modules, templates, and the security model. |
| [TUI Guide](tui.md) | Every page, key, and column in the dashboard. |
| [CLI Reference](cli.md) | The non-interactive commands for automation. |
| [Configuration](configuration.md) | Every option in `config.yaml`. |
| [Modules](modules.md) | Using the built-in modules. |
| [Writing Modules](writing-modules.md) | Authoring your own modules. |
| [Templates](templates.md) | Reusable module recipes. |
| [Troubleshooting](troubleshooting.md) | Common problems and fixes. |

## Design documents

These live at the repository root and describe intent and internals rather than
usage:

- [ARCHITECTURE.md](https://github.com/Mickael-Roger/opencode-manager/blob/main/ARCHITECTURE.md) — design, workspace & module model, security principle.
- [PROJECT.md](https://github.com/Mickael-Roger/opencode-manager/blob/main/PROJECT.md) — implementation plan and roadmap.
- [AGENTS.md](https://github.com/Mickael-Roger/opencode-manager/blob/main/AGENTS.md) — contributor and agent guidelines.
