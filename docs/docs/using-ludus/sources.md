---
sidebar_position: 3
title: "📦 Sources"
description: "Install blueprints, Packer templates, and/or Ansible roles and collections from a git repo, tarball, or local directory"
keywords: [sources, sharing, blueprints, ansible, collections, packer, git]
---

# 📦 Sources

A source is a versioned bundle of blueprints, Packer templates, and Ansible roles and collections. `ludus source add` registers it and opens an interactive installer.

```bash
# Pick what to install
ludus source add https://github.com/badsectorlabs/ludus-source-bsl

# Script an install
ludus source add https://github.com/badsectorlabs/ludus-source-bsl --blueprints goad

```

## What's in a Source Repo

```
my-source-repo/
├── README.md
├── source.yml                       # repo-level metadata
├── templates/                       # Packer template configs
│   └── win-server-2025/
├── ansible/
│   ├── roles/                       # Ansible roles
│   │   └── shared_role/
│   └── collections/                 # Ansible collections
│       └── my_namespace.my_collection/
└── blueprints/                      # one directory per blueprint
    └── goad/
        ├── blueprint.yml            # display metadata
        ├── range-config.yml         # the range config
        ├── requirements.yml         # required ansible roles, collections, or ludus subscription roles
        └── thumbnail.png
```

:::warning Secure code execution

Packer templates and Ansible roles run on the Ludus host as the `ludus` user — with your Proxmox credentials in scope. Consider reviewing the repo before installing resources or pinning an immutable commit with `--ref <commit-sha>`.

:::

## Submodules

Any asset subdirectory — a blueprint, template, role (`ansible/roles/<name>/`) or
collection (`ansible/collections/<dir>/`) — can be a **git submodule**. When you
register or sync a git-backed source, Ludus clones it with `--recurse-submodules`,
so submodules are pulled (and refreshed on re-sync) automatically. This lets a
source aggregate content that lives in its own repository while keeping that repo
independent for issues and development.

Each submodule points at its upstream repository URL in `.gitmodules` (e.g.
`https://github.com/badsectorlabs/ludus_adcs.git`); public repositories clone
without credentials.

## Common Workflows

### Register Someone Else's Source

```bash
# terminal-command-local
ludus source add https://github.com/badsectorlabs/ludus-source-bsl
ludus templates build
ludus blueprint apply badsectorlabs-ludus-source-bsl/goad
ludus range deploy
```

By default `source add` runs in two phases: it registers the source (clone or extract + walk), then opens an interactive picker for which blueprints, templates, and source-bundled roles and collections to install. The picker also lists the galaxy roles and collections a selected blueprint will pull in. Pass `--all` to skip the picker, or pass `--blueprints`/`--templates`/`--source-roles`/`--source-collections` to script the selection. In a non-TTY context (CI, piped stdin) `add` defaults to `--all`.

Templates are registered but not built; run `ludus templates build` separately.

Slug-prefixed IDs (`badsectorlabs-ludus-source-bsl/goad`) keep blueprints from different sources separate. If two sources both ship `goad`, they appear as `badsectorlabs-ludus-source-bsl/goad` and `secteam-workshop-labs/goad`. Apply by full prefix.

### Fork to Edit Source Blueprints

```bash
# terminal-command-local
ludus blueprint create --from-blueprint badsectorlabs-ludus-source-bsl/goad --id scratch-pad
ludus blueprint config edit scratch-pad
ludus blueprint apply scratch-pad
ludus range deploy
```

### Roles-Only or Templates-Only Sources

A source doesn't need to ship blueprints. Register a roles-only or templates-only source the same way:

```bash
# terminal-command-local
ludus source add https://github.com/foo/ludus-role-pack --all
# Roles installed for your user; no apply step.
```

### Pick or Extend What's Installed

Run `source add <existing-sourceID>` to open the picker against a source you already registered. Useful for installing additional items later, or for finishing a source whose picker you closed without committing.

```bash
# terminal-command-local
ludus source add badsectorlabs-ludus-source-bsl                 # opens picker
ludus source add badsectorlabs-ludus-source-bsl --blueprints goad  # scripted
ludus source add badsectorlabs-ludus-source-bsl --all              # install everything in the catalog
```

