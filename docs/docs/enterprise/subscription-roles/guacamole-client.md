# Guacamole Client

Adds the VM as a client to an existing apache ludus guacamole server

## OS Platforms

This role has been tested on the following operating protocols:

- SSH (Supports unix based systems)
- RDP (Supports windows based Ludus instances)


# Role Variables

Available variables are listed below, along with default values.

```yaml
# Whether you want to add this instance as a client or not
add_client: "true"

# Username of the Guacamole admin account used to manage connections.
guac_admin_username: "guacadmin"

# Default password of the guacamole server. Needed to authenticate to the server.
guac_admin_password: "doubleguacplease"

# Guacamole accounts that receive access to their configured connection.
guac_connection_users:
  guacadmin: {}

# Default SSH creds
guac_ssh_username: "debian"
guac_ssh_password: "debian"
guac_ssh_port: "22"

# Windows Default Creds
guac_rdp_user: "{{ 'localuser' if ludus_domain_fqdn is undefined else defaults.ad_domain_user }}"
guac_rdp_password: "{{ 'password' if ludus_domain_fqdn is undefined else defaults.ad_domain_user_password }}"
guac_rdp_port: "3389"

```

`guac_connection_users` is keyed by Guacamole account name. Each account accepts these optional settings:

| Setting | Value | Default |
| --- | --- | --- |
| `connection_type` | `rdp`, `ssh`, or `vnc` | `ssh` on Linux; `rdp` on Windows |
| `username` | Username sent to the target host | `guac_ssh_username` on Linux; `guac_rdp_user` on Windows |
| `password` | Password sent to the target host | `guac_ssh_password` on Linux; `guac_rdp_password` on Windows |
| `connection_port` | TCP port on the target host | `guac_ssh_port` on Linux; `guac_rdp_port` on Windows |

The Guacamole admin account keeps the host name as its connection name. Other accounts receive separate connections named `<host> (<Guacamole username>)`.
Every mapping key must already exist as a Guacamole account. When `ludus_guacamole_server` manages the accounts, include every key in its `guac_users` list.


## Changes

- v1.2.1 - Create configured connections without requiring their custom destination ports to be open, and normalize numeric credentials as strings.
- v1.2.0 - Added per-user protocol, credentials, and port 
- v1.1.1 - Change `guac_password` to `guac_admin_password` to match ludus_guacamole_server format
- v1.1.0 - Added `guac_admin_username` and `guac_connection_users` variables, with automatic READ permission grants for configured users on created connections; extended guacamole server discovery to support router-hosted servers.
- v1.0.6 - Changed client connectivity checks to test TCP port 22 for Linux clients and TCP port 3389 for Windows clients.

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
      guac_admin_password: guacpassword
      guac_users:
        - username: analyst
          password: analystpassword
        - username: operator
          password: operatorpassword
        - username: analyst-vnc
          password: analystvncpassword

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
      guac_admin_password: guacpassword
      guac_connection_users:
        guacadmin: {}
        analyst:
          connection_type: ssh
          username: test
          password: debian
          connection_port: 2223
        analyst-vnc:
          connection_type: vnc
          username: test-vnc
          password: debian
          connection_port: 5901

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
      guac_admin_password: guacpassword
      guac_connection_users:
        guacadmin: {}
        operator:
          connection_type: rdp
          username: myuser
          password: Summer2026!
          connection_port: 3389

```