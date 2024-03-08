---
title: "Vulhub"
---

# Vulhub

1. Add the [vulhub role](https://github.com/badsectorlabs/ludus_vulhub) to your Ludus server

```
ludus ansible roles add badsectorlabs.ludus_vulhub
```

2. Edit your config to add vulhub to a debian based VM and define which environments you wish to run

```yaml title="config.yml"
ludus:
  - vm_name: "{{ range_id }}-vulhub"
    hostname: "{{ range_id }}-vulhub"
    template: debian-12-x64-server-template
    vlan: 20
    ip_last_octet: 1
    ram_gb: 4
    cpus: 2
    linux: true
    testing:
      snapshot: false
      block_internet: false
    // highlight-next-line
    roles:
      // highlight-next-line
      - badsectorlabs.ludus_vulhub
    // highlight-next-line
    role_vars:
      // highlight-next-line
      vulhub_envs:
        // highlight-next-line
        - confluence/CVE-2023-22527
        // highlight-next-line
        - airflow/CVE-2020-11978
```

```
ludus ansible roles add badsectorlabs.ludus_vulhub
ludus range config get > config.yml
# Edit config to add the role to the VMs you wish to install vulhub on and define your desired vulhub_envs (see above)
ludus range config set -f config.yml
```

3. Deploy the range

:::note

The `user-defined-roles` tag will only run the ansible to add roles to VMs
you can always run a "full deploy" without any -t argument if you wish, but it will run every step

:::

```
ludus range deploy -t user-defined-roles
```