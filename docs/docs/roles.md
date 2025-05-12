---
sidebar_position: 8
title: "🎭 Roles"
---

## How to use Roles

:::tip

Looking to create your own roles? Check out the [role developer](./developers/ansible-roles.md) page!

:::

Roles are Ansible roles that are applied to VMs in Ludus after they are deployed and configured. It's easy to add a role to a Ludus VM, simply add the role to Ludus and then define the `roles` key in the config.

Roles are unique to each user on a Ludus host, which allows users to have different versions of roles, custom roles, etc without overwriting or breaking each other's roles.

:::tip Ansible Galaxy

Any ansible role (35,000+) can be used with Ludus, as long as it is compatible with the OS of the VM and the roles pre-requisites are met. You can search for roles on [Ansible Galaxy](https://galaxy.ansible.com/ui/standalone/roles/).

:::

To add a role to Ludus, use the client as the user that will deploy the role (optionally specify the user/range that will use the role with `--user`)

```bash
# Add directly from Ansible Galaxy
#terminal-command-local
ludus ansible role add badsectorlabs.ludus_adcs

# Add from a local directory
#terminal-command-local
ludus ansible role add -d ./ludus_child_domain

# Add a role for another user/range (as an admin)
#terminal-command-local
ludus ansible role add badsectorlabs.luds_adcs --user USER2
```

After roles have been added to Ludus, you can modify the range config to use them:

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
      docker_edition: ce    # Arbitrary variables for user-defined roles. Do *not* use hyphens to prefix these variables, the role_vars key *must* be a dictionary!
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
| [badsectorlabs.ludus_bloodhound_ce](https://github.com/badsectorlabs/ludus_bloodhound_ce) | Installs Bloodhound CE on a Debian based system. | Bad Sector Labs ||
| [badsectorlabs.ludus_mssql](https://github.com/badsectorlabs/ludus_mssql) | Installs MSSQL on Windows systems. | Bad Sector Labs ||
| [badsectorlabs.ludus_elastic_container](https://github.com/badsectorlabs/ludus_elastic_container) | Installs "The Elastic Container Project" on a Linux system. | Bad Sector Labs | See [the env guide](./environment-guides/elastic.md) |
| [badsectorlabs.ludus_elastic_agent](https://github.com/badsectorlabs/ludus_elastic_agent) | Installs an Elastic Agent on a Windows, Debian, or Ubuntu system | Bad Sector Labs | See [the env guide](./environment-guides/elastic.md) |
| [badsectorlabs.ludus_xz_backdoor](https://github.com/badsectorlabs/ludus_xz_backdoor) | Installs the xz backdoor (CVE-2024-3094) on a Debian host and optionally installs the xzbot tool. | Bad Sector Labs | See [the env guide](./environment-guides/malware-lab.md) |
| [badsectorlabs.ludus_commandovm](https://github.com/badsectorlabs/ludus_commandovm) | Sets up Commando VM on Windows >= 10 hosts | Bad Sector Labs | Available as a [template](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates/commando-vm?ref_type=heads) |
| [badsectorlabs.ludus_flarevm](https://github.com/badsectorlabs/ludus_flarevm) | Installs Flare VM on Windows >= 10 hosts | Bad Sector Labs | Available as a [template](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates/flare-vm?ref_type=heads) |
| [badsectorlabs.ludus_remnux](https://github.com/badsectorlabs/ludus_remnux) | Installs REMnux on Ubuntu 20.04 systems | Bad Sector Labs | Available as a [template](https://gitlab.com/badsectorlabs/ludus/-/tree/main/templates/remnux?ref_type=heads) |
| [badsectorlabs.ludus_emux](https://github.com/badsectorlabs/ludus_emux) | Installs EMUX and runs an emulated device on Debian based hosts | Bad Sector Labs | |
| [aleemladha.wazuh_server_install](https://github.com/aleemladha/wazuh_server_install) | Install Wazuh SIEM Unified XDR and SIEM protection with SOC Fortress Rules | [@LadhaAleem](https://twitter.com/LadhaAleem) ||
| [aleemladha.ludus_wazuh_agent](https://github.com/aleemladha/ludus_wazuh_agent) | Deploys Wazuh Agents to Windows systems | [@LadhaAleem](https://twitter.com/LadhaAleem) ||
| [aleemladha.ludus_exchange](https://github.com/aleemladha/ludus_exchange) | Installs Microsoft Exchange Server on a Windows Server host | [@LadhaAleem](https://twitter.com/LadhaAleem) ||
| [ludus_child_domain](https://github.com/ChoiSG/ludus_ansible_roles) | Create a child domain and domain controller because ansible's microsoft.ad doesn't support it | [@_choisec](https://twitter.com/_choisec) | Must install from directory |
| [ludus_child_domain_join](https://github.com/ChoiSG/ludus_ansible_roles) | Join a machine to the child domain created from ludus_child_domain, since ludus's backend does not support domain/controllers created with 3rd party ansible roles | [@_choisec](https://twitter.com/_choisec) | Must install from directory |
| [ludus-local-users](https://github.com/Cyblex-Consulting/ludus-local-users) | Manages local users and groups for Windows or Linux | [@tigrebleu](https://infosec.exchange/@tigrebleu) | Must install from directory |
| [ludus-gitlab-ce](https://github.com/Cyblex-Consulting/ludus-gitlab-ce) | Handles the installation of a Gitlab instance | [@tigrebleu](https://infosec.exchange/@tigrebleu) | Must install from directory |
| [ludus-ad-content](https://github.com/Cyblex-Consulting/ludus-ad-content) | Creates content in an Active Directory (OUs, Groups, Users) | [@tigrebleu](https://infosec.exchange/@tigrebleu) | Must install from directory |
| [ludus_tailscale](https://github.com/NocteDefensor/ludus_tailscale) | Provision or remove a device to/from a Tailnet | [@__Mastadon](https://x.com/__Mastadon) | |
| [ludus_velociraptor_client](https://github.com/fmurer/ludus_velociraptor_client) | Install a Velociraptor Agent on a System in Ludus | [@f_Murer](https://x.com/f_Murer) | Must install from directory |
| [ludus_velociraptor_server](https://github.com/fmurer/ludus_velociraptor_server) | Install a Velociraptor Server in Ludus | [@f_Murer](https://x.com/f_Murer) | Must install from directory |
| [bagelByt3s.ludus_adfs](https://github.com/bagelByt3s/ludus_adfs) | Installs an ADFS deployment with optional configurations. | [Beyviel David](https://github.com/bagelByt3s) | Must install from directory |
| [ludus_caldera_server](https://github.com/frack113/ludus_caldera_server) | Installs [Caldera Server](https://caldera.mitre.org/) main branch on linux | [@frack113](https://x.com/frack113) | |
| [ludus_caldera_agent](https://github.com/frack113/ludus_caldera_agent) | Installs [Caldera Agent](https://caldera.mitre.org/) on Windows | [@frack113](https://x.com/frack113) | |
| [ludus_aurora_agent](https://github.com/frack113/ludus_aurora_agent) | Installs [Aurora Agent](https://www.nextron-systems.com/aurora/) on Windows | [@frack113](https://x.com/frack113) | You must have a package and a valid license (edit the role before using) |
| [ludus_graylog_server](https://github.com/frack113/my-ludus-roles) | Installs Graylog server on Ubuntu 22.04 | [@frack113](https://x.com/frack113) | Must install from directory |
| [ludus_filigran_opencti](https://github.com/frack113/ludus_filigran_opencti) | Installs [OpenCTI](https://filigran.io/solutions/open-cti/) | [@frack113](https://x.com/frack113) |  |
| [ludus_ghosts_server](https://github.com/frack113/ludus_ghosts_server) | Installs [Ghosts](https://github.com/cmu-sei/GHOSTS) on a Linux server | [@frack113](https://x.com/frack113) | |
| [0xRedpoll.ludus_cobaltstrike_teamserver](https://github.com/0xRedpoll/ludus_cobaltstrike_teamserver) | Install and provision a Cobalt Strike teamserver in Ludus | [@0xRedpoll](https://github.com/0xRedpoll) ||
| [0xRedpoll.ludus_mythic_teamserver](https://github.com/0xRedpoll/ludus_mythic_teamserver) | Installs and spins up a Mythic Teamserver on a Debian or Ubuntu server | [@0xRedpoll](https://github.com/0xRedpoll) ||
| [ludus-ad-vulns](https://github.com/Primusinterp/ludus-ad-vulns) | Adds vulnerabilities in an Active Directory. | [@Primusinterp](https://github.com/Primusinterp) | Must install from directory |
| [ludus_juiceshop](https://github.com/xurger/ludus_juiceshop) | Installs [OWASP Juice Shop](https://github.com/juice-shop/juice-shop) | [xurger](https://github.com/xurger) | Must install from directory |
