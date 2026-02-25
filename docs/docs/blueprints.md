---
sidebar_position: 13
title: "🗺️ Blueprints"
description: "Save, share, and reuse range configurations with blueprints"
keywords: [blueprints, range, config, sharing, reuse]
---

# 🗺️ Blueprints

A blueprint is a saved range configuration that can be applied to a range to quickly configure it.

```bash
# Save your range config as a blueprint
ludus blueprint create --id ad-lab --name "AD Lab"

# Share it with your team
ludus blueprint share group ad-lab sec-team

# A teammate applies it to their range
ludus blueprint apply ad-lab

# Deploy the range
ludus range deploy
```

:::tip When to use blueprints

- **Onboarding** — give new team members a ready-to-go range config
- **Workshops** — distribute a standardized lab environment to a class
- **Iteration** — save a config before experimenting with changes
- **Standardization** — maintain approved configs that teams can self-serve

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

### From Your Default Range

```bash
# terminal-command-local
ludus blueprint create --id <blueprintID>
```

You can also provide `--name` and `--description` to set a display name and description.

### From a Different Range

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

The `ACCESS` column shows your relationship to each blueprint: `admin`, `owner`, `direct` (shared with you), or `group` (shared via a group).

## Viewing a Blueprint Config

```bash
# terminal-command-local
ludus blueprint config get <blueprintID>
```
This will print the YAML config to stdout.

## Applying a Blueprint

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
You may only see blueprints you have access. Admins have access to all blueprints. When a blueprint is shared with a group, all members and managers of that group gain access to the blueprint.

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
| `ludus blueprint list` | List all accessible blueprints |
| `ludus blueprint create` | Create a blueprint from a range or copy one |
| `ludus blueprint apply <id>` | Apply a blueprint to a range |
| `ludus blueprint config get <id>` | View blueprint YAML config |
| `ludus blueprint access users <id>` | List users with access |
| `ludus blueprint access groups <id>` | List groups with access |
| `ludus blueprint share user <id> <userID...>` | Share with users |
| `ludus blueprint share group <id> <groupName...>` | Share with groups |
| `ludus blueprint unshare user <id> <userID...>` | Unshare from users |
| `ludus blueprint unshare group <id> <groupName...>` | Unshare from groups |
| `ludus blueprint rm <id>` | Delete a blueprint |
