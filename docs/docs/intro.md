---
sidebar_position: 1
---

# Ludus Introduction

<!-- <img src="img/why.png" alt="A tweet from @hotnops saying '90 percent of security research is getting test environments setup properly'" width="600"/> -->

![A tweet from @hotnops saying '90 percent of security research is getting test environments setup properly'](/img/intro/why.png)


> https://twitter.com/hotnops/status/1453408941073911811


Ludus is a system to build easy to use cyber environments, or "ranges" for testing and development.

Built on [Proxmox](https://www.proxmox.com/en/), Ludus enables advanced automation while still allowing easy manual modifications or setup of virtual machines and networks.

Ludus is implemented as a server that runs [Packer](https://www.packer.io/) and [Ansible](https://www.ansible.com/) to create templates and deploy complex cyber environments from a single configuration file. Ludus is accessed via the [Ludus CLI](./cli) (client) or the Proxmox web interface. Normal users should not need to access Ludus via SSH.

As a user, you can *always* make manual changes or set up manual environments via Proxmox instead of/in addition to Ludus managed VMs/networks.
Ludus is an automation overlay on top of Proxmox, not a 100% replacement for manual configuration - just most of the common setup tasks!

## Getting Started

Ludus is *only* compatible with virtualization capable Debian 12. No other environments or hosting solutions will be supported.

### Requirements
- A host capable of virtualization running Debian 12 (e.g. "bare metal", Azure Dv3 and Ev3, AWS *.metal, [supported Google Cloud VMs](https://cloud.google.com/compute/docs/instances/nested-virtualization/managing-constraint))
- at least 32GB RAM per user/range that will be deployed
- at least 200GB storage for initial templates and at least 50GB per user/range that will be deployed (large, fast NVMe drives recommended)
- no more than 150 users per Ludus host
- If you want to access Ludus across the internet
    - 1 public IP address
    - the ability to allow in arbitrary ports (i.e. port forwarding or control of the cloud firewall)

### User Quick Start

If you are a Ludus user and your Ludus server has already been installed, getting started is easy!

1. Get your API key and WireGuard config from your Ludus admin.
2. Download or compile the Ludus client
3. Import and connect the WireGuard VPN
4. Run `ludus apikey` and give it your API key
5. Use the **[Ludus CLI](./cli)** to manage your range!