---
sidebar_position: 1
title: "🏛️ Enterprise"
---

# 🏛️ Ludus Enterprise

While Ludus is free and open source, Ludus Enterprise is a paid service that provides additional features and support.


| Feature | Community | Enterprise |
| --- | :---: | :---:|
| Easy one command install | ✅  | ✅ |
| Automated tempalte builds | ✅ | ✅ |
| Chocolatey package manager support | ✅ | ✅ |
| Ansible role management | ✅ | ✅ |
| Command line client | ✅ | ✅ |
| Fully documented API | ✅ | ✅ |
| Up to 255 VLANs per range | ✅ | ✅ |
| Support | Community | Professional |
| Roles on the router | ❌ | ✅ |
| Inbound WireGuard Server per range | ❌ | ✅ |
| CTFd integration | ❌ | ✅ |
| [Private Role Catalog](./private-roles.md) | ❌ | ✅ |
| [Outbound WireGuard](./outbound-wireguard.md) | ❌ | ✅ |
| [Windows Licensing](./kms.md) | ❌ | ✅ |
| [Anti-Sandbox](./anti-sandbox.md) | ❌ | 🔌 Add-on |
| Arbitrary credential support | ❌ | 🚧 In development |   
| Web Interface | ❌ | 🚧 In development |

Ludus Enterprise directly supports the development of the free and open source core Ludus product that helps thousands of cybersecurity professionals around the world every day.

To enquire about Ludus Enterprise, please [contact us](<mailto:ludus-support@badsectorlabs.com?subject=I'm interested in Ludus Enterprise>).

# 🪪 License Key

The Ludus Enterprise license key is a unique string that is used to activate the Ludus Enterprise features.

Your license key will look like this:

```
46C1CA-B11C52-80E9E7-19E436-FDDG1B-V3
```

## How to get a license key

To enquire about Ludus Enterprise licensing for your organization, please [contact us](<mailto:ludus-support@badsectorlabs.com?subject=I'm interested in Ludus Enterprise>).

## How use the license key

Once you have a license key, you can activate Ludus Enterprise by setting the `license_key` key in the Ludus config file (or during install).

```yaml
license_key: 46C1CA-B11C52-80E9E7-19E436-FDDG1B-V3
```

Once the license key is set, Ludus will check the license key on startup and if it is valid, it will activate the Ludus Enterprise features and any add-ons you are entitled to.

You can manually restart the Ludus services to have the license key check run again.

```shell-session
#terminal-command-ludus-root
systemctl restart ludus
#terminal-command-ludus-root
systemctl restart ludus-admin
```
