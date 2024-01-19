---
sidebar_position: 10
---

# Security

The nature of Ludus, allowing users to add Ansible and Packer templates, presents security risks.
While efforts have been made to secure Ludus against malicious users, only trusted users should be granted access to Ludus.

## External Access

By default, Ludus listens on port 8080 on all interfaces. This allows users to deploy Ludus in a private network (like on a small form factor computer or laptop) and be able to access it immediately. However, if Ludus is deployed on a system with a public IP, access to port 8080 should be restricted. Ideally, a firewall should be used to disable all access to port 8080 except over WireGuard.

The following iptables command can be run on the Ludus host to restrict traffic to the Ludus server to only hosts that are connected via WireGuard.

```bash
sudo iptables -I INPUT -p tcp --dport 8080 ! -i wg0 -j DROP

# Persist the rule across reboots
sudo apt install iptables-persistent
sudo /sbin/iptables-save > /etc/iptables/rules.v4
```

You may also wish to limit access to the Proxmox web interface (tcp/8006) in the same way.

## Malicious Users

Giving users the ability to add arbitrary Ansible roles is effectively allowing for remote code execution, as a role could simply be a reverse shell executed on host `localhost` (the Ludus host).
The flexibility offered by arbitrary Ansible roles is worth the security trade off for nearly all use cases.

The Ludus server process (port 8080), is heavily sandboxed (i.e. all files outside of /opt/ludus are read only), however there are likely still methods of privilege escalation.

User's router VMs are configured to allow traffic from the Ludus host, and the user's WireGuard IP. A user could write an Ansible module to execute as `localhost` and interact with other user's router VMs.

## CPU Flaws

In order to [maximize CPU speed](https://www.phoronix.com/review/retbleed-benchmark), Ludus disables all CPU mitigations for [meltdown, spectre](https://meltdownattack.com/), [retbleed](https://en.wikipedia.org/wiki/Retbleed), [downfall](https://downfall.page/), and others. 
Ludus makes conscious tradeoffs between security and usability.
Ludus is designed for individual or team use, and not to be shared with untrusted users.

## Passwords

In order to allow admins to impersonate users (act on their behalf), user Proxmox passwords are stored in plain text and are readable by the `ludus` user. Malicious users could deploy custom Ansible roles to read other user's Proxmox credentials.

Simple passwords (often literally `password`) are used throughout Ludus virtual machines. Ludus VMs are meant for testing and not serious infrastructure deployment.
