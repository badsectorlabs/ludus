---
sidebar_position: 2
title: "📦 Storage"
---

# 📦 Storage

By default Ludus uses the `Directory` storage type on top of an `ext4` filesystem with `qcow2` as the disk format as it is the simplest and most compatible combination of options with all types of setups.

## Proxmox Storage Types

| **Storage Type** | **Disk Format** | **Snapshots** | **Revert to any Snapshot** | **TPM Snapshots** | **Shared** | **Complexity** |
|------------------|-----------------|:-------------:|:--------------------------:|:-----------------:|:----------:|:--------------:|
| Directory        | qcow2           | ✅            | ✅                          | ✅ (PVE 9.1+)     | ❌         | 😊              |
| LVM-thin         | raw             | ✅            | ✅                          | ✅                | ❌         | 🙂              |
| ZFS              | raw             | ✅            | ❌                          | ✅                | ❌         | 😐              |
| Cephfs           | raw             | ✅            | ✅                          | ✅                | ✅         | 🤯              |

For single node Ludus deployments, `Directory` + `qcow2` is the simplest way to configure your storage disks. If you want to use "striped" (RAID0) for `LVM-thin` you must set it up using the CLI, not in the Proxmox webUI.

For clustered Ludus deployments, `Ceph` + `raw` is best, but requires at least three nodes, a dedicated high speed (10Gb/s+) network, and 1GB of RAM for every 1TB of used storage.

## Adding Storage after Install

For Proxmox storage to work with Ludus it requires the following permissions:

| **Path** | **User/Group/API Token** | **Role** | **Propagate** |
|----------|--------------------------|----------|---------------|
| `/storage/<new storage name>` | @ludus_users, @ludus_admins | LudusPacker | true |
| `/storage/<ISO storage name>` | @ludus_users, @ludus_admins | LudusISO | true |

![Adding a group permission](/img/storage/group-permissions.png)

![Add permission dialog](/img/storage/add-permission.png)

`LudusPacker` grants the storage privileges needed to allocate VM disks and upload
installation media. The additional `LudusISO` role grants `Datastore.Allocate`,
which Proxmox requires to delete temporary Packer ISOs after a build or cancellation.
Ludus bootstrap creates both roles.

Grant `LudusISO` only on `proxmox_iso_storage_pool`, not on VM-only datastores.
`Datastore.Allocate` also permits storage management and deletion of other volumes
on that datastore. Use a separate ISO datastore to keep this permission away from
VM disks; if VM and ISO storage share a datastore, the permission applies to both.

Additionally, you can modify the Ludus configuration file at `/opt/ludus/config.yml` to use your new storage. Specifically, modify these values:

```
proxmox_vm_storage_pool: <new storage name>
proxmox_vm_storage_format: <new storage disk format (qcow2|raw)>
proxmox_iso_storage_pool: local # Only change this if your new storage supports ISO files
```

Active the config by restarting the Ludus services with `systemctl restart ludus ludus-admin`