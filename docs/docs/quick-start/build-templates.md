---
sidebar_position: 2
---

# Build Templates

Before you can deploy a range, you first must build the template VMs (base VMs without customization) that will be used in your range.

Templates are the basis of every VM deployed by Ludus.
Unlike other solutions, Ludus templates are built from scratch (ISO), and by design don't contain any customization.
This allows users to modify base templates into arbitrary VMs during a deploy without having to maintain a library of stale, customized VMs.
This focus on infrastructure as code allows Ludus users to create fresh, up to date VMs every deployment.

The first step is to start the template build process. First, we can view the available templates.

:::tip

Adding a space at the beginning of the `export LUDUS_API_KEY=..` command will prevent it from being written to the
shell's history file in most common shells.

:::

```shell-session
#terminal-command-ludus
su -
#terminal-command-ludus-root
ludus-install-status
Initial admin credentials:
  userID: JD
  Proxmox username: john-doe
  Proxmox password: password
  Ludus Web username: john.doe@example.com
  Ludus Web password: password

  API key for user JD: JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt
  [Note: This API key will be recreated if this command is run again and the old key will no longer work]
#terminal-command-ludus-root
exit
#terminal-command-ludus
 export LUDUS_API_KEY=JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt
#terminal-command-ludus
ludus templates list
+------------------------------------+--------------+
|              TEMPLATE              |    STATUS    |
+------------------------------------+--------------+
| debian-12-x64-server-template      | ❌ NOT BUILT |
| debian-13-x64-server-template      | ❌ NOT BUILT |
| kali-x64-desktop-template          | ❌ NOT BUILT |
| win11-22h2-x64-enterprise-template | ❌ NOT BUILT |
| win2022-server-x64-template        | ❌ NOT BUILT |
+------------------------------------+--------------+
```

On a fresh install, no templates are built. Ludus will build them from ISO files (with checksums) with the following command.

```shell-session
#terminal-command-ludus
ludus templates build
[INFO]  Template building started - this will take a while. Building 1 template(s) at a time.
```

:::tip

If you have decently powerful hardware, you can build more than 1 template at a time with the `--parallel` option to specify how many
templates to build concurrently. Be aware that when building in parallel, no template logs will be generated (see [issue #55](https://gitlab.com/badsectorlabs/ludus/-/issues/55#note_2026923273))

:::

To check the status of the template build, you can run `templates status`, `templates list` again, or follow the packer logs with 

```shell-session
#terminal-command-ludus
ludus templates logs -f
2026/09/01 12:58:17 ui: ==> proxmox-iso.debian13: Retrieving ISO
2026/09/01 12:58:17 ui: ==> proxmox-iso.debian13: Trying https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/debian-13.6.0-amd64-netinst.iso
2026/09/01 12:58:27 ui: ==> proxmox-iso.debian13: Creating VM
...
```

:::info

Building templates will take a while (up to a few hours depending on your internet and hardware speed).

If multiple VMs time out without getting created, there may be a [network issue](../troubleshooting/network.md).

:::

Use `control+c` to stop following the logs.

You can also monitor template builds using the Proxmox web UI. It is available at `https://<ludus IP>:8006` and the credentials for the Proxmox web UI can be retrieved with `ludus user creds get`.

Once all the templates have been built, you can deploy a range.

Curious how templates work? Check out the [Templates](../using-ludus/templates.md) page.