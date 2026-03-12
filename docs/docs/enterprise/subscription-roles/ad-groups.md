# AD Groups

Creates a group in AD and optionally populates it with specified users.

Use it on a domain controller.
Path is optional.
Any missing groups will be created.

## Install

```
ludus ansible subscription-roles install -n ludus_ad_groups
```

## Role Variables

All groups should be nested under the `ludus_ad_groups` key and contain a `name`, optional `path`, and optional `members`

```yaml
ludus_ad_groups:
  - name: Server Admins
    path: OU=groups,DC=ludus,DC=domain
    members:
      ludus.domain\domainuser
```

## Example Range Config

```yaml
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: primary-dc
    roles:
        - ludus_ad_groups
    role_vars:
        ludus_ad_groups:
          - name: Server Admins
            path: OU=groups,DC=ludus,DC=domain
            members:
              ludus.domain\domainuser
```