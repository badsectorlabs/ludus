---
title: Proxmox
---

# Proxmox

Ludus runs its server components in an unprivileged LXC on an existing
Proxmox VE 8 or 9 cluster. Range machines remain normal Proxmox VMs.

Follow the [Proxmox LXC installer](./proxmox-lxc.md) guide for interactive,
automated, and offline installation instructions.

:::warning

An existing Proxmox installation may contain storage, SDN, firewall, or network
configuration that conflicts with Ludus. Back up the cluster configuration and
review the installer before running it.

:::

## What the installer changes

The LXC installer:

- creates or validates the Proxmox API token used by Ludus
- creates the `ludus` SDN zone and the `ludusnat` VNet
- creates an unprivileged LXC with management and Ludus NAT interfaces
- stores the cluster endpoints and token in `/opt/ludus/config.yml` inside the
  container
- starts the Ludus API, admin API, and WireGuard services in the container

It does not install Ansible, Packer, Python packages, or Ludus services directly
on every Proxmox node. Those dependencies are part of the LXC appliance.

## Storage

Choose separate values as needed for:

- the LXC root filesystem
- range VM disks and templates
- ISO files used by Packer

The range VM and ISO stores must be available on every node where Ludus builds
or runs VMs. If you change storage after installation, update
`/opt/ludus/config.yml` inside the LXC and restart `ludus` and `ludus-admin`.

Custom storage still needs the appropriate permissions for Ludus users:

```shell
pveum acl modify /storage/<storage> -group ludus_users -roles DatastoreUser
pveum acl modify /storage/<storage> -group ludus_admins -roles PVEDatastoreAdmin
```

If VMs do not receive addresses or cannot reach their router, see
[Network troubleshooting](../troubleshooting/network.md).