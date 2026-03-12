# SMB Share

Sets up an SMB share on Windows 8 / Windows 2012 or newer and optionally mounts the share on clients.

:::warning
    This role will not remove existing shares/mappings when run unless specified.
    Use `state: absent` to remove shares/mappings (see Role Variables)
:::

## Role Variables

Available variables are listed below.

```yaml
ludus_smb_shares_setup: []
# Example of ludus_smb_shares_setup:
# ludus_smb_shares_setup:
#   - name: internal # Share name (required)
#     description: top secret share # Share description
#     path: C:\shares\internal # Share directory (will be created if it does not exist - required)
#     list: false # Specify whether to allow or deny file listing, in case user has no permission on share. Also known as Access-Based Enumeration
#     full: Administrators,CEO # Specify user list that should get full access on share, separated by comma
#     read: HR-Global # Specify user list that should get read access on share, separated by comma
#     deny: HR-External # Specify user list that should get no access, regardless of implied access on share, separated by comma
#     change: HR-Leadership # Specify user list that should get read and write access on share, separated by comma
#   - name: company
#     description: all company share
#     path: C:\shares\company
#     list: true
#     full: Administrators,CEO
#     read: Global
#   - name: badshare
#     path: C:\share\badshare
#     state: absent # Only requied when you want to remove the share
ludus_smb_shares_setup_user: '{{ ludus_domain_fqdn }}\{{ defaults.ad_domain_admin }}' # The user to use for setting up the share
ludus_smb_shares_setup_password: "{{ defaults.ad_domain_admin_password }}" # The password for the user to use for setting up the share

ludus_smb_shares_map: []
# Example of ludus_smb_shares_mount:
# ludus_smb_shares_map:
#   - letter: X
#     path: \\ludus.domain\internal
#   - letter: Y
#     path: \\ludus.domain\company
#   - letter: Z
#     path: \\ludus.domain\badshare
#     state: absent # Only requied when you want to remove the share mapping
ludus_smb_shares_map_user: '{{ ludus_domain_fqdn }}\{{ defaults.ad_domain_user }}' # The user to use for mapping the share
ludus_smb_shares_map_password: "{{ defaults.ad_domain_user_password }}" # The password for the user to use for mapping the share
```

## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-fileshare"
    hostname: "{{ range_id }}-fileshare"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 1
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: primary-dc
    roles:
      - ludus_smb_share
    role_vars:
      ludus_smb_shares_setup:
        - name: secret
          description: top secret share
          path: C:\shares\secret
          list: false
          full: ludus.domain\domainadmin
        - name: internal
          description: internal share
          path: C:\shares\internal
          list: true
          full: ludus.domain\domainadmin
          change: ludus.domain\domainuser
  - vm_name: "{{ range_id }}-client"
    hostname: "{{ range_id }}-client"
    template: win11-22h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 2
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: member
    roles:
      - name: ludus_smb_share
        depends_on:
          - role: ludus_smb_share
            vm_name: "{{ range_id }}-fileshare"
    role_vars:
      ludus_smb_shares_map:
        - letter: X
          path: \\ludus.domain\secret
        - letter: Y
          path: \\ludus.domain\internal
```
