---
sidebar_position: 7
title: "📥 Migrate to LXC"
---

# Migrating a host-installed Ludus server into an LXC

Use `install.sh --migrate-host` from the target LXC release. Do **not** run
the LXC server binary with `--update` on the old host: it refuses that operation
before stopping services. After migration, ordinary updates run inside the LXC.

## Before migration

- Back up the Proxmox host and Ludus data. Schedule a maintenance window; API and
  WireGuard sessions are interrupted while the services move.
- Use the installer and signed appliance from the same LXC release. For a local
  development archive, follow the build and verification instructions in
  `DEVELOPERS.md`; do not substitute an older public installer.
- Finish template builds and range deployments first. Migration refuses to
  interrupt running Ansible or Packer workers.
- Install the host's `conntrack` package if it is missing. Migration needs it
  to discard stale WireGuard connection mappings without flushing unrelated
  connections. Prepare this dependency before an air-gapped migration.
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

Run the target release's installer as root:

```bash
bash ./install.sh --migrate-host
```

Use `--version` to select a release, and the normal installer options to choose
an unused `--vmid`, container storage, or a local `--template-file`.

Omit `--bridge`, `--ip`, and `--gw` to create a private management bridge
automatically. Alternatively, supply all three with an unused static address.
The private management address is separate from the existing client endpoint;
clients do not need a new server address.

The installer:

1. Validates the existing installation and saves reversible host/network state
   under `/var/lib/ludus-migration/upgrade.*`.
2. Stops the old services and exports a versioned, integrity-checked state
   archive. It preserves the database, user/range files, encryption settings,
   supported service secrets, private-source credentials, TLS material, and
   WireGuard keys and peers. Legacy executables and playbooks are not imported.
3. Creates the LXC using the new runtime, restores the saved state, and
   reconciles the Proxmox objects.
4. Moves existing VM NICs to the appropriate SDN VNets without cloning or
   renumbering VMs. It preserves MAC addresses and range VLANs, and converts
   the legacy NAT NIC's native VLAN 1 to the untagged NAT VNet.
5. Preserves `192.0.2.254` inside the container for existing routers' gateway
   and DNS settings. The Proxmox-side NAT gateway moves to `192.0.2.49`.
   Existing ranges retain their original router template and VM name rather
   than adopting the new-install Debian 13 default.
6. Forwards the old host's API and WireGuard endpoints to the LXC. It resets
   only old local WireGuard connection mappings so keepalives cannot pin
   clients to the stopped host service.
7. Checks both services and the original API endpoint before committing.
   Failures before commit trigger automatic host/network rollback.

The admin API remains localhost-only **inside the LXC** unless it was explicitly
exposed before migration. Run local administrative operations through
`pct exec VMID -- ...`; the old host's localhost admin endpoint is not retained
by default. An explicitly exposed admin endpoint is forwarded.

## Verify and recover

Confirm that your existing API key still works at the original server address,
your unchanged WireGuard client reconnects, and existing range VMs are reachable.
A restarted WireGuard server has new session state: allow clients their normal
rekey interval after either migration or rollback. Client keys and configuration
do not need regeneration.

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
