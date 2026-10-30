---
sidebar_position: 1
title: "🏛️ Pro/Enterprise Overview"
---

# 🏛️ Ludus Pro and Enterprise Overview

While Ludus is free and open source, Ludus Pro or Enterprise are paid licenses that provides additional features and support.


| Feature | Community | Pro | Enterprise |
| --- | :---: | :---: | :---: |
| Easy one command install | ✅ | ✅ | ✅ |
| Automated template builds | ✅ | ✅ | ✅ |
| Chocolatey package manager support | ✅ | ✅ | ✅ |
| Ansible role management | ✅ | ✅ | ✅ |
| Command line client | ✅ | ✅ | ✅ |
| Fully documented API | ✅ | ✅ | ✅ |
| Up to 255 VLANs per range | ✅ | ✅ | ✅ |
| Cluster support | ✅ | ✅ | ✅ |
| Range Blueprints | ✅ | ✅ | ✅ |
| Support | Community | Dedicated Discord Channel | SLA backed support |
| Roles on the router | ❌ | ✅ | ✅ |
| [Private Role Catalog](./subscription-roles/roles-overview.md) | ❌ | Pro Roles | Pro + Enterprise Roles |
| Web Interface | ❌ | ✅ | ✅ |
| [Outbound WireGuard](./outbound-wireguard.md) | ❌ | ❌ | ✅ |
| Inbound WireGuard Server per range | ❌ | ❌ | ✅ |
| CTFd integration | ❌ | ❌ | ✅ |
| [Windows Licensing](./kms.md) | ❌ | ❌ | ✅ |
| [Quotas](./quotas.md) | ❌ | ❌ | ✅ |
| [Auto Shutdown](./auto-shutdown.md) | ❌ | ❌ | ✅ |
| [Anti-Sandbox](./anti-sandbox.md) | ❌ | ❌ | 🔌 Add-on |
| [Arbitrary credential support](./ssh-key-authentication.md) | ❌ | ❌ | ✅ |



Ludus Pro and Enterprise directly supports the development of the free and open source core Ludus product that helps thousands of cybersecurity professionals around the world every day.

## How to get a license key

Ludus Pro is available for self-service checkout at [ludus.cloud](https://ludus.cloud/#pricing).

To enquire about Ludus Enterprise licensing for your organization, please [contact us](<mailto:ludus-support@badsectorlabs.com?subject=I'm interested in Ludus Enterprise>).



# 🪪 License Key

The Ludus Pro and Enterprise license keys are a unique string that is used to activate the Ludus Pro or Enterprise features.

Your license key will look like this:

```
46C1CA-B11C52-80E9E7-19E436-FDDG1B-V3
```

## How use the license key

Once you have a license key, you can activate Ludus Pro/Enterprise by setting the `license_key` key in the Ludus config file (or during install).

```yaml
license_key: 46C1CA-B11C52-80E9E7-19E436-FDDG1B-V3
```

Once the license key is set, Ludus will check the license key on startup and if it is valid, it will activate the Ludus Pro/Enterprise features. With an Enterprise license, that startup check installs the Ludus Enterprise plugin if it is not installed yet. This happens once: Ludus never updates the Enterprise plugin, or any other plugin, on its own. When a newer version is available, **Resources → Plugins → Official** shows the update and an administrator selects **Update** to install it; the new version starts without restarting Ludus. Pro, Enterprise, and add-on plugins (such as [Anti-Sandbox](./anti-sandbox.md)) are installed by an administrator from the same page; see [Official plugins](../developers/rpc-plugins.md#official-plugins).

You can manually restart the Ludus services to have the license key check run again.

```shell-session
#terminal-command-ludus-root
systemctl restart ludus
#terminal-command-ludus-root
systemctl restart ludus-admin
```
