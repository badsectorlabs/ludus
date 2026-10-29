---
title: VMware Fusion
---

# VMware Fusion

:::warning

Using a type 2 hypervisor is not recommended. However, using the settings below allow for acceptable performance.

:::

:::danger

Apple Silicon macs (M1, M2, M3, etc.) are not supported!

:::

## VM Setup

Create a VM for Proxmox VE with the following settings (disk can be larger than 250GB as available). Choose Debian 12 or 13 64-bit as the guest operating system type:

![VMWare Fusion CPU/RAM Options](/img/deployment/vmware-fusion-procram.png)

![VMWare Fusion Advanced Options](/img/deployment/vmware-fusion-advanced.png)

![VMWare Fusion Disk Options](/img/deployment/vmware-fusion-disk.png)

## Install

Install Proxmox VE 8 or 9 in the VM from the [Proxmox VE ISO](https://www.proxmox.com/en/downloads/proxmox-virtual-environment/iso). Once it is running, follow [Install Ludus](../quick-start/install-ludus) on the Proxmox VE VM.