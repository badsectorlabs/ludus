---
sidebar_position: 7
title: "🎭 Roles"
---

## How to use Roles

Roles are Ansible roles that are applied to VMs in Ludus after they are deployed and configured. It's easy to add a role to a Ludus VM, simply define the `roles` key in the config:

```yaml title="range-config.yml"
ludus:
  - vm_name: "{{ range_id }}-docker-host"
    hostname: "{{ range_id }}-docker"
    template: debian-12-x64-server-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 8
    cpus: 4
    linux: true
    //highlight-next-line
    roles:                  # This key is an array of user-defined roles that will be installed on this VM. Roles must exist on the Ludus server and can be installed with `ludus ansible role add`
    //highlight-next-line
      - geerlingguy.docker  # Arbitrary role name, as it appears in `ludus ansible roles list`
    role_vars:              # This key contains `key: value` pairs of variables that are passed to ALL user-defined roles.
      docker_edition: ce    # Arbitrary variables for user-defined roles. Do *not* use hypens to prefix these variables, the role_vars key *must* be a dictionary!
      docker_users:         # You can use lists or dicts here
        - localuser
```

You can define any variables that will be passed to the role with `role_vars` as seen above. Note that all variable in `role_vars` will be passed to all roles.

## Ludus Specific Roles

While most existing ansible roles will work with Ludus, this page contains a table of roles specifically designed for Ludus.

| Role | Description | Author | Notes |
| ---- | ----------- | ------ | ----- |
| [badsectorlabs.ludus_vulhub](https://github.com/badsectorlabs/ludus_vulhub) | Runs [Vulhub](https://vulhub.org/) environments on a Linux system. | Bad Sector Labs | See [the env guide](./environment-guides/vulhub.md) |
| [badsectorlabs.ludus_adcs](https://github.com/badsectorlabs/ludus_adcs) | Installs ADCS on Windows Server and optionally configures Certified Preowned templates. | Bad Sector Labs | See [the env guide](./environment-guides/adcs.md) |
| [badsectorlabs.ludus_bloudhound_ce](https://github.com/badsectorlabs/ludus_bloudhound_ce) | Installs Bloodhound CE on a Debian based system. | Bad Sector Labs ||
| [badsectorlabs.ludus_mssql](https://github.com/badsectorlabs/ludus_mssql) | Installs MSSQL on Windows systems. | Bad Sector Labs ||
| [badsectorlabs.ludus_elastic_container](https://github.com/badsectorlabs/ludus_elastic_container) | Installs "The Elastic Container Project" on a Linux system. | Bad Sector Labs | See [the env guide](./environment-guides/elastic.md) |
| [badsectorlabs.ludus_elastic_agent](https://github.com/badsectorlabs/ludus_elastic_agent) | Installs an Elastic Agent on a Windows, Debian, or Ubuntu system | Bad Sector Labs | See [the env guide](./environment-guides/elastic.md) |
| [badsectorlabs.ludus_xz_backdoor](https://github.com/badsectorlabs/ludus_xz_backdoor) | Installs the xz backdoor (CVE-2024-3094) on a Debian host and optionally installs the xzbot tool. | Bad Sector Labs | See [the env guide](./environment-guides/malware-lab.md) |
| [badsectorlabs.ludus_commandovm](https://github.com/badsectorlabs/ludus_commandovm) | Sets up Commando VM on Windows >= 10 hosts | Bad Sector Labs | Available as a [template](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates/commando-vm?ref_type=heads) |
| [badsectorlabs.ludus_flarevm](https://github.com/badsectorlabs/ludus_flarevm) | Installs Flare VM on Windows >= 10 hosts | Bad Sector Labs | Available as a [template](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates/flare-vm?ref_type=heads) |
| [badsectorlabs.ludus_remnux](https://github.com/badsectorlabs/ludus_remnux) | Installs REMnux on Ubuntu 20.04 systems | Bad Sector Labs | Available as a [template](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates/remnux?ref_type=heads) |
| [aleemladha.wazuh_server_install](https://github.com/aleemladha/wazuh_server_install) | Install Wazuh SIEM Unified XDR and SIEM protection with SOC Fortress Rules | [@LadhaAleem](https://twitter.com/LadhaAleem) ||
| [ludus_child_domain](https://github.com/ChoiSG/ludus_ansible_roles) | Create a child domain and domain controller because ansible's microsoft.ad doesn't support it | [@_choisec](https://twitter.com/_choisec) ||
| [ludus_child_domain_join](https://github.com/ChoiSG/ludus_ansible_roles) | Join a machine to the child domain created from ludus_child_domain, since ludus's backend does not support domain/controllers created with 3rd party ansible roles | [@_choisec](https://twitter.com/_choisec) ||