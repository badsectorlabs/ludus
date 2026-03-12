# Sysmon

Installs Sysmon with selected configuration. Included configurations are [SwiftOnSecurity sysmon config](https://github.com/SwiftOnSecurity/sysmon-config) or [olafhartong sysmon-modular config](https://github.com/olafhartong/sysmon-modular).

:::note
    Supplying your own config is supported.
:::

## Usage

- Place the config of your choosing in the `files/` folder OR use the `sysmon_config_url`/`sysmon_config_linux_url` variables.

`/opt/ludus/users/<username>/.ansible/roles/ludus_sysmon/files`

or if installed globally at

`/opt/ludus/resources/global-roles/ludus_sysmon/files`


- The sysmon config XML file precedence is:

    1. `sysmon_config_file_url` variable value if specified

    2. `sysmon_config_file` variable value if specified

- The role will use `swiftonsecurity-sysmonconfig.xml` (included in the role) if not specified on Windows

- The role will use `mstic-linux-sysmonconfig.xml` (included in the role) if not specified on Debian/Ubuntu

## Supported platforms:

- Windows 10
- Windows 11
- Windows Server 2022
- Windows Server 2019
- Windows Server 2016
- Debian 11
- Debian 12
- Ubuntu 20.04

## Role Variables

Available variables are listed below.

```yaml
# Default configs visible in files/
sysmon_config_file: swiftonsecurity-sysmonconfig.xml # olafhartong-sysmonconfig.xml is also included in the role
# A URL to an xml like "https://yourdomain.com/yourconfig.xml"
sysmon_config_url: ""
sysmon_config_linux_file: mstic-linux-sysmonconfig.xml
sysmon_config_linux_url: ""
```


## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-CLIENT-WIN11"
    hostname: "{{ range_id }}-CLIENT-WIN11"
    template: win11-22h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 21
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: member
    roles:
      - ludus_sysmon

  - vm_name: "{{ range_id }}-debian12"
    hostname: "{{ range_id }}-debian12"
    template: debian-12-x64-server-template
    vlan: 10
    ip_last_octet: 30
    ram_gb: 8
    cpus: 4
    linux: true
    roles:
      - ludus_sysmon
```

## Change log

- v1.0.0 - Initial Release
- v1.0.1 - Allow custom configs to be use
- v1.0.2 - Fix bug preventing Windows install, allow URL to be set for windows config (`sysmon_config_url`), allow sysmon config to be changed and subsequent applications of the role will update the sysmon config
