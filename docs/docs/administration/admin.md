---
sidebar_position: 1
title: "📝 Admin Notes"
---

# 📝 Admin Notes

## Enable the Pocketbase web UI

Enable the pocketbase UI by running the following commands on your Ludus host.

```shell-session
#terminal-command-ludus-root
set-environment LUDUS_ENABLE_SUPERADMIN=ill-be-careful
#terminal-command-ludus-root
systemctl restart ludus
```

You can then browse to the pocketbase admin page at `https://<Ludus IP>:<port>/admin` (default port: 8080)

Log in with the username `root@ludus.internal` and the password the full ROOT API key from `/opt/ludus/install/root-api-key`

## Disable the Pocketbase web UI

You can disable the pocketbase web interface by running the following commands

```shell-session
#terminal-command-ludus-root
unset-environment LUDUS_ENABLE_SUPERADMIN
#terminal-command-ludus-root
systemctl restart ludus
```

## Promoting/Demoting a user to/from admin

1. Enable the Pocketbase web UI as detailed above and log in. You can select a user in the `users` table and toggle the `isAdmin` toggle. Remember to click `Save changes`.
2. Add the user to the `ludus_admins` group in the Proxmox Web UI or run `pveum user modify <username>@pam --groups ludus_admins`

## Forcing a range out of testing mode

Enable the Pocketbase web UI as detailed above and log in. Select the `ranges` table and click on the range. Toggle the `testingEnabled` toggle. Remember to click `Save changes`.

## Log history retention

Ludus keeps the last 100 deploy/build logs per range and per user (for template builds). To change this limit:

```yaml title="/opt/ludus/config.yml"
max_log_history: 25
```

```shell-session
#terminal-command-ludus-root
systemctl restart ludus
```

## Auto shutdown (enterprise)

Ludus can automatically power off idle ranges after a configurable timeout. Set a server-wide default in the config file:

```yaml title="/opt/ludus/config.yml"
inactivity_shutdown_timeout: 4h
```

The value is a Go duration string (e.g. `4h`, `30m`, `1h30m`). Set to `0` or omit to disable. Changes are picked up automatically without restarting the services.

Individual ranges can override the server default with `ludus range auto-shutdown set --timeout <duration>`. See the [Auto Shutdown](../enterprise/auto-shutdown.md) docs for details.

## Get the total resources for a range config

```
yq '{"Total VMs": (.ludus.[] as $vm_item ireduce (0; . + 1)),"Total CPUs": (.ludus.[] as $vm_item ireduce (0; . + $vm_item.cpus)),"Total RAM (GB)": (.ludus.[] as $vm_item ireduce (0; . + $vm_item.ram_gb))}' /opt/ludus/range/<rangeID>/range-config.yml
```