---
sidebar_position: 3
title: "📦 Sources"
description: "Register Packer templates, Ansible roles, and blueprints from a git repo, tarball, or local directory"
keywords: [sources, sharing, blueprints, ansible, packer, git]
---

# 📦 Sources

A source is a versioned bundle of Packer templates, Ansible roles, and blueprints, served from a git repo, tarball, or local directory. `ludus source add` registers the contents in one step.

```bash
# Register the Bad Sector Labs source
ludus source add https://github.com/badsectorlabs/ludus-blueprints

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
        ├── requirements.yml         # galaxy/git role deps for this blueprint
        ├── thumbnail.png
        └── README.md                # long description
```

If a template or role is only used by one blueprint, put it inside that blueprint's directory at `blueprints/<id>/templates/` or `blueprints/<id>/roles/` instead of the top-level `templates/` or `roles/`.

## Common Workflows

### Register Someone Else's Source

```bash
# terminal-command-local
ludus source add https://github.com/badsectorlabs/ludus-blueprints
ludus templates build
ludus blueprint apply badsectorlabs-ludus-blueprints/goad
ludus range deploy
```

`source add` registers the templates and roles, and installs declared galaxy/git role dependencies. Templates are registered but not built; run `ludus templates build` separately.

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
ludus source add https://github.com/foo/ludus-role-pack
# Roles are now installed for your user; no apply step.
```

Templates work the same way; run `ludus templates build` to produce VM images.

### Retry a Partial Source Add

If `source add` fails partway, retry just one blueprint's deps:

```bash
# terminal-command-local
ludus blueprint install badsectorlabs-ludus-blueprints/goad
```

Works on any blueprint you can see, whether it's local or from a source.

## Authoring a Source

### Packer templates

Each `templates/<n>/` directory is a standard Ludus Packer template, the same shape as the [templates bundled with Ludus](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates):

```
templates/my-debian-base/
├── my-debian-base.pkr.hcl   # the Packer build config
├── http/                    # Linux: preseed.cfg / kickstart served at install time
└── Autounattend.xml         # Windows only: unattended install answer file
```

Templates register to a global, single-namespace pool. If two sources both register a template named `my-debian-base`, the second `source add` will conflict. Prefix shared template names with your source slug to avoid collisions.

After `ludus source add`, run `ludus templates build` to produce the VM image. Build is a separate step.

Per-blueprint templates live at `blueprints/<id>/templates/<n>/` and follow the same shape.

### Ansible roles

Each `roles/<n>/` directory is a standard [Ansible role](https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_reuse_roles.html):

```
roles/my_helper/
├── tasks/main.yml           # the role's tasks
├── defaults/main.yml        # default variables
├── handlers/main.yml        # handlers
└── meta/main.yml            # role metadata, dependencies
```

Reference roles by directory name (`my_helper`) under `roles:` in any blueprint's `range-config.yml`. If a local role shares a name with a galaxy role, Ludus skips the galaxy install and uses the local role.

Roles install per-user by default; admins can use `--global-roles` on `source add` to install instance-wide.

Per-blueprint roles live at `blueprints/<id>/roles/<n>/` and follow the same shape.

### Blueprints

Each `blueprints/<id>/` directory holds one blueprint. Two files are required when the directory exists:

`blueprint.yml` holds display metadata. Ludus infers which templates and roles a blueprint needs by reading its `range-config.yml`.

```yaml
manifest_version: 1
id: my-lab
name: "My Lab"
description: "Short tagline"
version: 1.0.0
tags: [ad, workshop]
min_ludus_version: 2.1.2
config: range-config.yml
```

`range-config.yml` is a standard Ludus range config, the same format `ludus range config get` returns. See [Range Config](configuration.mdx).

`requirements.yml` (optional) uses [ansible-galaxy syntax](https://docs.ansible.com/ansible/latest/galaxy/user_guide.html#installing-roles-and-collections-from-the-same-requirements-yml-file). Only needed when a role isn't on galaxy.ansible.com by bare name, or when you need a pinned version:

```yaml
roles:
  - name: badsectorlabs.ludus_adcs
    src: https://github.com/badsectorlabs/ludus_adcs
    version: v1.2.0
```

Blueprints that use only plain galaxy roles (e.g. `geerlingguy.docker`) need no `requirements.yml`.

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

Develop your source locally — pass the directory path directly:

```bash
# First registration: tars and uploads the directory
# terminal-command-local
ludus source add ./my-source-repo --id mysource

# After edits, push the new content
# terminal-command-local
ludus source update mysource ./my-source-repo
```

When ready, push to a remote and switch to the git form:

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

Templates registered by a source are global. Every user sees them automatically; nothing to share.

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

## CLI Reference

### Source Management

| Command | Description |
|---------|-------------|
| `ludus source add <url\|tarball\|directory>` | Register a new source (argument auto-detected) |
| `ludus source list` | List registered sources |
| `ludus source sync [<sourceID>]` | Re-pull a git source (no-op for upload sources) |
| `ludus source update <sourceID> --ref <ref>` | Change a git source's tracked ref |
| `ludus source update <sourceID> <tarball\|directory>` | Push new content to an upload source |
| `ludus source rm <sourceID>` | Remove a source (`--purge` also removes templates/roles registered only by this source) |

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
| `--global-roles` | `source add`, `source sync`, `source update`, `blueprint install` | Admin only. Install roles instance-wide instead of user-scoped |
| `--force` | `source add`, `source sync`, `source update` | Overwrite already-installed templates and galaxy/local roles |
| `--force-roles` | `blueprint install` | Overwrite already-installed galaxy/local roles |
| `--dry-run` | `source add`, `source sync` | Preview planned operations without persisting or installing |
| `--purge` | `source rm` | Also remove templates/roles registered only by this source |
| `--id <sourceID>` | `source add` | Override the auto-derived sourceID |
| `--ref <ref>` | `source add`, `source update` | Git branch, tag, or commit to track |
