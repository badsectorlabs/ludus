---
sidebar_position: 3
title: "🚫🏖️ Anti-Sandbox"
---

# 🚫🏖️ Anti-Sandbox

:::note[🏛️ `Available as an add-on to Ludus Enterprise`]
:::

Ludus Enterprise can optionally include a plugin that enables the use of the Anti-Sandbox measures.

## What is a VM sandbox?

A VM sandbox is a virtual machine that is used for malware research or other purposes. It often includes software and other tools that are used to perform malware analysis, such as a debugger, memory analyzer, or disassembler.

However, some malware or other software may specifically look for artifacts of a virtual machine that are not normally present on "real" hosts. This allows the malware to change its behavior, and potentially mislead the analyst or otherwise not perform the same actions as it would on a "real" host.

## What is the Ludus Anti-Sandbox plugin?

The Ludus Anti-Sandbox plugin uses custom compiled QEMU and OVMF packages that have sandbox artifacts (i.e. QEMU strings, etc) removed to create a VM that appears to be a "real" host. Additionally, the Ludus Anti-Sandbox plugin modifies specified VMs in the following ways:

* Drop and configure realistic user files:
  * Adds random numbers of PDF, DOC, PPTX, and XLSX files to Desktop and Downloads folders
  * Sets random creation/modification dates on files spanning the last 5 years
  * Opens random files to create usage artifacts and recent files history

* Modifies system timestamps and registration:
  * Sets a random Windows installation date between 2021-2024
  * Can configure custom registered organization and owner information

* Removes virtualization artifacts:
  * Uninstalls the VirtIO Serial Driver using its actual INF package, without assuming an OEM driver number
  * Removes QEMU Guest Agent and related services
  * Deletes RedHat registry keys
  * Removes virtualization-related folders (C:\ludus, C:\Tools, C:\QEMU-ga)
  * Removes disconnected virtual devices through Windows PnP; preserves active PCI/SCSI devices and the VM Generation ID needed for safe domain-controller operation

* Configures a more realistic desktop environment:
  * Restores default Windows wallpaper
  * Removes Ludus-specific background configurations

* Modifies processor information:
  * Can configure custom processor name
  * Can configure custom processor vendor identifier
  * Can configure custom processor speed
  * Can configure custom processor identifier

* Optionally populates browser profile data (with `--browser-data`):
  * Adds history, bookmarks, Top Sites, saved passwords, cookies, autofill entries, and address profiles to Microsoft Edge and Google Chrome
  * Adds `TypedURLs` registry entries

## Comparison

This is a comparison of the Ludus Anti-Sandbox plugin and the standard Windows 11 template.

## Standard Windows 11 VM

Notice the QEMU strings and other virtualization artifacts in the standard Windows 11 VM.

![A screenshot showing the VM artifacts in a standard VM](/img/enterprise/normal-vm.png)

## Ludus Anti-Sandbox VM

The VM artifacts have been replace with realistic artifacts that are present in a "real" host.
![A screenshot showing the realistic artifacts in an anti-sandbox VM](/img/enterprise/anti-sandbox-enabled.png)


## How to use

:::note

Ludus Anti-Sandbox is not supported on macOS or Linux VMs at this time. Contact us if you need this feature for those platforms.

:::

The Ludus Anti-Sandbox plugin works best with Windows VMs that have the bare minimum required to function in a hypervisor. One such template is included in the plugin: `win11-22h2-x64-enterprise-antisandbox`.

To use the Ludus Anti-Sandbox plugin, first build the `win11-22h2-x64-enterprise-antisandbox` template with the Ludus Enterprise plugin:

```shell-session
#terminal-command-local
ludus templates build -n win11-22h2-x64-enterprise-antisandbox-template
[INFO]  Template building started
```

You can now use the `win11-22h2-x64-enterprise-antisandbox` template in ranges.
You should also set `force_ip: true` in the range config to ensure the VMs maintain their IP addresses for ansible after the QEMU guest agent is removed.
The inventory also uses the range config's `windows` settings when guest-agent
OS information is unavailable. This preserves the WinRM connection group even
when the agent answers network queries before its OS queries after a reboot.

```yaml
ludus:
    ...
    template: win11-22h2-x64-enterprise-antisandbox
    ...
    force_ip: true
    ...
```

