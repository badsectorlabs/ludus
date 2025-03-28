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
  * Uninstalls VirtIO Serial Driver
  * Removes QEMU Guest Agent and related services
  * Deletes RedHat registry keys
  * Removes virtualization-related folders (C:\ludus, C:\Tools, C:\QEMU-ga)

* Configures a more realistic desktop environment:
  * Restores default Windows wallpaper
  * Removes Ludus-specific background configurations

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

You can also specify `--drop-files` to populate the autologon user's desktop and download folders with random files (PPTX, DOC, XLSX, and PDF). The `--org` and `--owner` flags can be used to specify the organization and owner of the Machine set in the registry.

If there are any errors during the enable process, you can check the logs with `ludus range logs` or `ludus range errors`.


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
