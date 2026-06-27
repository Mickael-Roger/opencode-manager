# Troubleshooting

## `ocm: command not found` after npm install

npm's global bin directory isn't on your `PATH`. Find it with `npm bin -g` (or
`npm prefix -g` + `/bin`) and add it to your shell profile. Or run without
installing: `npx @mickaelroger78/opencode-manager`.

## "Creating the base image..." hangs or fails

`ocm` pulls or builds the base container image on first start.

- Make sure Docker or Podman is installed and the daemon/service is running:
  `docker info` (or `podman info`) should succeed.
- Confirm `runtime` in `config.yaml` matches what you actually have installed
  (`docker` or `podman`).
- The default base image is pulled from a registry — check network/proxy access.
  Pointing `baseImage.name` at a local image falls back to building locally.

See [Configuration → Base image](configuration.md#base-image).

## Where are the logs?

Diagnostics are written to a file, never the terminal (so they don't disturb the
TUI):

```text
~/.local/share/opencode-manager/logs/opencode-manager.log
```

Raise the detail with `logLevel: debug` in `config.yaml` (`debug`, `info`,
`warning` (default), `error`). See
[Configuration → Logging](configuration.md#logging).

## A module change is blocked while a task is running

Modules that export environment variables (`restartServer: true` — among
built-ins: `golang`, `nodejs`, `python`) require restarting the OpenCode server,
so the editor blocks the edit until the workspace is idle. Wait for the running
task to finish, or use a module that only writes its own config files. See
[Modules → Server restart behaviour](modules.md#server-restart-behaviour).

## An imported account's credentials are missing in the workspace

Multi-instance modules (`aws`, `outscale`, `ssh`) store only the account name in
the manifest and pull secrets from the host at install time via the module's
`resolve` hook. If the account no longer exists on the host, `resolve` emits
nothing and no secret is written. Re-add the account on the host, or use **Add
manually…** to type the credentials directly. See
[Modules → Multi-instance modules](modules.md#multi-instance-modules).

## My OpenCode config / skills / agents aren't showing up

The global OpenCode config is shared read-only from
`~/.config/opencode-manager/`. Files placed there propagate live to running
workspaces, but **adding or removing** entries only takes effect the next time a
workspace container is (re)created. Stop/start the workspace (`t`) to pick up new
entries. See [Concepts → Shared OpenCode config](concepts.md#shared-opencode-config).

## Auth is isolated but I want the host's OpenCode login

Set `useLocalOpenCodeAuth: true` in `config.yaml` to mount the host
`~/.local/share/opencode/auth.json` read-write into each workspace. The default
`false` keeps auth isolated from the host. See
[Configuration](configuration.md#uselocalopencodeauth).

## Resetting a workspace

From the dashboard: stop it with `t`, or delete it with `^d`. Workspace state
lives under the workspace root (`workspace.yaml` and `home/`); removing that
directory after deletion clears everything for that workspace.