Re-adding the same git URL is idempotent — Ludus re-pulls and refreshes the catalog without touching what's already installed. Re-adding the same sourceID with a different URL returns `409`; pick a different sourceID, or repoint the existing source with `ludus source set-url <sourceID> <git-url>`.

### Private Git Repos

Ludus runs `git clone` under the `ludus` system user, inheriting whatever git auth that user has configured on the host — no Ludus-side flags or secret storage. Here is a recipe for setting up an SSH deploy key:

```bash
# terminal-command-host (run as root on the Ludus host)
sudo -u ludus -H bash -c '
  mkdir -p ~/.ssh && chmod 700 ~/.ssh
  cp /path/to/deploy_key ~/.ssh/id_ed25519
  chmod 600 ~/.ssh/id_ed25519
  ssh-keyscan github.com >> ~/.ssh/known_hosts
'
# Then register the source with an SSH URL:
ludus source add git@github.com:owner/private-repo
```

Other git auth schemes (HTTPS credential helpers, `~/.git-credentials`, SSH agents, etc.) also work in principle — anything you can make `sudo -u ludus -H git clone <url>` succeed with on the host will work for Ludus.

Caveats:

- Host-wide: every Ludus user on the instance clones with the same credentials. For multi-tenant deployments where users need distinct git access, treat private sources as not yet supported — per-source credentials are not implemented.
- The systemd unit applies `ProtectHome=read-only`, so the service can read `/home/ludus/` but cannot write to it. Set up credentials from a root shell, not from inside the service.

## Authoring a Source

:::tip Publishing your own