To take full advantage of the Anti-Sandbox feature, you must install the custom QEMU and OVMF packages:

```shell-session
#terminal-command-local
ludus antisandbox install-custom
[INFO]  Anti-Sandbox QEMU and OVMF installed - will take effect on VM's next power cycle
```

:::note

The custom QEMU and OVMF packages apply to the entire Ludus host.

:::

### LXC node access

The RPC plugin runs inside the Ludus LXC. Proxmox API requests still use the
configured token, but `qm`, package inspection, and QEMU/OVMF installation must
execute on the Proxmox node. Configure a dedicated root SSH key, an explicitly
pinned host key, and a node-to-address map for `ludus-admin` inside the LXC:

```ini
# /etc/systemd/system/ludus-admin.service.d/antisandbox-ssh.conf
[Service]
Environment="LUDUS_ANTISANDBOX_SSH_KEY_FILE=/etc/ludus/antisandbox/node_ed25519"
Environment="LUDUS_ANTISANDBOX_SSH_KNOWN_HOSTS_FILE=/etc/ludus/antisandbox/known_hosts"
Environment='LUDUS_ANTISANDBOX_SSH_HOSTS={"pve1":"192.0.2.254"}'
```

Replace `pve1` and its address with the actual Proxmox node name and a reachable
address. Map every node that can host the target VMs. Addresses can include an
SSH port. Provision the private key as root-only inside the LXC and authorize
its public key on the selected nodes. Pin host keys obtained through a trusted
channel; the plugin requires strict host-key checking. Restrict the authorized
key to the LXC's source address and disable forwarding and PTY allocation.
This access permits root commands on the mapped nodes; it is not a sandbox for
untrusted plugins.

After provisioning the credentials and drop-in, run inside the LXC:

```shell
systemctl daemon-reload
systemctl restart ludus-admin
```

VM operations select the VM's actual node. Package/status API routes accept
`?node=<Proxmox-node-name>` and otherwise use `proxmox_node` from the server
configuration. An absent mapping or failed SSH connection is an error, not an
indication that packages are missing. Custom packages must have a compatible
published version; do not replace host-wide QEMU/OVMF packages during active
builds or without a recovery plan.

### Deploy and enable

Once your range config is updated to use the `win11-22h2-x64-enterprise-antisandbox` template, deploy the range:

```shell-session
#terminal-command-local
ludus range deploy
[INFO]  Range deploy started
```

When the range is fully deployed, make any modifications to the VMs you want before enabling Anti-Sandbox (take a snapshot as well).
When you are ready to enable Anti-Sandbox, note the VMID for the VM and run the following command. Multiple VMs can be specified with a comma separated list.

```shell-session
#terminal-command-local
ludus snapshot create -n 179 -d "Clean snapshot before enabling anti-sandbox" pre-antisandbox
#terminal-command-local
ludus antisandbox enable -n 179
[INFO]  Enabling Anti-Sandbox settings for VM(s), this can take some time. Please wait.
[INFO]  Successfully enabled anti-sandbox for VM(s): 179
```

Before changing virtual hardware, the plugin stages a one-shot NIC migration
and requests a clean Windows shutdown. It changes the node's VM configuration
only after the guest has stopped, then starts the VM and waits for Proxmox to
report it running before generating Ansible inventory. IPv4 addresses, routes,
and DNS settings move to the replacement Intel NIC; the migration task and its
files remove themselves after success. A failed shutdown is an error, not a
reason to hard-stop Windows and risk losing cached disk writes.

Configured domain members and controllers use `defaults.ad_domain_admin` and
`defaults.ad_domain_admin_password` over NTLM for management. The domain must
already be deployed. Standalone Windows VMs use the template's `localuser`
account. Enable waits for authenticated WinRM, not just an open TCP port:
domain controllers can open port 5986 before domain authentication is ready.

You can also specify `--drop-files` to populate the autologon user's desktop and download folders with random files (PPTX, DOC, XLSX, and PDF). The `--org` and `--owner` flags can be used to specify the organization and owner of the Machine set in the registry.

If there are any errors during the enable process, check the logs with `ludus range logs` or `ludus range errors`.

:::note

If you experience a Blue Screen of Death (BSOD) after enabling Anti-Sandbox, you can try the following:

```
echo 1 > /sys/module/kvm/parameters/ignore_msrs
```

