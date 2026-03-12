# Unconstrained Delegation

This role enables unconstrained delegation for hosts in the domain and reboots them to ensure unconstrained delegation is applied.

:::warning
    This role should only be used on a domain controller!
:::

## Role Variables

```yaml
ludus_unconstrained_delegation_hosts: [] # An array of NETBIOS names to grant unconstrained delegation to
```

## Example Ludus Range Config

```yaml
ludus:
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
        - ludus_unconstrained_delegation
    role_vars:
        ludus_unconstrained_delegation_hosts:
          - "{{ range_id }}-SHAREPOINT
```


