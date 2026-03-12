# Windows Random Files

This role drops random files onto a Windows host (Desktop and Downloads folder). You must have the anti-sandbox plugin on the server for this to work.

The host is rebooted and files are randomly opened to populate recent files list and associated artifacts.

Supported platforms:

- Windows 11
- Windows 10
- Windows Server 2022
- Windows Server 2019
- Windows Server 2016
- Windows Server 2012R2

## Role Variables

There are no role variables for this role.

## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-CLIENT-WIN10"
    hostname: "{{ range_id }}-CLIENT-WIN10"
    template: win10-21h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 20
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: member
    roles:
      - ludus_random_files
```