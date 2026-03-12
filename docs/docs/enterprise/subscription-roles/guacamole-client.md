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

# Default password of the guacamole server. Needed to authenticate to the server.
guac_password: "doubleguacplease"

# Default SSH creds
guac_ssh_username: "debian"
guac_ssh_password: "debian"
guac_ssh_port: "22"

# Default RDP (Windows) Creds
guac_rdp_user: "{{ 'localuser' if ludus_domain_fqdn is undefined else defaults.ad_domain_user }}"
guac_rdp_password: "{{ 'password' if ludus_domain_fqdn is undefined else defaults.ad_domain_user_password }}"
guac_rdp_port: "3389"
```


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