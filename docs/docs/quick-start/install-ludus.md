---
sidebar_position: 1
---

# Install Ludus

:::warning

Ludus will completely take over the machine! It should not be used for other tasks (i.e. Docker).

:::

:::danger

Ludus is not supported on hosts that are connected to the network via WiFi. Please connect via ethernet.

:::

Ludus can **only** be installed on a host that meets the following requirements:

- x86_64 (aka amd64 aka 64-bit "Intel") CPU with a [Passmark](https://www.cpubenchmark.net/cpu_list.php) score > 6,000
- Debian 12 or Proxmox 8 (If Proxmox, see [this page](../deployment-options/proxmox.md) for details)
- Supports virtualization - vmx or svm in /proc/cpuinfo (nested virtualization is supported, but has a performance penalty)
- Has at least 32 GB of RAM
- Has at least 200 GB of disk space (fast NVMe recommended)
- Root access
- Internet access (not via WiFi). Note: Bonded nics or other advanced networking is not supported. If you use these, you will need to console in and fix the network after install (edit `/etc/network/interfaces`), as Ludus assumes you have a single, standard interface.

Machines with lower specs than listed above may work, but are not tested/supported.

If you are installing Debian 12, the [netinst ISO](https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/) (scroll down to `debian-12.x.x-amd64-netinst.iso`) is recommended. 
At the screen below during install, uncheck `Debain desktop environment` and check `SSH server`.

![A screenshot of the Debain 12 install page with SSH Server and standard system utilities checked](/img/intro/debain-12-install.png)

To install ludus, use the installer script on a Debian 12 machine as shown below. It will extract files into /opt/ludus and walk through the configuration
values during install.

:::tip[Don't trust the binaries?]

    Ludus binaries are built in CI, but you can always [build them from source](../developers/building-from-source) yourself.

:::

```shell
# terminal-command-local
ssh user@debian12

# terminal-command-user-at-debian
su -
# Enter root password to elevate to root
# terminal-command-root-at-debian
apt update && apt install curl sudo

# All-in-one command
# terminal-command-root-at-debian
curl -s https://ludus.cloud/install | bash

# If you want to check out the install script
# terminal-command-root-at-debian
curl https://ludus.cloud/install > install.sh
# terminal-command-root-at-debian
cat install.sh
# terminal-command-root-at-debian
chmod +x install.sh
# terminal-command-root-at-debian
./install.sh
```

The `install.sh` script will install the `ludus` client, and optionally shell completions, and then prompt to install the server.
Follow the interactive installer. If you are unsure of any option, just accept the default value. The installer will start and reboot the machine.

After the reboot, the install will continue automatically. To monitor its progress, ssh into
the machine, elevate to root, and run `ludus-install-status`.

![A gif of the ssh-ing into Debian 12 and running the installer](/img/intro/ludus-install.gif)

## Customizing the install

In advanced setups `/opt/ludus/config.yml` can be modified after install to accommodate different storage pools,
ZFS, etc.


```yaml title="/opt/ludus/config.yml"
---
proxmox_node: ludus               # The proxmox node/hostname for this machine
proxmox_invalid_cert: true        # Disables certificate checking when using the Proxmox API (default true because of the self signed certificates)
proxmox_interface: ens18          # The interface this machine uses to communicate to the internet
proxmox_local_ip: 203.0.113.136   # The IP address for this interface (will be set statically)
proxmox_public_ip: 203.0.113.136  # The public IP address to reach this machine (for use in cloud/NAT environments)
proxmox_gateway: 203.0.113.254    # The gateway this machine uses to reach the internet
proxmox_netmask: 255.255.255.0    # The netmask for the proxmox_interface
proxmox_vm_storage_pool: local    # The name of the VM storage pool - can be changed after install for custom pools
proxmox_vm_storage_format: qcow2  # The VM storage format - can be changed after install (i.e. raw)
proxmox_iso_storage_pool: local   # The storage pool used to store ISOs as they are downloaded for templates - can be changed after install
ludus_nat_interface: ludus        # The name of the interface Ludus will create on the proxmox host that Ludus will use as the "WAN" for range routers
prevent_user_ansible_add: false   # Set this to true to prevent non-admin users from adding Ansible roles or collections to the server
license_key: community            # Set this to your license key if you have one, or leave as community for community edition
expose_admin_port: false          # Set this to true to expose the admin API globally
```