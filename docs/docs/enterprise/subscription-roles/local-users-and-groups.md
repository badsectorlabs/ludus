# Local Users

Creates local users and add them to existing groups on the VM.

## OS Platforms

- Windows
- Linux
- macOS

## Role Variables

By default this role does nothing. You must define `ludus_local_users` and/or `ludus_local_groups`.

```yaml
# Windows
ludus_local_users:
- username: localusername
    password: localpassword
ludus_local_groups:
  - name: Administrators # group must exist
    members:
      - testuser
  - name: Remote Desktop Users
    members:
      - Administrators
      - ludus\Server Admins
# Unix
ludus_local_users:
  - login: localusername
    password: localpassword
    groups: sudo # optional, group must exist
    passwordless_sudo: true # optional, Linux only
  - login: otheruser
    password: somepassword
```

## Example Ludus Range Config


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
      - ludus_local_users
    role_vars:
      ludus_local_users:
        - username: testuser
          password: testpassword
      ludus_local_groups:
        - name: Administrators
          members:
            - testuser
        - name: Remote Desktop Users
          members:
            - Administrators
            - ludus\Server Admins
  - vm_name: "{{ range_id }}-mythic"
    hostname: "{{ range_id }}-mythic"
    template: debian-12-x64-server-template
    vlan: 99
    ip_last_octet: 2
    ram_gb: 8
    cpus: 2
    linux: true
    testing:
      snapshot: true
      block_internet: false
    roles:
      - ludus_local_users
    role_vars:
      ludus_local_users:
        - login: mythicadmin
          password: mythicpassword
          groups: sudo
          passwordless_sudo: true
        - login: otheruser
          password: somepassword
```