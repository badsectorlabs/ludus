# Mythic C2

Installs [Mythic](https://github.com/its-a-feature/Mythic) on Debian.

:::warning
    From the official documentation: "It's recommended to run Mythic on a VM with at least 2CPU and 4GB Ram."
:::

## How to use

```
ludus ansible role add geerlingguy.docker
ludus ansible role add gantsign.golang
ludus ansible role add geerlingguy.pip
ludus ansible subscription-roles install -n ludus_mythic
```

## Role Variables

Available variables are listed below, along with default values (see `defaults/main.yml`):

```yaml
# Repository
mythic_repo: https://github.com/its-a-feature/Mythic
mythic_version: 03e27abdb6eb951aecddcca4379de1c075d60856 # 2026-04-04 # v3.4.0.44
mythic_install_dir: /opt/mythic

# Mythic Configurations
mythic_debug_level: warning
mythic_operation_name: Ludus
mythic_admin_user: mythic_admin
mythic_admin_password: mythicadminpassword
mythic_admin_port: 7443

mythic_agents_config:
  - name: merlin # 2024-12-12 1.0.3
    repo: https://github.com/MythicAgents/merlin
    version: 43e64c5567540d7c9aa2065377acc8a887ef6278

  - name: athena # 2026-04-04 2.2.4
    repo: https://github.com/MythicAgents/Athena
    version: 13dfe5c8357a86973eab81df7b1ef4b67f9697f1

  - name: thanatos # 2025-09-15 0.1.14
    repo: https://github.com/MythicAgents/thanatos
    version: 411eb770d97ffd81414b0c3c4e81f5136b05a0ed

  - name: poseidon # 2026-04-12 0.0.3.14
    repo: https://github.com/MythicAgents/poseidon
    version: 54e3bf5d7b6cd33c7adef10dd46999e101edb023

  - name: medusa # 2025-07-16
    repo: https://github.com/MythicAgents/medusa
    version: cdc3127aa8d531c6477b0c36e26f4bbe540b72cc


mythic_c2_profiles:
  - name: http # 2026-02-05 0.0.3.2
    repo: https://github.com/MythicC2Profiles/http
    version: b1e7ed17719ed123ddc1a99d9802b3b9d85849f8

  - name: httpx # 2026-02-16 0.0.0.20
    repo: https://github.com/MythicC2Profiles/httpx
    version: b8e451f8325656f03c47c1c6b71f4bb2b9f5cebe

  - name: smb # 2025-12-11 0.1.1
    repo: https://github.com/MythicC2Profiles/smb
    version: 53138a68c2a5c9b3ae380f4ee26b24b7e93b86c5
```

## Dependencies

geerlingguy.docker
geerlingguy.pip

## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-mythic"
    hostname: "{{ range_id }}-mythic"
    template: debian-12-x64-server-template
    vlan: 20
    ip_last_octet: 1
    ram_gb: 8
    cpus: 4
    linux: true
    roles:
        - ludus_mythic
    role_vars:
      mythic_admin_user: myadminusername
      mythic_admin_password: mycustompassword
```

## Changelog

1.0.1 - Update mythic, agents, and profiles
1.0.0 - Initial release