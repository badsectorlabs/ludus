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

You can then browse to the pocketbase admin page at `https://<Ludus IP>:8080/admin`

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

## Get the total resources for a range config

```
yq '{"Total VMs": (.ludus.[] as $vm_item ireduce (0; . + 1)),"Total CPUs": (.ludus.[] as $vm_item ireduce (0; . + $vm_item.cpus)),"Total RAM (GB)": (.ludus.[] as $vm_item ireduce (0; . + $vm_item.ram_gb))}' /opt/ludus/range/<rangeID>/range-config.yml
```