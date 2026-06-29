# Installation

## Requirements

- **Docker or Podman** installed and runnable on the host. `ocm` runs one
  container per workspace and builds/pulls a base image on first use.
- **Node.js / npm**, with npm's global bin directory on your `PATH` (only needed
  for the recommended install method below).
- A Linux or macOS host. (`x64` and `arm64` are published.)

## Install with npm (recommended)

```sh
npm install -g @mickaelroger78/opencode-manager
```

This installs two interchangeable commands:

- `opencode-manager` — the full name.
- `ocm` — the short alias.

The npm **postinstall** step also:

- creates the global config directory `~/.config/opencode-manager/`,
- writes a default `config.yaml` (only if one does not already exist),
- copies the built-in modules into your module directory **without overwriting**
  anything you have customised.

Nothing you already have is clobbered, so re-running the install to upgrade is
safe.

## Run without installing

```sh
npx @mickaelroger78/opencode-manager
```

`npx` runs the same binary on demand. The first run still creates the config
directory and seeds defaults.

## Verify the install

```sh
ocm version           # prints the installed version
ocm workspaces list   # prints an (empty) workspace table or "No workspaces"
ocm                   # launches the dashboard
```

On first launch `ocm` ensures the base container image is available and shows
`Creating the base image...` while it is pulled or built. With the default
prebuilt base image this is a quick pull; see
[Configuration → Base image](configuration.md#base-image) for what controls it.

## Upgrading

Re-run the install command. Your `config.yaml`, customised modules, and
workspaces are preserved.

```sh
npm install -g @mickaelroger78/opencode-manager@latest
```

## Uninstalling

```sh
npm uninstall -g @mickaelroger78/opencode-manager
```

This removes the commands but leaves your data in place. To remove everything,
also delete the config directory (`~/.config/opencode-manager/`) and the
workspace root (see [Configuration](configuration.md)). Stop and remove any
remaining workspace containers/images from the dashboard first.
