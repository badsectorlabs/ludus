---
title: "ADCS"
---

# Active Directory Certificate Services Lab

1. Clone down the [ludus_adcs](https://github.com/bad-sector-labs/ludus_adcs) ansible role from github (soon to be published on Ansible Galaxy).

```
local:~$ git clone https://github.com/bad-sector-labs/ludus_adcs
```

2. Add the ludus_adcs role to your Ludus server

```
local:~$ ludus ansible roles add -d ludus_adcs
```

3. Modify your ludus config to add the role to a Windows server VM

```
local:~$ ludus range config get > config.yml
```

```yaml title="config.yml"
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
    // highlight-next-line
    roles:
    // highlight-next-line
      - ludus_adcs
```

```
local:~$ ludus range config set -f config.yml
```

4. Deploy the range

```
local:~$ ludus range deploy
```

5. Enjoy your ESC1,2,3,4,6,8, and 13 attack paths!

![ESC Templates](/img/envs/adcs-templates.png)
