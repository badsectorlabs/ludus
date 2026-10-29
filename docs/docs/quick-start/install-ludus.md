---
sidebar_position: 1
---

# Install Ludus

:::warning

Ludus requires a Proxmox VE 8 or 9 host. The installer runs the Ludus server in an unprivileged LXC on that host, and range machines run as normal Proxmox VMs.
If you are starting from bare metal, a cloud VM, or another hypervisor, [install Proxmox VE first](../deployment-options/bare-metal.mdx#install-proxmox-ve), then return to this page.

:::

:::danger

Ludus is not supported on hosts that are connected to the network via WiFi. Please connect via ethernet.

:::

Ludus can **only** be installed on a host that meets the following requirements:

- x86_64 (aka amd64 aka 64-bit "Intel") CPU with a [Passmark](https://www.cpubenchmark.net/cpu_list.php) score > 6,000
- Proxmox VE 8 or 9, installed from the Proxmox VE ISO or on top of Debian (see [Bare Metal](../deployment-options/bare-metal.mdx#install-proxmox-ve)). For an existing Proxmox installation or cluster, see [Proxmox](../deployment-options/proxmox.md).
- Supports virtualization - vmx or svm in /proc/cpuinfo (nested virtualization is supported, but has a performance penalty)
- Has at least 32 GB of RAM
- Has at least 200 GB of disk space (fast NVMe recommended)
- Root access to the Proxmox host
- An unused static IPv4 address and gateway for the Ludus container on a Proxmox bridge. DHCP is not supported. If the host has only one public address, see [Hosts with a single public address](../deployment-options/proxmox-lxc.md#hosts-with-a-single-public-address).
- Internet access during installation, unless you have staged the files and internal services listed in the [offline Proxmox LXC guide](../deployment-options/proxmox-lxc.md#install-on-an-offline-proxmox-cluster).

Machines with lower specs than listed above may work, but are not tested/supported.

## Install

:::tip[Don't trust the binaries?]

    Ludus binaries are built in CI, but you can always [build them from source](../developers/building-from-source.md) yourself.

:::

Run the install script as root on the Proxmox host:

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

The `install.sh` script installs the `ludus` client and optionally shell completions for bash or zsh.
It selects the latest stable tag, ignoring tags containing `-beta`.
On an amd64 Proxmox VE 8 or 9 host, it then offers to install the Ludus server. On any other system, including macOS and Debian without Proxmox, it installs only the client.

Answer `y` to start the interactive installer. It shows the license, a settings form, and a final review; nothing is changed until you select **Install**.
The installer creates the Ludus LXC without rebooting the host. See the [LXC installer guide](../deployment-options/proxmox-lxc.md)
for each setting, non-interactive options, and cluster requirements.

When the install finishes, the summary shows the Ludus API and web UI addresses, and three commands that configure the `ludus` client with the initial admin's API key:

```shell
export LUDUS_URL=https://<container-ip>:8080
export LUDUS_API_KEY=<initial-admin-api-key>
ludus range list all
```

Store the API key securely; it is not shown again.
To check the installation later, run `ludus-install-status` as root on the Proxmox host. It runs the health check inside the Ludus LXC.

## Customizing the install

The server configuration is `/opt/ludus/config.yml` inside the Ludus LXC, not on the Proxmox host.
In advanced setups it can be modified after install to accommodate different storage pools, ZFS, etc.

Open a shell in the container from the Proxmox host, replacing `<vmid>` with the Ludus LXC's VMID:

```shell-session
#terminal-command-root-at-proxmox
pct enter <vmid>
```

To have the Ludus server processes pick up any changes to the config file, restart the Ludus services inside the container:

```shell-session
#terminal-command-ludus-root
systemctl restart ludus
#terminal-command-ludus-root
systemctl restart ludus-admin
```

The installer writes the Proxmox connection, token, storage, network, and WireGuard values. The remaining keys are optional.

```yaml title="/opt/ludus/config.yml"
---
proxmox_endpoints:                       # Proxmox API URLs for every node Ludus manages
  - https://203.0.113.10:8006
proxmox_node: pve                        # The Proxmox node where Ludus builds templates
proxmox_token_id: root@pam!ludus-lxc-100 # The API token the container uses to manage Proxmox
proxmox_token_secret: <secret>           # Written by the installer. DO NOT SHARE
proxmox_user_realm: pve                  # The Proxmox realm for Ludus user accounts
proxmox_invalid_cert: true               # Disables certificate checking when using the Proxmox API (default true because of the self signed certificates)
proxmox_vm_storage_pool: local-lvm       # The name of the VM storage pool - can be changed after install for custom pools
proxmox_vm_storage_format: raw           # The VM storage format - can be changed after install (i.e. qcow2 on file-based storage)
proxmox_iso_storage_pool: local          # The storage pool used to store ISOs as they are downloaded for templates - can be changed after install
ludus_nat_interface: ludusnat            # The SDN VNet Ludus uses as the "WAN" for range routers
ludus_nat_ip: 192.0.2.253                # The container's address on the ludusnat VNet
ludus_nat_gateway: 192.0.2.254           # The gateway range routers use on the ludusnat VNet
sdn_zone: ludus                          # The SDN Zone name Ludus will use for the `ludusnat` VNet and range VNets
wireguard_endpoint: 203.0.113.10         # The address WireGuard clients connect to
wireguard_port: 51820                    # The port WireGuard listens on
port: 8080                               # The TCP port the web UI and user-facing API listen on
admin_port: 8081                         # The TCP port the admin API listens on (localhost only by default, all interfaces if expose_admin_port is true)
expose_admin_port: false                 # Set this to true to expose the admin API globally
tls_cert_file: /opt/ludus/tls/server.crt # The API's TLS certificate
tls_key_file: /opt/ludus/tls/server.key  # The API's TLS private key
data_directory: /opt/ludus/db            # The directory to store the ludus database and some ludus data
database_encryption_key: abc12...        # The key used to encrypt data in the database. DO NOT CHANGE AFTER INSTALL
license_key: community                   # Set this to your license key if you have one, or leave as community for community edition
prevent_user_ansible_add: false          # Set this to true to prevent non-admin users from adding Ansible roles or collections to the server
airgapped_install: false                 # Set this to true on offline clusters so tasks skip Internet lookups (for example Office package version discovery)
sso_require_existing_user: true          # Require SSO email to match an existing Ludus account; false allows automatic account creation
create_default_range: true               # Create a default range for each new user, including SSO users
reserved_range_numbers:                  # An array of range numbers that should not be used by users (see networking docs for more info), example: [1,2,3,4]
cluster_mode: false                      # Override cluster detection and for cluster mode on (true) or off (false)
vxlan_tag_base: 0                        # The number that will be added to the VXLAN tag used for each range's VNet (vxlan_tag_base + range_number = Range VNet VXLAN tag)
max_log_history: 100                     # The number of log entries to keep per range and user (user logs for template builds)
```
