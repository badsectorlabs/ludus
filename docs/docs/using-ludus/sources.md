---
sidebar_position: 3
title: "📦 Sources"
description: "Register Packer templates, Ansible roles, and blueprints from a git repo, tarball, or local directory"
keywords: [sources, sharing, blueprints, ansible, packer, git]
---

# 📦 Sources

A source is a versioned bundle of Packer templates, Ansible roles, and blueprints, served from a git repo, tarball, or local directory. `ludus source add` registers it and opens an interactive picker for what to install.

```bash
# Register and pick what to install (interactive)
ludus source add https://github.com/badsectorlabs/ludus-blueprints

# Or install everything from the source non-interactively
ludus source add https://github.com/badsectorlabs/ludus-blueprints --all

# Build any required templates that aren't built yet
ludus templates build

# Apply one of the source's blueprints to your range
ludus blueprint apply badsectorlabs-ludus-blueprints/goad

# Deploy
ludus range deploy
```

:::tip Publishing your own

Fork the [Ludus Source Template](https://gitlab.com/badsectorlabs/ludus-source-template) to start your own.

:::

## What's in a Source Repo

A source can include Packer templates, Ansible roles, and blueprints — any combination, as long as it ships at least one.

```
my-source-repo/
├── README.md
├── source.yml                       # repo-level metadata
├── templates/                       # Packer template configs
│   └── win-server-2025/
├── roles/                           # local Ansible roles
│   └── shared_role/
└── blueprints/                      # one directory per blueprint
    └── goad/
        ├── blueprint.yml            # display metadata
        ├── range-config.yml         # the range config
        ├── requirements.yml         # galaxy roles, collections, subscription_roles
        └── thumbnail.png
```

## Common Workflows

### Register Someone Else's Source

```bash
# terminal-command-local
ludus source add https://github.com/badsectorlabs/ludus-blueprints
ludus templates build
ludus blueprint apply badsectorlabs-ludus-blueprints/goad
ludus range deploy
```

By default `source add` runs in two phases: it registers the source (clone or extract + walk), then opens an interactive picker for which blueprints, templates, and source-bundled roles to install. The picker also lists the galaxy roles a selected blueprint will pull in. Pass `--all` to skip the picker, or pass `--blueprints`/`--templates`/`--source-roles` to script the selection. In a non-TTY context (CI, piped stdin) `add` defaults to `--all`.

Templates are registered but not built; run `ludus templates build` separately.

Slug-prefixed IDs (`badsectorlabs-ludus-blueprints/goad`) keep blueprints from different sources separate. If two sources both ship `goad`, they appear as `badsectorlabs-ludus-blueprints/goad` and `secteam-workshop-labs/goad`. Apply by full prefix.

### Fork to Edit

`apply` writes a source blueprint's config into your range. Edit it via `ludus range config get/set`, then save it as a new blueprint:

```bash
# terminal-command-local
ludus blueprint apply badsectorlabs-ludus-blueprints/goad
# ... edit via ludus range config get/set ...
ludus blueprint create --id my-goad --from-range <rangeID>
ludus blueprint apply my-goad
ludus range deploy
```

### Roles-Only or Templates-Only Sources

A source doesn't need to ship blueprints. Register a roles-only or templates-only source the same way:

```bash
# terminal-command-local
ludus source add https://github.com/foo/ludus-role-pack --all
# Roles installed for your user; no apply step.
```

Templates work the same way; run `ludus templates build` to produce VM images.

### Pick or Extend What's Installed

Run `source add <existing-sourceID>` to open the picker against a source you already registered. Useful for installing additional items later, or for finishing a source whose picker you closed without committing.

```bash
# terminal-command-local
ludus source add badsectorlabs-ludus-blueprints                 # opens picker
ludus source add badsectorlabs-ludus-blueprints --blueprints goad  # scripted
ludus source add badsectorlabs-ludus-blueprints --all              # install everything in the catalog
```

Re-adding the same git URL is idempotent — Ludus re-pulls and refreshes the catalog without touching what's already installed. Re-adding the same sourceID with a different URL returns `409`; override the sourceID or use `ludus source update --ref` to change the tracked ref.

### Retry a Partial Source Add

If `source add` fails partway, retry just one blueprint's deps:

```bash
# terminal-command-local
ludus blueprint install badsectorlabs-ludus-blueprints/goad
```

Works on any blueprint you can see, whether it's local or from a source.

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

`description` is the one-line summary shown when browsing the source (`ludus source list` and the GUI picker). `icon_path` is a relative path to an image bundled in the template dir — a square, transparent PNG (256×256 works well) — shown on the template's card, with an OS glyph as the fallback. Packer requires a variable's `default` to be a literal, so both stay static strings.

Templates install per-user and persist across server updates. Each is keyed by the `*-template` name in its `.pkr.hcl` — the name `ludus templates list` reports — so re-installing a name you already have is a no-op, and a name that collides with a built-in template is rejected.

Run `ludus templates build` to produce the VM image — a separate step after `source add`. Built images are shared instance-wide by name, so one build makes a template usable by every range.

### Ansible roles

Each `roles/<n>/` directory is a standard [Ansible role](https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_reuse_roles.html):

```
roles/my_helper/
├── tasks/main.yml           # the role's tasks
├── defaults/main.yml        # default variables
├── handlers/main.yml        # handlers
└── meta/main.yml            # role metadata; galaxy_info.description shows in the catalog
```

Ludus reads each role's `meta/main.yml` `galaxy_info.description` and shows it as the role's description in the catalog and picker.

Reference roles by directory name (`my_helper`) under `roles:` in any blueprint's `range-config.yml`. If a local role shares a name with a galaxy role, Ludus skips the galaxy install and uses the local role.

Roles install per-user by default; admins can use `--global-roles` on `source add` to install instance-wide.

### Blueprints

Each `blueprints/<id>/` directory holds one blueprint. Two files are required when the directory exists:

`blueprint.yml` holds display metadata. Ludus reads `range-config.yml` to surface which templates and roles a blueprint references on the blueprint detail page.

```yaml
manifest_version: 1
id: my-lab
name: "My Lab"
description: "Short tagline"               # shown in the catalog and picker
version: 1.0.0
tags: [ad, workshop]
min_ludus_version: 2.1.2
config: range-config.yml
```

`range-config.yml` is a standard Ludus range config, the same format `ludus range config get` returns. See [Range Config](configuration.mdx).

`requirements.yml` is the manifest for the ansible roles and collections a blueprint depends on. Every role referenced under `roles:` in `range-config.yml` must appear here (or as a directory under the source's top-level `roles/`).

```yaml
roles:
  - name: geerlingguy.docker
    version: 7.4.4                                  # pin a galaxy role
  - name: badsectorlabs.ludus_adcs                  # off-galaxy: name + src
    src: https://github.com/badsectorlabs/ludus_adcs
    version: v1.2.0

collections:                                        # required for any 3-part
  - name: community.crypto                          # FQCN role like
    version: 2.16.0                                 # community.crypto.openssl_certificate
  - name: my.collection                             # off-galaxy collection
    source: https://github.com/foo/my-collection.git # NOTE: collections use
    type: git                                       # `source:` not `src:`
    version: main

subscription_roles:                                 # license-gated roles served
  - ludus_ghosts_client                             # from the Ludus catalog
  - name: ludus_adcs                                
```

Subscription role bytes never travel with a source; only the names. The importing instance's license must cover them. See the [Private Role Catalog](../enterprise/subscription-roles/roles-overview.md).

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

Every source gets a `sourceID` auto-derived from the URL or path when you run `source add`. Git URLs default to `<org>-<repo>` so two repos with the same name under different orgs don't collide. For example:

| Input | Derived sourceID |
|-------|-----------------|
| `https://github.com/badsectorlabs/ludus-blueprints` | `badsectorlabs-ludus-blueprints` |
| `https://github.com/badsectorlabs/ludus-blueprints.git` | `badsectorlabs-ludus-blueprints` |
| `git@gitlab.com:secteam/workshop-labs.git` | `secteam-workshop-labs` |
| `/tmp/my-source.tar.gz` | `my-source` |
| `/home/user/my-workshop-lab` (directory) | `my-workshop-lab` |

Override it with `--id` for a shorter alias:

```bash
# terminal-command-local
ludus source add https://github.com/badsectorlabs/ludus-blueprints --id bsl
ludus blueprint apply bsl/goad
```

If you already have a source registered under the auto-derived ID, pass `--id` to give the new one a distinct alias. The same repo can be added twice to one account this way — useful for tracking different branches of the same upstream.

## Sharing what's in a source

Sources are personal — only the user who ran `source add` sees them in `source list`. To make a source's contents available to others, share each piece individually.

Templates install per-user. The built VM image is shared instance-wide by name, so building a template once makes it usable by every range; another user installs it from the source only to rebuild it.

Roles install per-user. An admin can install them instance-wide by passing `--global-roles` to `source add`, which makes them available to every user on the instance.

Blueprints share per-blueprint with `ludus blueprint share user <sourceID>/<bpID> <userID>` (or `share group`).

```bash
# Admin: register a source with global roles for all users
# terminal-command-local
ludus source add https://github.com/.../my-class --global-roles

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
| `ludus source list` | List registered sources |
| `ludus source sync [<sourceID>]` | Re-pull a git source and re-apply its persisted selection (no-op for upload sources, and a benign no-op for sources that were registered without an install committed) |
| `ludus source update <sourceID> --ref <ref>` | Change a git source's tracked ref |
| `ludus source update <sourceID> <tarball>` (or `-d <dir>`) | Push new content to an upload source |
| `ludus source rm <sourceID>` | Remove a source's registration and blueprints (installed templates and roles stay on disk) |

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
| `--global-roles` | `source add`, `source sync`, `source update`, `blueprint install` | Admin only. Install roles instance-wide instead of user-scoped |
| `--force` | `source add`, `source sync`, `source update` | Overwrite already-installed templates and galaxy/local roles |
| `--force-roles` | `blueprint install` | Overwrite already-installed galaxy/local roles |
| `--id <sourceID>` | `source add` | Override the auto-derived sourceID |
| `--ref <ref>` | `source add`, `source update` | Git branch, tag, or commit to track |
