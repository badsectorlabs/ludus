---
sidebar_position: 2
title: "🗺️ Blueprints"
description: "Save, share, and reuse range configurations with blueprints"
keywords: [blueprints, range, config, sharing, reuse]
---

# 🗺️ Blueprints

A blueprint is a range config bundled with its dependencies: pinned role versions, copies of any local roles, and template build configs.

```bash
# Save your range config as a blueprint
ludus blueprint create --id ad-lab --name "AD Lab"

# Share it with your team
ludus blueprint share group ad-lab sec-team

# A teammate creates a new range from it
ludus range create -r ad-lab -n "AD Lab" --from-blueprint ad-lab

# Deploy the range
ludus range deploy
```

:::tip Where blueprints come from

Create blueprints from your own ranges with `ludus blueprint create --from-range`, or register them in bulk from a [source](./sources.md): a git repo or tarball that ships blueprints, roles, and templates together.

:::

## Creating a Blueprint

### Blueprint IDs
Every blueprint has a unique identifier and must follow these rules:
  - Start with a letter (A-Z or a-z)
  - Followed by any number of: alphanumeric, `_`, or `-` characters
  - Optionally up to 2 `/`-separated segments, each containing alphanumeric, `_`, or `-`

#### Examples
```
- `my-blueprint`
- `team/windows`
- `org/team/prod-lab`
```

### From Scratch

By default, `blueprint create` seeds the new blueprint with the same example range config that `range create` uses (a small AD lab) so you start with a working layout to edit:

```bash
# terminal-command-local
ludus blueprint create --id <blueprintID>
ludus blueprint config edit <blueprintID>
```

Override the seed with your own YAML in one shot:

```bash
# terminal-command-local
ludus blueprint create --id <blueprintID> --config ./range-config.yml
```

The metadata flags (`--name`, `--description`, `--version`, `--tag`, `--min-ludus-version`) are accepted inline — no follow-up `update` needed.

### From a Range

```bash
# terminal-command-local
ludus blueprint create --id <blueprintID> --from-range <rangeID>
```

### Copying an Existing Blueprint

```bash
# terminal-command-local
ludus blueprint create --from-blueprint <sourceBlueprintID>
```

The copy is owned by you and is shared with no one. `--id` is optional and if it is omitted, the copy's blueprint ID will be `{source}-copy`, then `{source}-copy-2`, etc.

## Listing Blueprints

```bash
# terminal-command-local
ludus blueprint list
```

```
+--------------------+------------------+---------+--------+--------------+---------------+------------------+
|   BLUEPRINT ID     |       NAME       |  OWNER  | ACCESS | SHARED USERS | SHARED GROUPS |     UPDATED      |
+--------------------+------------------+---------+--------+--------------+---------------+------------------+
| ad-lab             | AD Lab           | JD-1    | owner  |            2 |             1 | 2026-02-10 09:30 |
| team/windows       | Windows Range    | JD-1    | owner  |            0 |             0 | 2026-02-12 14:15 |
| org/sec/malware    | Malware Lab      | admin   | group  |            3 |             2 | 2026-02-15 11:00 |
+--------------------+------------------+---------+--------+--------------+---------------+------------------+
```

The `ACCESS` column shows your relationship to each blueprint: `admin`, `owner`, `direct` (shared with you), `group` (shared via a group), or `source` (inherited from a shared [source](./sources.md)).

## Viewing a Blueprint Config

```bash
# terminal-command-local
ludus blueprint config get <blueprintID>
```
This will print the YAML config to stdout.

## Applying a Blueprint

To create a new range from a blueprint, pass `--from-blueprint` to `range create`:

```bash
# terminal-command-local
ludus range create -r ad-lab -n "AD Lab" --from-blueprint ad-lab
ludus range deploy --range-id ad-lab
```

If the apply step fails after the range is created, the range still exists; the error includes the retry command.

To apply a blueprint to a range that already exists:

```bash
# Apply to your default range
# terminal-command-local
ludus blueprint apply ad-lab

# Apply to a specific range
# terminal-command-local
ludus blueprint apply ad-lab --target-range JD-1-range-2
```

:::warning
Applying a blueprint **overwrites** the target range configuration, and does not redeploy the range for you. Please run `ludus range deploy --range-id <your range>` to deploy the applied configuration.
:::

If testing mode is enabled on the target range, apply will fail. Use `--force` to override it.

## Sharing Blueprints

### Share with Users

```bash
# terminal-command-local
ludus blueprint share user <blueprintID> <userID...>
```

```bash
# Single user
ludus blueprint share user ad-lab JD-1

# Multiple users (space-separated or comma-separated)
ludus blueprint share user ad-lab JD-1 AS-2 BW-3
ludus blueprint share user ad-lab JD-1,AS-2,BW-3
```

### Share with Groups

```bash
# terminal-command-local
ludus blueprint share group <blueprintID> <groupName...>
```

```bash
# Single group
ludus blueprint share group ad-lab sec-team

# Multiple groups
ludus blueprint share group ad-lab sec-team,dev-team
```

### Unshare

