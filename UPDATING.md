# Moving a host-installed server into an LXC

Use the target LXC release's `install.sh --migrate-host`, not
`ludus-server --update` on the Proxmox host. The automated path supports a
single-node installation or a healthy cluster with a compatible existing VXLAN
zone. It preserves users, ranges, VM identities, client endpoints, the configured
source database, and global Ansible defaults. It requires a maintenance window,
host backups, and trusted root SSH access to every cluster node.

See [the migration procedure](docs/docs/infrastructure-operations/migrate-to-lxc.md)
for prerequisites, supported topology, verification, and rollback. For local
branch validation, use the baseline checkout and build-only flow in
[DEVELOPERS.md](DEVELOPERS.md).

# Deploying ranges from the LXC appliance

The appliance now packages `badsectorlabs/proxmox` Packer plugin 1.2.4, matching
the server's dependency-update floor. The earlier integration image packaged
HashiCorp 1.2.1, whose pool-scoped VM creation regression could cause template
builds to fail with HTTP 403. Do not work around this by granting users global
VM permissions.

For an online binary/dependency update inside an existing LXC, use the same
Python environment and UTF-8 locale as the API services:

```bash
env LANG=C.UTF-8 LC_ALL=C.UTF-8 \
  PATH=/opt/ludus/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  /tmp/ludus-server --update
```

Use a new server binary outside `/opt/ludus`; wait for any running deployment or
template build to finish first. `--no-dep-update` skips the plugin refresh.
Local role uploads now create a new user's Ansible temporary directory on first
use, rather than requiring a prior Ansible run.

Match the VM disk format to the selected storage backend. In particular,
`--vm-storage local-lvm` requires `--vm-storage-format raw`; the default `qcow2`
format is not supported by LVM-thin storage.

# Upgrading from Ludus < 2.0.0
For these older installations, complete the following database upgrade using a
**host-based release** before following the LXC migration procedure above.


1. Upgrade Ludus normally (`./ludus-server --update`)
2. Wait for the database migration to complete. You can watch the status with `journalctl -u ludus-admin -n 20 -f`
