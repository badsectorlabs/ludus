---
sidebar_position: 6
title: "📊 Quotas"
description: "Manage per-user resource quotas for RAM, CPU, VMs, and ranges"
keywords: [quotas, limits, resources, ram, cpu, vms, ranges, enterprise]
---

# 📊 Quotas

:::note[🏛️ `Available in Ludus Enterprise`]
:::

## Overview

Quotas allow administrators to limit the resources each user can consume. This is useful for shared environments like training labs or university courses where many users share a single Ludus deployment.

Four resource types can be quota'd:

| Resource | Unit | What it limits |
|----------|------|---------------|
| RAM      | GB   | Total memory across all VMs in all of a user's ranges |
| CPU      | Cores | Total CPU cores across all VMs in all of a user's ranges |
| VMs      | Count | Total number of VMs across all of a user's ranges |
| Ranges   | Count | Number of ranges a user can create |

## How Quotas Are Resolved

Quotas are resolved using a priority chain. The first non-zero value wins:

1. **Per-user quota** — set explicitly for a specific user
2. **Group default** — inherited from the user's group (if the user belongs to multiple groups, the most permissive value is used)
3. **System default** — configured in `/opt/ludus/config.yml`
4. **Unlimited** — if none of the above are set

## When Quotas Are Enforced

| Action | Behavior |
|--------|----------|
| `ludus range config set` | **Warns** if the config would exceed quotas, but saves the config |
| `ludus range deploy` | **Blocks** if deploying would exceed quotas |
| `ludus range create` | **Blocks** if the user has reached their range quota |

The config-set warning allows users to plan ahead — they may intend to destroy another range before deploying.

## Viewing Quotas

### View Your Own Quotas

```shell-session
#terminal-command-local
ludus quotas view
```

```
+-------------+------+-----------+-----------+
|  RESOURCE   | USED |   LIMIT   | AVAILABLE |
+-------------+------+-----------+-----------+
| RAM (GB)    |   24 |        64 |        40 |
| CPU (cores) |   12 |        16 |         4 |
| VMs         |    5 |        10 |         5 |
| Ranges      |    2 |         3 |         1 |
+-------------+------+-----------+-----------+
```

### View All Users (Admin)

```shell-session
#terminal-command-local
ludus quotas view users
```

```
+------+---------------+----------+-----------+-----------+---------+
| USER |     NAME      | RAM (GB) |    CPU    |    VMS    | RANGES  |
+------+---------------+----------+-----------+-----------+---------+
| sa   | suibhne admin |       49 |        33 |        11 |       6 |
| su   | suibhne user  | 6/64 (U) | 6/16 (U)  | 2/10 (G) | 2/3 (G) |
| eh   | erik hunstad  | 0/64 (U) | 0/16 (U)  | -         |       1 |
+------+---------------+----------+-----------+-----------+---------+
(U) = user quota, (G) = group default, (S) = system default
```

Cells show `used/limit (source)`. A `-` means unlimited with no usage.

### View Group Defaults (Admin)

```shell-session
#terminal-command-local
ludus quotas view groups
```

```
+-----------+---------+-----+-----+--------+---------+
|   GROUP   | RAM (GB)| CPU | VMS | RANGES | MEMBERS |
+-----------+---------+-----+-----+--------+---------+
| students  |      32 |   8 |   5 |      2 |      30 |
| red-team  |      64 |  16 |  10 |      3 |       5 |
+-----------+---------+-----+-----+--------+---------+
```

### View System Defaults (Admin)

```shell-session
#terminal-command-local
ludus quotas view defaults
```

This shows the system-wide default quotas configured in `/opt/ludus/config.yml`.

All `view` commands support `--json` for machine-readable output.

## Setting Quotas

### Per-User Quotas (Admin)

Set quotas for one user:

```shell-session
#terminal-command-local
ludus quotas user set -i JD --ram 64 --cpu 16 --vms 10 --ranges 3
```

Set quotas for multiple users at once:

```shell-session
#terminal-command-local
ludus quotas user set -i JD,AB,TU --ram 64 --cpu 16
```

Only the flags you provide are changed — omitted quotas are left as-is. To remove a limit, use the `reset` command:

```shell-session
#terminal-command-local
ludus quotas user reset -i JD --ram
```

To reset all quotas for a user:

```shell-session
#terminal-command-local
ludus quotas user reset -i JD
```

### Group Default Quotas (Admin)

Group defaults apply to all members who don't have explicit per-user quotas. This is the recommended way to manage quotas for large groups of users (e.g., students in a course).

```shell-session
#terminal-command-local
ludus quotas group set -g students --ram 32 --cpu 8 --vms 5 --ranges 2
```

To set up quotas for a new course:

```shell-session
#terminal-command-local
ludus groups create fall-2026-cs450 --description "CS450 Fall 2026"
ludus quotas group set -g fall-2026-cs450 --ram 32 --cpu 8 --vms 5 --ranges 2
ludus groups add user student1,student2,student3 fall-2026-cs450
```

All students in the group automatically inherit the quota limits.

### System-Wide Defaults

System defaults are the fallback when neither a per-user nor group default quota is set. Configure them in `/opt/ludus/config.yml`:

```yaml title="/opt/ludus/config.yml"
default_quota_ram: 64
default_quota_cpu: 16
default_quota_vms: 10
default_quota_ranges: 3
```

Restart the Ludus service after changing the config:

```shell-session
#terminal-command-ludus-root
systemctl restart ludus
```

## Command Reference

| Command | Description |
|---------|-------------|
| `ludus quotas view` | View your own quota status |
| `ludus quotas view users` | View all users' quotas (admin) |
| `ludus quotas view groups` | View all groups' default quotas (admin) |
| `ludus quotas view defaults` | View system-wide defaults (admin) |
| `ludus quotas user set -i <userID(s)> [flags]` | Set per-user quotas (admin, supports comma-separated IDs) |
| `ludus quotas user reset -i <userID(s)> [flags]` | Remove per-user quotas (admin, supports comma-separated IDs) |
| `ludus quotas group set -g <group> [flags]` | Set group default quotas (admin) |
| `ludus quotas group reset -g <group> [flags]` | Remove group default quotas (admin) |

### Flags for `set`

| Flag | Description |
|------|-------------|
| `--ram <int>` | RAM limit in GB |
| `--cpu <int>` | CPU core limit |
| `--vms <int>` | VM count limit |
| `--ranges <int>` | Range count limit |

### Flags for `reset`

| Flag | Description |
|------|-------------|
| `--ram` | Remove RAM limit |
| `--cpu` | Remove CPU limit |
| `--vms` | Remove VMs limit |
| `--ranges` | Remove ranges limit |

Omit all flags to reset every quota at once.