If that allows the VM to boot, make it permanent by adding the following to `/etc/modprobe.d/kvm.conf`:

```
options kvm ignore_msrs=1
options kvm report_ignored_msrs=0
```

:::

### Browser data

Use `--browser-data` to populate Microsoft Edge and Google Chrome on the VM with profile data. This adds browsing history, bookmarks, Top Sites, saved passwords, cookies, autofill entries, address profiles, and `TypedURLs` registry entries.

Edge is pre-installed on Windows 11, so it will be populated by default. To also populate Chrome, add `googlechrome` to the `chocolatey_packages` list in the range config so it is installed before the range is deployed.

Windows Server images may contain neither browser. Install `googlechrome` and/or
`microsoft-edge` before requesting browser data. The plugin starts installed
browsers with the autologon user's persistent data directory and waits for their
native profile databases before seeding. Missing browsers or incomplete profile
initialization fail the operation rather than silently omitting data.
On domain controllers, use an account permitted to log on interactively, such
as the synthetic domain administrator in an isolated lab. Profile paths are
resolved from Windows rather than assumed to match the account name.

```shell-session
#terminal-command-local
ludus antisandbox enable -n 179 --browser-data
[INFO]  Enabling Anti-Sandbox settings for VM(s), this can take some time. Please wait.
[INFO]  Successfully enabled anti-sandbox for VM(s): 179
```

The seed data is generated offline with the [Faker](https://github.com/joke2k/faker) library and contains no real user data.

:::note

`--browser-data` sets a machine-wide `ApplicationBoundEncryptionEnabled=0` policy for Edge and Chrome. This disables Chrome's App-Bound Encryption (introduced in Chrome 127, July 2024) and forces the browser to fall back to v10 DPAPI encryption for cookies and saved passwords, so the seeded data can be decrypted by the browser. Do not use this flag on VMs where users will sign in with a real Microsoft or Google account.

:::

After the command completes, log in as the autologon user and open Edge to verify:

* `edge://history`
* `edge://favorites`
* `edge://settings/autofill/passwords`
* `edge://settings/privacy/cookies/AllCookies`
* `edge://settings/autofill/personalInfo`
* The new tab page for Top Sites

## Example Anti-Sandbox Configuration

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64"
    hostname: "NYC-DC01-ACQ34"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    force_ip: true
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: company.com
      role: primary-dc
  - vm_name: "{{ range_id }}-ad-win11-22h2-enterprise-x64-1"
    hostname: "Q2VZX232CY"
    template: win11-22h2-x64-enterprise-antisandbox-template
    vlan: 10
    ip_last_octet: 21
    force_ip: true
    ram_gb: 8
    cpus: 4
    windows:
      install_additional_tools: false
      chocolatey_ignore_checksums: true # Chrome is always out of date
      chocolatey_packages:
        - googlechrome
        - firefox
        - adobereader
        - zoom
        - microsoft-teams-new-bootstrapper
        - webex
        - slack
        - bitwarden
        - 7zip
      office_version: 2021
      office_arch: 64bit
      autologon_user: DA-john.doe
      autologon_password: password
    domain:
      fqdn: company.com
      role: member
  - vm_name: "{{ range_id }}-ad-win11-22h2-enterprise-x64-2"
    hostname: "BAVZ2532VD"
    template: win11-22h2-x64-enterprise-antisandbox-template
    vlan: 10
    ip_last_octet: 22
    force_ip: true
    ram_gb: 8
    cpus: 4
    windows:
      install_additional_tools: false
      chocolatey_ignore_checksums: true # Chrome is always out of date
      chocolatey_packages:
        - googlechrome
        - firefox
        - adobereader
        - zoom
        - microsoft-teams-new-bootstrapper
        - webex
        - slack
        - bitwarden
        - 7zip      
      office_version: 2021
      office_arch: 64bit
      autologon_user: john.doe
      autologon_password: password
    domain:
      fqdn: company.com
      role: member

defaults:
  snapshot_with_RAM: true
  stale_hours: 0
  ad_domain_functional_level: Win2012R2
  ad_forest_functional_level: Win2012R2
  ad_domain_admin: DA-john.doe
  ad_domain_admin_password: password
  ad_domain_user: john.doe
  ad_domain_user_password: password
  ad_domain_safe_mode_password: password
  timezone: America/New_York
  enable_dynamic_wallpaper: false
```
