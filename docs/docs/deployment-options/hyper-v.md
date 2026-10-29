---
title: Hyper-V
---

# Hyper-V

## VM Setup

1. Create a Generation 2 VM with typical settings.


![Hyper-V Generation 2 settings](/img/deployment/hyper-v-generation-2.png)

2. Before booting the VM for the first time, disable `Checkpoints` in VM Settings.


![Hyper-V Disable Checkpoints](/img/deployment/hyper-v-disable-checkpoints.png)

3. If your host in a server edition of windows, run the following powershell

```powershell
Set-VMProcessor -VMName <Ludus VM Name> -ExposeVirtualizationExtensions $true
```

4. The Ludus container uses its own MAC address on the Proxmox bridge, so enable MAC address spoofing on the VM's network adapter:

```powershell
Set-VMNetworkAdapter -VMName <Ludus VM Name> -MacAddressSpoofing On
```

Alternatively, put the container on a private NAT bridge as described in [Hosts with a single public address](./proxmox-lxc.md#hosts-with-a-single-public-address).

5. Boot the VM from the [Proxmox VE ISO](https://www.proxmox.com/en/downloads/proxmox-virtual-environment/iso) and install Proxmox VE 8 or 9.

## Install

1. Follow [Install Ludus](../quick-start/install-ludus) on the Proxmox VE VM.
