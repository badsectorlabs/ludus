---
title: Uninstall Ludus
---

## LXC installations

Using an administrator key and `LUDUS_URL` pointing to the public Ludus API,
remove each user's ranges (`ludus range rm --user <USER ID>`) and then the user
(`ludus user rm -i <USER ID>`). Wait for range removal to finish before deleting
users or stopping the container. The public API forwards admin operations; do
not point the client at the Proxmox host's localhost admin port.

The installed container ID is recorded in `/etc/ludus-lxc.json` on Proxmox.
Back up any data you need before removing that container through Proxmox. For
migrated installations, also review the forwarding service and saved migration
state documented in [migration to LXC](../infrastructure-operations/migrate-to-lxc.md).
Do not apply the legacy host cleanup commands below to an LXC installation.

## Legacy host installations only

Run the following as root on a legacy (non-LXC) Ludus host to uninstall Ludus.

```
ludus users list all
# Repeat the next command for all users
ludus range rm --user <USER ID>
export LUDUS_API_KEY=$(cat /opt/ludus/install/root-api-key)
ludus users list all
# Repeat the next command for all users
ludus --url https://127.0.0.1:8081 user rm -i <USER ID>
systemctl stop ludus
systemctl stop ludus-admin
pveum group delete ludus_users
pveum group delete ludus_admins
pvesh delete /pools/SHARED
pvesh delete /pools/ADMIN # if created
rm -rf /opt/ludus
# Remove vmbr1000 (and any other vmbr1000+ interfaces) using the Proxmox GUI
```
