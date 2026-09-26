---
sidebar_position: 7
title: "📥 Migrate to LXC"
---

# Migrating a host-installed Ludus server into an LXC

The normal installer detects a legacy host installation and starts the LXC
migration instead of running the old in-place server update. Do **not** run the
LXC server binary with `--update` on the old host: it refuses that operation
before stopping services. After migration, ordinary updates run inside the LXC.

## Before migration

- Back up the Proxmox host and Ludus data. The installer stages the LXC while the
  existing API and WireGuard service remain available, then performs a short
  final state transfer.
- Use the installer and signed appliance from the same LXC release. For a local
  development archive, follow the build and verification instructions in
  `DEVELOPERS.md`; do not substitute an older public installer.
- Finish template builds and range deployments first. Migration refuses to
  interrupt running Ansible or Packer workers.
- Migration uses the host's `conntrack` package to discard stale WireGuard
  connection mappings without flushing unrelated connections. The online
  installer adds it when missing; install it before an air-gapped migration.
- Apply or discard pending Proxmox SDN changes before starting. Migration must
  not apply unrelated network edits.

The automated same-host path supports a **single-node Proxmox installation**
with the standard legacy IPv4 NAT network (`192.0.2.254/24` on `vmbr1000` or
`ludusnat`) and range bridges. Clustered hosts, incompatible SDN zones, custom
NAT VLANs, WireGuard hooks, and unsupported credential references are rejected
during preflight; resolve these explicitly rather than bypassing the checks.

If Enterprise is installed, supply the matching target-release plugin with
`--enterprise-plugin`. The legacy host's shared library is not copied into the
new runtime.

## Migrate on the existing Proxmox host

Run the normal installer on the existing Proxmox host:

```bash
curl --proto '=https' --tlsv1.2 -sSf https://ludus.cloud/install | bash
```

After updating the local client, the installer reports the detected 2.x server
and asks whether to upgrade it. It requests `sudo` once for the server portion.
For unattended automation, use `--no-prompt`; detection still selects the
migration path. `--migrate-host` remains available when invoking a downloaded
installer directly.

Use `--version` to select a release, and the normal installer options to choose
an unused `--vmid`, container storage, or a local `--template-file`.

Omit `--bridge`, `--ip`, and `--gw` to create a private management bridge
automatically. Alternatively, supply all three with an unused static address.
The private management address is separate from the existing client endpoint;
clients do not need a new server address.

The installer:

1. Validates the existing installation and saves reversible host/network state
   under `/var/lib/ludus-migration/upgrade.*`.
2. Downloads, creates, and configures the candidate LXC while the 2.x API and
   WireGuard service remain available.
3. Creates a consistent online database snapshot and imports a versioned,
   integrity-checked state archive into the staged LXC while 2.x remains live.
   It preserves the database, user/range files, encryption settings, supported
   service secrets, private-source credentials, TLS material, and WireGuard
   keys and peers. Legacy executables and playbooks are not imported. Static
   files are fingerprinted so a concurrent configuration change aborts instead
   of being lost.
4. Redirects new API connections to a byte relay, stops the old API, and copies
   only the final database, WireGuard state, and DHCP leases. Requests accepted
   during this short transfer wait for the new service and retain end-to-end TLS
   with the original certificate.
5. Moves existing VM NICs to the appropriate SDN VNets without cloning or
   renumbering VMs. It preserves MAC addresses and range VLANs, and converts
   the legacy NAT NIC's native VLAN 1 to the untagged NAT VNet.
6. Preserves `192.0.2.254` inside the container for existing routers' gateway
   and DNS settings. The Proxmox-side NAT gateway moves to `192.0.2.49`.
   Existing ranges retain their original router template and VM name rather
   than adopting the new-install Debian 13 default.
7. Starts the unchanged WireGuard identity in the LXC and switches forwarding
   before stopping the old listener. It resets only the old connection mappings.
   Existing peers re-handshake automatically without new keys, profiles, or
   endpoints.
8. Forwards the old host's API endpoint to the LXC after it is healthy.
9. Checks both services and the original API endpoint before committing.
   Failures before commit trigger automatic host/network rollback.

The admin API remains localhost-only **inside the LXC** unless it was explicitly
exposed before migration. Run local administrative operations through
`pct exec VMID -- ...`; the old host's localhost admin endpoint is not retained
by default. An explicitly exposed admin endpoint is forwarded.

## Verify and recover

Confirm that your existing API key still works at the original server address,
your unchanged WireGuard client remains connected, and existing range VMs are
reachable. The endpoint and client configuration do not change. WireGuard may
pause traffic briefly while peers perform their normal automatic re-handshake;
no user action or configuration regeneration is required.

Check `/opt/ludus/install/install.log` inside the LXC for bootstrap failures.
Retain the migration directory and backup until validation is complete; the
committed forwarding service uses the saved network metadata.

If automatic rollback reports an error, the saved helper can be rerun as root:

```bash
bash /var/lib/ludus-migration/upgrade.XXXXXXXX/migrate-host.sh \
  --rollback /var/lib/ludus-migration/upgrade.XXXXXXXX
```

Replace `upgrade.XXXXXXXX` with the directory reported by your migration.
Rollback restores host services and network state and stops the candidate LXC;
it does not destroy its disk. Do not roll back a committed migration after users
have changed data in the LXC without first reconciling those changes.

## Importing onto another host

`--import-db` accepts the versioned state archive produced by this release's
`ludus-server --export-state PATH`, with the old Ludus services stopped. A raw
tarball containing only `db`, `config.yml`, and `/etc/wireguard` is no longer a
supported migration archive.

State import does not move VM disks or perform the old host's network cutover.
Plan those operations and preserve the external WireGuard endpoint separately;
use `--migrate-host` for the automated same-host upgrade described above.