Fork the [Ludus Source Template](https://github.com/badsectorlabs/ludus-source-template) to start your own.

:::

### Packer templates

Each `templates/<n>/` directory is a standard Ludus Packer template, the same shape as the [templates in the Bad Sector Labs source](https://github.com/badsectorlabs/ludus-source-bsl/tree/main/templates):

```
templates/my-debian-base/
├── my-debian-base.pkr.hcl   # the Packer build config (incl. description + icon_path vars)
├── icon.png                 # optional: the catalog icon referenced by the icon_path variable
├── http/                    # Linux: preseed.cfg / kickstart served at install time
└── Autounattend.xml         # Windows only: unattended install answer file
```

Give the template a catalog description and icon with two static variables in the `.pkr.hcl`, so all of a template's metadata lives in one file:

```hcl
variable "description" {
  type    = string
  default = "Debian 12 minimal base image."
}

variable "icon_path" {
  type    = string
  default = "icon.png"
}
```

`description` is the one-line summary shown when browsing the source via the TUI and GUI installers. `icon_path` is a relative path to an image bundled in the template dir — a square, transparent PNG (256×256 works well) — shown on the template's card, with an OS glyph as the fallback. Packer requires a variable's `default` to be a literal, so both stay static strings.

Templates install per-user and persist across server updates. Each is keyed by the `*-template` name in its `.pkr.hcl` — the name `ludus templates list` reports — so re-installing a name you already have is a no-op, and a name that collides with a built-in template is rejected.

Run `ludus templates build` to produce the VM image — a separate step after `source add`. Built images are shared instance-wide by name, so one build makes a template usable by every range.

### Ansible roles

Each `ansible/roles/<name>/` directory is a standard [Ansible role](https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_reuse_roles.html):

```
ansible/roles/my_helper/
├── tasks/main.yml           # the role's tasks
├── defaults/main.yml        # default variables
├── handlers/main.yml        # handlers
├── meta/version.yml         # optional: defines the display version for the role in Ludus (overrides git tag detection)
└── meta/main.yml            # role metadata; galaxy_info.description shows in the catalog

```

Ludus reads each role's `meta/main.yml` `galaxy_info.description` and shows it as the role's description in the catalog and picker.

Reference roles by directory name (`my_helper`) under `roles:` in any blueprint's `range-config.yml`. If a local role shares a name with a galaxy role, Ludus skips the galaxy / subscription role install and uses the local role.

Roles install per-user by default; admins can install globally via the TUI or use the `--global` flag on a scripted `source add`.

### Ansible collections

Each `ansible/collections/<dir>/` directory is a standard [Ansible collection](https://docs.ansible.com/ansible/latest/dev_guide/developing_collections_structure.html) — any directory with a `galaxy.yml` at its root:

```
ansible/collections/my_namespace.my_collection/
├── galaxy.yml              # namespace, name, version, description
├── roles/                  # collection roles
├── plugins/                # modules, filters, lookups, etc.
└── playbooks/
```

The collection's identity is the `namespace.name` from its `galaxy.yml` — not the directory name. Ludus reads `galaxy.yml` for the version and `description` (shown in the catalog and picker) and installs it under the namespaced collections path, where a blueprint or range config references collection roles with fully-qualified names (`my_namespace.my_collection.some_module`).

Like roles, collections install per-user by default; admins can install globally via the TUI or use the `--global` flag on a scripted `source add`.

### Blueprints

Each `blueprints/<id>/` directory holds one blueprint in the standard on-disk format: `blueprint.yml` (display metadata) and `range-config.yml` are required, plus `requirements.yml` when the blueprint has galaxy or subscription dependencies. See [Blueprints: Directory Structure](./blueprints.md#directory-structure) for the file formats.

Two rules are specific to sources:

- Every role referenced under `roles:` in a blueprint's `range-config.yml` must be declared in its `requirements.yml` **or** vendored as a directory under the source's `ansible/roles/` (collections likewise under `ansible/collections/`). Vendored copies win: they install from the source's pinned content and are never re-fetched from galaxy.
- Subscription role bytes never travel with a source; only the names declared under `subscription_roles:`. The importing instance's license must cover them. See the [Private Role Catalog](../enterprise/subscription-roles/roles-overview.md).

### `source.yml` at repo root

Repo-level metadata used by `ludus source list`. License, homepage, and authors apply to the source as a whole; blueprints in the source inherit them.

```yaml
manifest_version: 1
name: "My Lab Library"
description: "Production-ready labs"
authors:
  - "Alice Anderson <alice@example.com>"
  - "Bob Builder <bob@example.com>"
homepage: https://example.com/labs
license: MIT
```

When absent, Ludus defaults `name` to the derived sourceID and `homepage` to the git URL for git sources.

### Local development workflow

Develop your source locally — pass the directory via `-d`:

```bash
# First registration: tars and uploads the directory
# terminal-command-local
ludus source add -d ./my-source-repo --id mysource

# After edits, push the new content
# terminal-command-local
ludus source update mysource -d ./my-source-repo
```

When ready, push to a remote and switch to the git form.

```bash
# terminal-command-local
ludus source rm mysource
ludus source add https://github.com/you/my-source-repo
```

## Source IDs

When `--id` is omitted, Ludus derives an ID from the URL or path and prefixes it with the effective user's `userID`. Git URLs use `<userID>-<org>-<repo>`, so different users can register the same source without colliding. For example, these commands run as user `JD`:

| Input | Derived sourceID |
|-------|-----------------|
| `https://github.com/badsectorlabs/ludus-source-bsl` | `JD-badsectorlabs-ludus-source-bsl` |
| `https://github.com/badsectorlabs/ludus-source-bsl.git` | `JD-badsectorlabs-ludus-source-bsl` |
| `git@gitlab.com:secteam/workshop-labs.git` | `JD-secteam-workshop-labs` |
| `/tmp/my-source.tar.gz` | `JD-my-source` |
| `/home/user/my-workshop-lab` (directory) | `JD-my-workshop-lab` |

Pass `--id` to choose the complete source ID yourself. Explicit IDs are used as supplied and remain globally unique:

```bash
# terminal-command-local
ludus source add https://github.com/badsectorlabs/ludus-source-bsl --id bsl
ludus blueprint apply bsl/goad
```

Re-adding the same source under its derived ID is idempotent. Use `--id` when you want a shorter name or need another registration for a different branch of the same upstream.

## Sharing what's in a source

Sources are personal — only the user who ran `source add` sees them in `source list`. To make a source's contents available to others, share each piece individually.

Templates install per-user. The built VM image is shared instance-wide by name, so building a template once makes it usable by every range; another user installs it from the source only to rebuild it.

Roles and collections install per-user. An admin can install them instance-wide by passing `--global` to `source add`, which makes them available to every user on the instance.

Blueprints share per-blueprint with `ludus blueprint share user <sourceID>/<bpID> <userID>` (or `share group`).

```bash
# Admin: register a source with global roles and collections for all users
# terminal-command-local
ludus source add https://github.com/.../my-class --global

# Share each blueprint with the class group
# terminal-command-local
ludus blueprint share group <sourceID>/lab-1 students
ludus blueprint share group <sourceID>/lab-2 students
```

## Startup behavior

On startup the Ludus server auto-registers the [Bad Sector Labs source](https://github.com/badsectorlabs/ludus-source-bsl) — owned by ROOT, visible to every admin — and re-syncs every registered source's catalog. Both run by default; disable either in `/opt/ludus/config.yml`:

```yaml
register_default_source: false   # don't auto-register the Bad Sector Labs source
sync_sources_on_startup: false   # don't re-sync registered sources on each startup
```

A source removed with `ludus source rm` is re-registered on the next restart unless `register_default_source: false` is set.

## CLI Reference

### Source Management

| Command | Description |
|---------|-------------|
| `ludus source add <url\|tarball\|directory\|existing-sourceID>` | Register a new source or open the picker for an existing one (argument auto-detected) |
| `ludus source list [<sourceID>]` | List registered sources, or show one source's metadata (`--catalog` to see what it ships instead) |
| `ludus source sync [<sourceID>]` | Re-pull a git source's content and refresh its catalog (read-only — installs nothing) |
| `ludus source set-url <sourceID> <git-url>` | Repoint a git source at a new remote URL (`--ref` to also switch the tracked ref) |
| `ludus source update <sourceID> <tarball>` (or `-d <dir>`) | Push new content to an upload source |
| `ludus source rm <sourceID>` | Remove a source's registration and blueprints (installed templates, roles, and collections stay on disk) |

Installing is additive — re-running `ludus source add` only ever adds to what's installed, and each install acts only on the items you name. To uninstall, remove items with the per-artifact commands: `ludus blueprint rm <sourceID>/<blueprint>`, `ludus templates rm -n <name>`, `ludus ansible role rm <name>`, or `ludus ansible collection rm <fqcn>`. Each one also releases the source's claim on the artifact, and nothing reinstalls removed items behind your back (re-running `ludus source add <sourceID> --all` reinstates everything).

### Blueprint Commands (Extended for Sources)

| Command | Description |
|---------|-------------|
| `ludus blueprint list` | List local and source blueprints; `--tag <tag>` filters by tag |
| `ludus blueprint apply <id>` | Apply a local blueprint or a source blueprint (`bsl/goad`) |
| `ludus blueprint install <id>` | Install one blueprint's role dependencies |
| `ludus blueprint info <id>` | Show metadata and dependency status |

### Useful Flags

| Flag | Available on | Description |
|------|-------------|-------------|
| `--all` | `source add` | Skip the picker; install everything the source ships |
| `--blueprints <ids>` | `source add` | Scripted selection: blueprint IDs to install (CSV or repeated) |
| `--templates <names>` | `source add` | Scripted selection: template names to install (CSV or repeated) |
| `--source-roles <names>` | `source add` | Scripted selection: source-bundled role names to install (CSV or repeated) |
| `--source-collections <fqcns>` | `source add` | Scripted selection: source-bundled collection FQCNs to install (CSV or repeated) |
| `--global` | `source add`, `source sync`, `source update`, `blueprint install` | Admin only. Install the source's roles and collections instance-wide instead of user-scoped |
| `--force` | `source add`, `source sync`, `source update` | Overwrite already-installed templates and galaxy/local roles |
| `--force-roles` | `blueprint install` | Overwrite already-installed galaxy/local roles |
| `--id <sourceID>` | `source add` | Override the auto-derived sourceID |
| `--ref <ref>` | `source add`, `source set-url` | Git branch, tag, or commit to track |
