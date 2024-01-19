---
title: VMware Fusion
---

# VMware

:::warning

Using a type 2 hypervisor is not recommended. However, using the settings below allow for acceptable performance.

:::

## VMware Fusion

:::danger

Apple Silicon macs (M1, M2, M3, etc.) are not supported!

:::

### VM Setup

Create a Debian 12 VM with the following settings (disk can be larger than 250GB as available):

![VMWare Fusion CPU/RAM Options](/img/deployment/vmware-fusion-procram.png)

![VMWare Fusion Advanced Options](/img/deployment/vmware-fusion-advanced.png)

![VMWare Fusion Disk Options](/img/deployment/vmware-fusion-disk.png)

Once Debian 12 is installed and running, follow [Install Ludus](../Quick%20Start/install-ludus).