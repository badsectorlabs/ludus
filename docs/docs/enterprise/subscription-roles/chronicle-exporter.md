# Google SecOps (Chronicle) Exporter

Installs [Google SecOps (Chronicle)](https://learn.microsoft.com/en-us/defender-endpoint/microsoft-defender-endpoint) Collection agent on Windows hosts (10/11 and 2016, 2019, 2022)

:::warning

  You must add your own `ludus_chronicle_exporter_creds` json blob as a base64 string to your range config or this role will not export any data

:::

## Getting Creds for this Role

1. Go to your `Collection Agents` page (`https://<your instance ID>.backstory.chronicle.security/settings/collection-agent`)

2. Download the `INGESTION AUTHENTICATION FILE`

3. Run `cat auth.json | base64` to get the blob to add to the range config

4. Go to the `Profile` page (`https://<your instance ID>.backstory.chronicle.security/settings/profile`)

5. Copy the `Customer ID` and use it as `ludus_chronicle_exporter_customer_id`


## Role Variables

Available variables are listed below, along with default values.

```yaml
# base64 blob of auth.json
ludus_chronicle_exporter_creds: 
# Customer ID from the SecOps profile
ludus_chronicle_exporter_customer_id:
# Force the collection of sysmon even if sysmon is not detected
ludus_chronicle_exporter_force_collect_sysmon: false
# onboard to install, offboard to uninstall
ludus_chronicle_exporter_action: onboard
```

:::tip

You can define these values once in `global_role_vars` and they will apply to all machines in the range

:::

## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64-1"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 6
    cpus: 4
    windows:
      sysprep: true
    domain:
      fqdn: ludus.domain
      role: primary-dc
    roles:
      - ludus_chronicle_exporter
    role_vars:
      ludus_chronicle_exporter_creds: ewogICJ0eXBlIjogI...
      ludus_chronicle_exporter_customer_id: 43923d3c-af10-46b7-a7c8-060cc9e1289e
```

## Example Ludus Range Config with `global_role_vars`

```yaml
global_role_vars:
  ludus_chronicle_exporter_creds: ewogICJ0eXBlIjogI...
  ludus_chronicle_exporter_customer_id: 43923d3c-af10-46b7-a7c8-060cc9e1289e

ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64-1"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 6
    cpus: 4
    windows:
      sysprep: true
    domain:
      fqdn: ludus.domain
      role: primary-dc
    roles:
      - ludus_chronicle_exporter
```