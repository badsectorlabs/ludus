---
sidebar_position: 3
---

# Build Templates

Before you can deploy a range, you first must build the template VMs (base VMs without customization) that will be used in your range.

Templates are the basis of every VM deployed by Ludus.
Unlike other solutions, Ludus templates are built from scratch (ISO), and by design don't contain any customization.
This allows users to modify base templates into arbitrary VMs during a deploy without having to maintain a library of stale, customized VMs.
This focus on infrastructure as code allows Ludus users to create fresh, up to date VMs every deployment.

The first step is to start the template build process. First, we can view the available templates.

```bash
local:~$ ludus templates list
+------------------------------------+-------+
|              TEMPLATE              | BUILT |
+------------------------------------+-------+
| debian-11-x64-server-template      | FALSE |
| debian-12-x64-server-template      | FALSE |
| kali-x64-desktop-template          | FALSE |
| win11-22h2-x64-enterprise-template | FALSE |
| win2022-server-x64-template        | FALSE |
+------------------------------------+-------+
```

On a fresh install, no templates are built. Ludus will build them from ISO files (with checksums) with the following command.

```bash
local:~$ ludus templates build
[INFO]  Template building started - this will take a while. Building 1 template(s) at a time.
```

To check the status of the template build, you can run `templates status`, `templates list` again, or follow the packer logs with 

```
local:~$ ludus templates logs -f
2023/12/01 22:00:47 [INFO] Packer version: 1.9.4 [go1.20.7 linux amd64]
2023/12/01 22:00:47 [TRACE] discovering plugins in /opt/ludus/resources/packer/plugins
2023/12/01 22:00:47 [INFO] Discovered potential plugin: proxmox = /opt/ludus/resources/packer/plugins/github.com/hashicorp/proxmox/packer-plugin-proxmox_v1.1.6_x5.0_linux_amd64
2023/12/01 22:00:47 [INFO] found external [-packer-default-plugin-name- clone iso] builders from proxmox plugin
2023/12/01 22:00:47 [INFO] PACKER_CONFIG env var not set; checking the default config file path
...
```

:::info

Building templates will take a while (up to a few hours depending on your internet and hardware speed).

:::

:::note

The "error" `[DEBUG] Error getting SSH address: 500 QEMU guest agent is not running` or `[DEBUG] Error getting WinRM host: 500 QEMU guest agent is not running` is expected and you will see this printed every 8 seconds until the VM has installed the OS and rebooted.
Don't panic!

:::

Use `control+c` to stop following the logs.

You can also monitor template builds using the Proxmox web GUI. It is available at `https://<ludus IP>:8006` and the credentials for the web GUI can be retrieved with `ludus user creds get`.

Once all the templates have been built, you can deploy a range.

Curious how templates work? Check out the [Templates](../templates) page.