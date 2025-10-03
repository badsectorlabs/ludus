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

4. Boot the VM and install Debian 12/13.

## Install

1. Follow [Install Ludus](../quick-start/install-ludus)
