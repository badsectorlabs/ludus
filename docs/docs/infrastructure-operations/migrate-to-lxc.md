---
sidebar_position: 7
title: "📥 Migrate to LXC"
---

# Migrating an existing Ludus install into the LXC

Automated migration is not provided, but you can carry your database, config,
and WireGuard keys into a fresh LXC install.

## On the old host

```bash
systemctl stop ludus ludus-admin
tar czf /root/ludus-backup.tar.gz \
  /opt/ludus/db \
  /opt/ludus/config.yml \
  /etc/wireguard
```

Copy `/root/ludus-backup.tar.gz` to the new Proxmox host.

## On the new host

```bash
curl -fsSL https://ludus.cloud/install.sh | bash -s -- \
  --import-db /root/ludus-backup.tar.gz
```

The installer will:

1. Create the Ludus LXC.
2. Unpack your DB and WireGuard keys into it before first boot.
3. On first boot, Ludus creates SDN VNets (`r1`, `r2`, …) for every range
   found in the DB and warns about any Proxmox users referenced in the DB
   that don't exist on the new cluster.

## After import

- Existing WireGuard client configs keep working **only if** `/etc/wireguard`
  was included in the backup. Otherwise re-issue with `ludus user wg regen --all`.
- Legacy `config.yml` keys (`proxmox_url`, `proxmox_public_ip`) are
  automatically migrated; check `/opt/ludus/install/install.log` for
  deprecation warnings.
- Existing `vmbr1XXX` bridges on the old host are **not** used. Ranges now
  attach to SDN VNets named `rN`. Re-deploy each range
  (`ludus range deploy`) to move VMs onto the new networks.
