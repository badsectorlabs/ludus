---
title: Uninstall Ludus
---

Run the following as root on the Ludus host to uninstall Ludus.

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