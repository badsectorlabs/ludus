# Moving a host-installed server into an LXC

Use the target LXC release's `install.sh --migrate-host`, not
`ludus-server --update` on the Proxmox host. The automated path supports a
single-node installation and preserves existing users, ranges, VM identities,
and client endpoints. It requires a maintenance window and host backups.

See [the migration procedure](docs/docs/infrastructure-operations/migrate-to-lxc.md)
for prerequisites, supported topology, verification, and rollback. For local
branch validation, use the baseline checkout and build-only flow in
[DEVELOPERS.md](DEVELOPERS.md).

# Upgrading from Ludus < 2.0.0
For these older installations, complete the following database upgrade using a
**host-based release** before following the LXC migration procedure above.


1. Upgrade Ludus normally (`./ludus-server --update`)
2. Wait for the database migration to complete. You can watch the status with `journalctl -u ludus-admin -n 20 -f`