```bash
# terminal-command-local
ludus blueprint unshare user <blueprintID> <userID...>
# terminal-command-local
ludus blueprint unshare group <blueprintID> <groupName...>
```

### View Access

```
# terminal-command-local
ludus blueprint access users <blueprintID>
```

```
+---------+---------------+---------------+-----------+
| USERID  |     NAME      |    ACCESS     |  GROUPS   |
+---------+---------------+---------------+-----------+
| JD-1    | John Doe      | owner         | -         |
| AS-2    | Alice Smith   | direct, group | sec-team  |
| BW-3    | Bob Williams  | group         | sec-team  |
+---------+---------------+---------------+-----------+
```

```
# terminal-command-local
ludus blueprint access groups <blueprintID>
```

```
+-----------+----------+---------+
|   GROUP   | MANAGERS | MEMBERS |
+-----------+----------+---------+
| sec-team  | AS-2     | BW-3    |
+-----------+----------+---------+
```

## Deleting a Blueprint

```
# terminal-command-local
ludus blueprint rm <blueprintID>
```

Only the owner or an admin can delete a blueprint. You will be prompted for confirmation, unless you use `--no-prompt` to skip it.

## Permissions
You may only see blueprints you have access to. Admins have access to all blueprints. When a blueprint is shared with a group, all members and managers of that group gain access to the blueprint.

| Action | Admin | Owner | Group Manager | Shared User | Group Member |
|--------|:-----:|:-----:|:-------------:|:-----------:|:------------:|
| View / list / apply / copy | ✅ | ✅ | ✅ | ✅ | ✅ |
| Edit config | ✅ | ✅ | ❌ | ❌ | ❌ |
| Share / unshare | ✅ | ✅ | ✅* | ❌ | ❌ |
| Delete | ✅ | ✅ | ❌ | ❌ | ❌ |

\* Group managers can only share with groups they manage, or with users in their groups.

## CLI Reference

| Command | Description |
|---------|-------------|
| `ludus blueprint list` | List all accessible blueprints; `--tag <tag>` filters |
| `ludus blueprint create` | Create from scratch, a range, an existing blueprint, or an exported tarball |
| `ludus blueprint info <id>` | Show metadata and dependency status |
| `ludus blueprint apply <id>` | Apply a blueprint to a range |
| `ludus blueprint install <id>` | (Re-)install a blueprint's role dependencies |
| `ludus blueprint update <id>` | Update name, description, version, tags, etc. |
| `ludus blueprint config get <id>` | Print the YAML config |
| `ludus blueprint config edit <id>` | Edit the YAML config (built-in TUI or `$LUDUS_EDITOR`) |
| `ludus blueprint config set <id> -f <file>` | Replace the YAML config from a file |
| `ludus blueprint export <id>` | Export the bundle as a `.tar.gz` |
| `ludus blueprint access users <id>` | List users with access |
| `ludus blueprint access groups <id>` | List groups with access |
| `ludus blueprint share user <id> <userID...>` | Share with users |
| `ludus blueprint share group <id> <groupName...>` | Share with groups |
| `ludus blueprint unshare user <id> <userID...>` | Unshare from users |
| `ludus blueprint unshare group <id> <groupName...>` | Unshare from groups |
| `ludus blueprint rm <id>` | Delete a blueprint |

## Directory Structure

Each blueprint is stored on disk as a small directory holding its range config and dependency manifest. Applying it always produces the same range.

```
<ludus_install_path>/blueprints/<record-id>/
├── blueprint.yml      # display metadata (imported blueprints only)
├── range-config.yml   # the range config
├── requirements.yml   # galaxy roles and collections, and license-gated roles
└── thumbnail.png      # optional display thumbnail
```

### Export and import

Export a blueprint and move it to another instance:

```bash
# terminal-command-local
ludus blueprint export my-lab -o my-lab.tar.gz
```

Import it elsewhere:

```bash
# terminal-command-local
ludus blueprint create --import my-lab.tar.gz
ludus blueprint apply my-lab
ludus range deploy
```

Blueprint export is a **config snapshot**, not a full installable artifact. The tarball carries `blueprint.yml` (display metadata), `range-config.yml`, `requirements.yml`, and the blueprint's thumbnail if one is set. Galaxy role and collection pins in `requirements.yml` are re-resolved on the importer's instance via ansible-galaxy. Custom local roles and Packer templates do not travel with a single-blueprint export — if you need to distribute those alongside a blueprint, package them as a [source](./sources.md) instead.

### Subscription roles

Subscription role bytes are never carried; only the names declared under `subscription_roles:` in `requirements.yml`. At deploy time, an instance without a valid license (or whose catalog doesn't cover one of the names) will fail when ansible-galaxy tries to install the role. See the [Private Role Catalog](../enterprise/subscription-roles/roles-overview.md) for the list of subscription roles.

### Recovering from a failed install

Dependencies install automatically when a blueprint is created, imported, or its source is synced. (Copies inherit the source bundle's installed deps.) If an install fails partway, retry with:

```bash
# terminal-command-local
ludus blueprint install <id>
```
