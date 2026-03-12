# Guacamole Server

Installs and configures [Apache Guacamole](https://guacamole.apache.org/).

## OS Platforms

This role has been tested on the following operating systems:

- Debian 12 (server)


## Role Variables

```yaml
# The user that will own the install directory
default_user: "{{ ansible_user_id }}"

# DB container credentials
postgres_user: "guac_user"
postgres_password: "doubleguacplease"

# define nginx docker container version
nginx_version: "1.27.0"

# define postgres docker container version
postgres_version: "15.0"

# define guacamole containers version
guacamole_backend_version: "1.6.0"
guacamole_frontend_version: "1.6.0"

# Guacamole password for the guacadmin user
guac_password: "doubleguacplease"
```

## Changes

- v1.0.3 - Fix port definition in docker compose file, update default container versions to 1.6.0
- v1.0.2 - Fix status code check (204 vs 200)
- v1.0.1 - Fix pip install compatibility with older OSs
- v1.0.0 - Initial release

## Troubleshooting, Known Issues

- The guacamole server will serve through nginx in port 443 with a self-signed certificate. 

## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-guacamole-server"
    hostname: "{{ range_id }}-guacamole-server"
    template: debian-12-x64-server-template
    vlan: 10
    ip_last_octet: 50
    ram_gb: 8
    cpus: 4
    linux: true
    roles:
      - ludus_guacamole_server
      - ludus_guacamole_client
    role_vars:
      guac_password: guacpassword

  - vm_name: "{{ range_id }}-guacamole-ssh-client"
    hostname: "{{ range_id }}-guacamole-ssh-client"
    template: debian-12-x64-server-template
    vlan: 10
    ip_last_octet: 51
    ram_gb: 4
    cpus: 2
    linux: true
    roles:
      - ludus_guacamole_client
    role_vars:
      guac_password: guacpassword

  - vm_name: "{{ range_id }}-guacamole-rdp-client"
    hostname: "{{ range_id }}-guacamole-rdp-client"
    template: win11-22h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 52
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: false
    roles:
      - ludus_guacamole_client
    role_vars:
      guac_password: guacpassword
```