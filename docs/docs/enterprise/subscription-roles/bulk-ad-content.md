# Bulk AD Content

Creates OUs, Users, and Groups on a domain controller from CSV files.

## Install

```
ludus ansible subscription-roles install -n ludus_bulk_ad_content
```

## Notes

Apply this role to a Domain Controller.

This role operates off of CSV files. Example files are located at

`/opt/ludus/users/<username>/.ansible/roles/ludus_bulk_ad_content/files`

or if installed globally at

`/opt/ludus/resources/global-roles/ludus_bulk_ad_content/files`

- Groups.example.csv
- OUs.example.csv
- ServiceAccounts.example.csv
- Users.example.csv

Copy these .csv files and name them

- Groups.csv
- OUs.csv
- ServiceAccounts.csv
- Users.csv

Edit these files to add large numbers of users, group, OUs, and service accounts to an Active Directory domain during deployment.

:::note

If you want to have different sets of bulk AD content to deploy, the easiest way is to copy the role, change the name, and edit the CSV files.

```
cd /opt/ludus/users/<username>/.ansible/roles/
cp -r ludus_bulk_ad_content ludus_bulk_ad_content_scenario_1
# edit files in 
ludus_bulk_ad_content_scenario_1/files
# and use `ludus_bulk_ad_content_scenario_1` as the role in your config
```

:::

## Role Variables

Available variables are listed below, along with default values

```yaml
ludus_bulk_ad_content_setup_user: '{{ ludus_domain_fqdn }}\{{ defaults.ad_domain_admin }}'
ludus_bulk_ad_content_setup_password: "{{ defaults.ad_domain_admin_password }}"
```

## Example Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: primary-dc
    testing:
      snapshot: true
      block_internet: false
    roles:
      - ludus_bulk_ad_content
```