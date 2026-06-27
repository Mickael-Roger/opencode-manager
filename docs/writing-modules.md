# Writing Modules

A module is a self-contained directory: a thin declarative `module.yml` plus a
couple of executables that do the real work. This page shows how to author one.

For where modules fit, see [Concepts → Modules](concepts.md#modules).

## Anatomy

```text
modules/<category>/<name>/
  module.yml      # metadata + the prompts to collect
  install         # executable: install packages, write files, export env vars
  uninstall       # executable: undo what install did
  resolve         # optional executable: derive container input from host state
  <hook>          # optional helper scripts (e.g. list-accounts)
```

- The **category** is the parent directory name (`cloud`, `infra`, `tools`,
  `language`, or a new one you create). It is purely organisational — the module
  is identified by its globally unique `name`.
- To add a module, drop its directory under a category in one of your configured
  `moduleDirs`. The whole tree is bind-mounted read-only into every workspace at
  `/opt/opencode-manager/modules` mirroring the `category/name` layout.

## `module.yml`

`module.yml` declares only metadata and the values to ask the user for:

```yaml
name: aws
version: 1
description: Install the AWS CLI and write an isolated profile + credentials.
restartServer: false        # see "Server restart" below
key: profile                # optional — makes the module multi-instance
prompts:
  - name: profile
    label: AWS profile name
    type: string
    required: true
    default: default
    optionsCommand: list-accounts   # host hook to populate choices
  - name: region
    label: Default region
    type: string
    default: eu-west-3
  - name: secret_key
    label: Secret access key
    type: secret
```

### Fields

| Field | Meaning |
| --- | --- |
| `name` | Globally unique module identifier. |
| `version` | Integer; bump when the module's behaviour changes. |
| `description` | One line shown in the editor and `/` filter. |
| `restartServer` | `true` (default) if the module touches `~/.env`; `false` if it only writes its own config files. |
| `key` | Name of a prompt that makes the module **multi-instance** (one install per distinct value). |
| `prompts` | The values to collect from the user. |

### Prompt types

| `type` | Behaviour |
| --- | --- |
| `string` | Free-text input. |
| `secret` | Masked input; not echoed. |
| `select` | Single choice from `options`. |
| `multiselect` | Multiple choices from `options`. |

Each prompt supports `name`, `label`, `type`, `required`, and `default`. A
`select`/`multiselect` prompt may list static `options`, or populate them
dynamically from the host via `optionsCommand` (see below).

## The `install` and `uninstall` scripts

These run **inside the workspace container** as the workspace user (sandboxed),
with passwordless `sudo` available. They receive each prompt value as an `OCM_*`
environment variable — uppercased prompt name (e.g. `OCM_PROFILE`,
`OCM_SECRET_KEY`).

`install` should:

- install any packages it needs (`sudo apt-get install ...`, `npx`, `uvx`, …);
- write config files into the home directory;
- **export environment variables** by appending `export VAR=value` to `~/.env`
  (a supervisor process sources it so the variable reaches the OpenCode server).

`uninstall` must undo exactly what `install` did.

> **Make multi-instance scripts instance-scoped by the key.** For example the
> `ssh` module writes `~/.ssh/config.d/$OCM_HOST.conf`, so multiple entries
> coexist and each `uninstall` removes only its own.

### Server restart

If your `install` writes to `~/.env`, leave `restartServer` at its default
(`true`): the manager bounces the OpenCode server so the variables take effect,
and the editor blocks edits while a task is running. If your module only writes
its own config files that tools read live (like `~/.aws` or `~/.kube/config`),
set `restartServer: false` — it is never bounced and can be edited mid-task.

## Host hooks (optional)

Two optional hooks run **on the host** (with full host access), as part of an
explicit user action — they let a module read host state it must not store.

### `optionsCommand` on a prompt

Names an executable in the module directory that prints one choice per line on
stdout. It populates a `select`/`multiselect` prompt's options from host state —
e.g. the `kubernetes` module's `list-contexts` lists host kube contexts.

A multi-instance module's **`key` prompt** may also carry an `optionsCommand`
(the only non-select prompt allowed to): it drives the import picker that lists
host accounts to import as new instances (e.g. `aws`'s `list-accounts`,
`ssh`'s `list-hosts`).

### The `resolve` script

A module-level `resolve` executable (present == enabled) runs **before** the
container install, with the collected values as `OCM_*` env vars. It prints extra
`key=value` lines that are merged into the install environment but **not**
persisted to the manifest.

Use `resolve` to derive container input from host-only state you must not store —
for example extracting the selected account's secret key from the host CLI config
so it never lands in `workspace.yaml`. `resolve` is re-run on every add and
reconcile. It must emit nothing for an account it does not find on the host,
leaving any manually-typed values in place.

## Checklist

- [ ] `module.yml` with a unique `name`, accurate `description`, and correct
      `restartServer`.
- [ ] `install` is idempotent and instance-scoped (if `key` is set).
- [ ] `uninstall` fully reverses `install` for that instance.
- [ ] Secrets resolved on the host via `resolve` rather than stored in the
      manifest, where applicable.
- [ ] Tested by adding/removing the module on a running workspace from the
      editor.
