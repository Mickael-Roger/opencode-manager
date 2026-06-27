# Templates

A **template** is a reusable, named set of modules-with-configuration — your
recipe for "this kind of project needs AWS + Git + Kubernetes, set up like so".
It carries no workspace-specific state: no container, no image, no home
directory, just the module recipe.

## Managing templates

Open the templates page from the dashboard by typing `:templates` (and
`:workspaces` to return). It reuses the same module editor as workspaces:

| Key | Action |
| --- | --- |
| `c` | Create a template — name it, then pick its modules and fill their prompts |
| `e` / `↵` | Edit a template |
| `^d` | Delete a template |

Creating or editing a template selects modules and collects their prompt values
exactly as for a live workspace — but applying it **saves the recipe** instead of
installing into a container.

## Using a template

When you create a workspace (`c`), after naming it you get an optional **Pick
Template** step:

- choose a template → the new workspace starts with exactly those modules already
  installed;
- choose **None** → start empty.

The picker is skipped when you have no templates yet.

Under the hood, the chosen template's module instances are copied into the new
workspace's `workspace.yaml` *before* provisioning; the normal reconcile step then
installs those modules on first start — the same path that converges a freshly
recreated container to its manifest.

## Storage

Each template is a single YAML file:

```text
<workspaceRoot>/templates/<slug>.yaml
```

`<slug>` is the template name run through the same safe-name slugging used for
workspace directories. The file holds the template name and a list of module
instances identical in shape to a workspace manifest's `modules:` entries (name,
id, category, version, values).

Because templates are plain files, you can version them, share them, or edit them
by hand.
