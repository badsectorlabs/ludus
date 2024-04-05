---
title: Proxmox
---

# Proxmox

:::warning

Using an existing Proxmox installation may cause issues with existing customizations as it is impossible for Ludus to account for every Proxmox setup. If starting from scratch the [bare metal Debian 12 install](./bare-metal) is recommended.

:::

Existing Proxmox 8 servers can install Ludus without a reboot.

Ludus will make the following changes - **do NOT do any actions below yourself before running the binary**:

- Extract files to `/opt/ludus`
- Install the following packages to the Proxmox host: ansible, packer, dnsmasq, sshpass, curl, jq, iptables-persistent, gpg-agent, dbus, dbus-user-session, and vim
- Install the following python packages to the host: proxmoxer, requests, netaddr, pywinrm, dnspython, and jmespath
- Create the proxmox groups `ludus_users` and `ludus_admins`
- Create the proxmox pools `SHARED` and `ADMIN`
- Create a wireguard server wg0 with IP range `198.51.100.0/24`
- Create an interface 'ludus' with IP range 192.0.2.0/24 that NATs traffic
- Create user ranges with IPs in the 10.0.0.0/16 network starting at 10.2.0.0/8 and incrementing the second octet for each user
- Create user interfaces starting at `vmbr1002` incrementing for each user
- Create the pam user `ludus` and pam users for all Ludus users added
- Create the `ludus-admin` and `ludus` systemd services that listen on 127.0.0.1:8081 and 0.0.0.0:8080

## Install

1. Copy the ludus-server binary to the Proxmox host and run it as root.
2. Once the install succeeds, update the values in `/opt/ludus/config.yml` to reflect the install (proxmox may have set up an lvm-thin datastore vs the default `local`)
3. Restart the ludus processes with `systemctl restart ludus-admin` and `systemctl restart ludus`.
4. Follow the Quick start guide as normal starting at [Create a User](../Quick%20Start/create-a-user).

If you encounter networking issues like VMs not getting IP addresses or having internet access, see [this guide](../Troubleshooting/network.md).