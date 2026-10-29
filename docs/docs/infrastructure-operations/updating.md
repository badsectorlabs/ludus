---
sidebar_position: 6
title: "🆙 Updating"
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# 🆙 Updating Ludus

:::tip[Don't trust the binaries?]

    Ludus binaries are built in CI, but you can always [build them from source](../developers/building-from-source) yourself.

:::

## Updating a Ludus server 

Run the install script again as root on the Proxmox host and answer `y` when it offers to install or upgrade the server.

- If Ludus already runs in an LXC on the host, the installer updates the Ludus runtime inside that container. The database, `/opt/ludus/config.yml`, users, API keys, and WireGuard configurations are preserved.
- If the host still runs a host-installed Ludus 2.x server, the installer performs the one-time migration to the LXC runtime instead. The existing client address, API keys, TLS identity, and WireGuard profiles are preserved. See [Migrate to LXC](./migrate-to-lxc.md) for the migration checks and rollback procedure.

```shell
# terminal-command-local
ssh root@proxmox

# All-in-one command
# terminal-command-root-at-proxmox
curl --proto '=https' --tlsv1.2 -sSf https://ludus.cloud/install | bash

# If you want to check out the install script
# terminal-command-root-at-proxmox
curl https://ludus.cloud/install > install.sh
# terminal-command-root-at-proxmox
cat install.sh
# terminal-command-root-at-proxmox
chmod +x install.sh
# terminal-command-root-at-proxmox
./install.sh
```

:::caution

    In-place updates of an existing Ludus LXC currently use the beta release channel (`install-beta.sh`); the stable channel stops with `an existing LXC is only updated by the beta channel`. The installer installs the release you select without comparing it to the running version, so do not select an older release with `--version`.

:::

:::note

    Upgrading from Ludus 1.x? See the [Upgrading from Ludus 1.x](../upgrading-from-v1) guide for database migration details.

:::

:::warning

    The Ludus server binary will refuse to update if any `ansible` or `packer` processes are active inside the Ludus LXC to prevent possible interruption to active range or template activity.

:::

### Server configuration defaults

Every server update replaces `/opt/ludus/ansible/server-config.yml` with the
defaults bundled in the new release. The pre-update file remains in the
timestamped backup at
`/opt/ludus/previous-versions/<timestamp>/ansible/server-config.yml`.

If the file contents differ, the updater prints a warning with the full backup
path and the destination where you can restore it. Configuration values are not
printed. Identical files and missing pre-update files do not produce a
configuration-change warning.

To retain customizations, compare the backup with the newly installed file and
merge the settings you need. Alternatively, restore the entire old file with
the command below, replacing `TIMESTAMP` with the value printed by the updater.
Review the new release defaults first: restoring the old file also restores
its old defaults.

```shell
sudo cp -- /opt/ludus/previous-versions/TIMESTAMP/ansible/server-config.yml /opt/ludus/ansible/server-config.yml
```

These paths and the restore command are inside the Ludus LXC (`pct enter <vmid>`),
not on the Proxmox host. This applies to ordinary updates; the one-time
[host-to-LXC migration](./migrate-to-lxc.md) preserves the imported defaults.

## Updating the Ludus client

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
Run the update command

```shell
#terminal-command-local
ludus update client
```

OR

The same installer script used to install the server/client will update the client on a Linux machine.

```shell
# All-in-one command
#terminal-command-local
curl -s https://ludus.cloud/install | bash

# If you want to check out the install script
#terminal-command-local
curl https://ludus.cloud/install > install.sh
#terminal-command-local
cat install.sh
#terminal-command-local
chmod +x install.sh
#terminal-command-local
./install.sh
```

  </TabItem>
  <TabItem value="macos" label="macOS">

Run the update command

```shell
#terminal-command-local
ludus update client
```

OR

The same installer script used to install the server/client will update the client on a macOS machine.


```shell
# All-in-one command
#terminal-command-local
curl -s https://ludus.cloud/install | zsh

# If you want to check out the install script
#terminal-command-local
curl https://ludus.cloud/install > install.sh
#terminal-command-local
cat install.sh
#terminal-command-local
chmod +x install.sh
#terminal-command-local
./install.sh
```
  </TabItem>
  <TabItem value="windows" label="Windows">

Run the update command

```
PS C:\> ludus update client
```

OR 
```
PS C:\> irm https://ludus.cloud/install-client.ps1 | iex
```
  </TabItem>
</Tabs>

## Updating a Ludus server manually

The server binary runs its update inside the Ludus LXC, never on the Proxmox host.

1. Download the server [release binary](https://gitlab.com/badsectorlabs/ludus/-/releases) (or build from source) of the version you wish to update to, and verify it against the release checksums.
1. Copy the Ludus server binary to the Proxmox host.
1. Push it into the Ludus LXC and run it with the `--update` flag. The installer records the LXC's VMID in `/etc/ludus-lxc.json`.

```shell
# terminal-command-local
scp ludus-server root@proxmox:
# terminal-command-local
ssh root@proxmox
# terminal-command-root-at-proxmox
VMID=$(python3 -c 'import json; print(json.load(open("/etc/ludus-lxc.json"))["vmid"])')
# terminal-command-root-at-proxmox
pct push "$VMID" ludus-server /tmp/ludus-server --perms 0700
# terminal-command-root-at-proxmox
pct exec "$VMID" -- env HOME=/root TMPDIR=/tmp LANG=C.UTF-8 LC_ALL=C.UTF-8 \
  PATH=/opt/ludus/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  /tmp/ludus-server --update
# terminal-command-root-at-proxmox
pct exec "$VMID" -- rm -f /tmp/ludus-server
```

The install script also records the running version in the `version` field of `/etc/ludus-lxc.json`. Update that field to match after a manual update, then run `ludus-install-status` to check the services.
