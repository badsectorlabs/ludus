# Installing and updating betas

`install.sh` selects the newest GitLab tag that does not contain `-beta` by
default. `--version` explicitly pins both the client and the LXC release.
Use `install-beta.sh` for tags containing `-beta`:

```bash
curl -fsSL https://beta-files.ludus.cloud/install-beta.sh -o install-beta.sh
bash ./install-beta.sh --no-prompt --version 2.4.0-beta.1
```

Omit `--version` to use the beta channel's `latest.txt`. The downloaded entry
point retrieves the shared installer from that same version, not from a moving
branch. In a source checkout it uses the adjacent `install.sh`.

- On a client workstation, this installs the checksum-verified beta client.
- On a fresh Proxmox host, it uses the standard 2.4 LXC installation path.
- On a legacy host installation, it selects the host-to-LXC migration below.
  `--migrate-host` can also select that path explicitly.
- On a host with `/etc/ludus-lxc.json`, it verifies the beta server download and
  runs `--update` inside the recorded container. It does not recreate the LXC,
  database, or range VMs. The host metadata version changes only after success.
  `--server-only` skips the client installation.

Existing-LXC beta updates are online operations and reject `--template-file`.
Fresh installs and migrations retain local-template support for CI/development,
but full air-gap installation now requires the separate `install-offline.sh`.
Both public entry points reject `--airgapped`, `--iso-directory`, and
`--license-file`. Wait for deployments and template builds to finish, take
backups, and allow a maintenance window before updating. Running the standard
installer against an existing LXC still leaves the server unchanged.

`R2_BUCKET_BASE_URL` overrides the beta binary/installer origin (default:
`https://beta-files.ludus.cloud`); `LUDUS_R2_BASE` independently overrides the LXC
template origin (default: `https://lxc.ludus.cloud`). Prerelease documentation is
published at <https://beta.ludus.cloud/docs/intro>.

# Offline installation

Transfer `install-offline.sh` together with the **same release's `install.sh`**
and the signed local media. Run `install-offline.sh` directly; there is no
`--airgapped` switch. The wrapper reuses the shared LXC installation/migration
engine without downloading its companion script, discovering a public release,
or installing a client. It requires a local LXC archive, all pinned ISOs, and an
explicit shared ISO storage pool. Existing-LXC updates are not supported.

The public installer downloads do not include the offline entry point. This is
a distribution split, not a new license check or a restriction on source access.
See [the offline procedure](docs/docs/deployment-options/proxmox-lxc.md#install-on-an-offline-proxmox-cluster)
for signed-media, license, CA, and internal-mirror requirements.

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
template build to finish first. `--no-dep-update` skips **all** controller Python,
collection, role, and Packer dependency updates, not just the plugin refresh.
Do not use it to move an older dependency set to this release.
Local role uploads create a new user's Ansible temporary directory on first
use, rather than requiring a prior Ansible run.

Match the VM disk format to the selected storage backend. In particular,
`--vm-storage local-lvm` requires `--vm-storage-format raw`; the default `qcow2`
format is not supported by LVM-thin storage.

## Ansible 2.21.4

The appliance and online updater use **ansible-core 2.21.4**, not the `ansible`
community-package version. Python dependencies come from
`ludus-server/lxc/python-requirements.txt`; collection and role pins come from
`ludus-server/ansible/requirements.yml`. The managed controller environment is
`/opt/ludus/venv`, using Debian 13's Python 3.13. Core 2.21 requires Python
3.12–3.14 on the controller and 3.9–3.14 on managed Linux nodes; see the
[upstream support matrix](https://docs.ansible.com/projects/ansible/latest/reference_appendices/release_and_maintenance.html#ansible-core-support-matrix).

Online updates install the exact Python requirements, check the environment,
then stage and validate the pinned collections and roles before replacing their
global copies. Copies of those same shipped identities under users' `.ansible`
directories are removed so they cannot shadow the release pins. Unrelated
user-installed roles and collections are retained. Back up any local changes to
shipped dependencies before updating. Dependency failures after replacement has
started leave the API services stopped: correct the reported error and rerun the
update, or restore the LXC backup. Do not treat a binary-only rollback as a
dependency rollback.

Proxmox tasks use `community.proxmox` 2.0.0 with `proxmoxer` 2.3.0. VM deployment
resolves existing destination names before cloning: the new module's clone mode
does not preserve name-based idempotence by itself. Repeated deployments reuse
the existing VMID instead of creating duplicate VMs.
Windows timezone tasks use `ansible.windows.win_timezone`. Galaxy collections
are installed unmodified. Ludus's `json_query` calls use field lookups and string
filtering, which work with the pinned provider. For numeric operations on Ansible
values, use built-in filters such as `sum`, `sort`, and `map(attribute=...)`;
numeric `json_query` functions can reject Ansible-tagged integers and floats.
`geerlingguy.packer` 1.0.2 and `ansible-thoteam.nexus3-oss` v2.5.2 remain pinned
because they are still their latest releases.

Fresh offline appliances contain the controller wheels, collections, and roles.
This does **not** add offline update support: existing-LXC offline updates remain
unsupported.

The controller upgrade does not repair old VM images or existing guests.
Debian 10, Ubuntu 20.04, and Rocky 8 need a supported guest interpreter **and**
their package-manager bindings. Update the first-party source and rebuild those
templates, or bootstrap retained guests over SSH before Ansible gathers facts.
Installing the distro's old `python3-apt` or `python3-dnf` package does not make
its bindings usable by a private Python 3.11. See the
[first-party template runtime guidance](https://github.com/badsectorlabs/ludus-source-bsl/blob/main/templates/README.md).

# Upgrading from Ludus < 2.0.0
For these older installations, complete the following database upgrade using a
**host-based release** before following the LXC migration procedure above.


1. Upgrade Ludus normally (`./ludus-server --update`)
2. Wait for the database migration to complete. You can watch the status with `journalctl -u ludus-admin -n 20 -f`
