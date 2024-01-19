---
sidebar_position: 1
---

# Install Ludus

:::warning

Ludus will completely take over the machine! It should not be used for other tasks (i.e. Docker).

:::

Ludus can **only** be installed on a host that meets the following requirements:

- Debian 12
- Supports virtualization - vmx or svm in /proc/cpuinfo (nested virtualization is supported, but has a performance penalty)
- Has at least 32 GB of RAM
- Has at least 200 GB of HDD space
- Root access
- Internet access

To install ludus, copy the ludus-server binary to the machine and run it as root. It will copy all files into /opt/ludus and print the configuration
values used during install. 

```
local:~$ scp ludus-server user@debian12:
local:~$ ssh user@debian12
user@debian12:~$ chmod +x ludus-server
user@debian12:~$ sudo ./ludus-server

Ludus server v0.9.2+e35d94d
No config.yml found. Generating a config at /home/debian/config.yml. Please check that it contains the correct values.
Extracting ludus to /opt/ludus...
Downloading proxmox.py...
Proxmox.py downloaded successfully
Ludus files extracted successfully

!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!!! Only run Ludus install on a clean Debian 12 machine that will be dedicated to Ludus !!!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
Using the following config:
---
proxmox_node: ludus
proxmox_interface: ens18
proxmox_local_ip: 203.0.113.136
proxmox_public_ip: 203.0.113.136
proxmox_gateway: 203.0.113.254
proxmox_netmask: 255.255.255.0
proxmox_vm_storage_pool: local
proxmox_vm_storage_format: qcow2
proxmox_iso_storage_pool: local


Ludus install will cause the machine to reboot twice. Install will continue
automatically after each reboot. Check the progress of the install by running:
'ludus-install-status' from a root shell.

Do you want to continue? (y/N):
y
...
```

Once the install starts, ansible logs will be printed to the screen until the first reboot.

After the reboot, the install will continue automatically. To monitor its progress, ssh into
the machine, elevate to root, and run `ludus-install-status`.

## Customizing the install

In almost all cases, the default values generated during install are correct. However, if the auto-generated
config contains incorrect values, you can manually create a config called `config.yml` in the same
directory as the ludus-server binary and those values will be used when run.

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
```