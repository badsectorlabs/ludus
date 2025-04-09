---
title: Splunk Attack Range
---

# Splunk Attack Range

This guide will create a [Splunk Attack Range](https://github.com/splunk/attack_range).

1. Add the different roles to your Ludus server

```shell-session
#terminal-command-local
ludus ansible roles add p4t12ick.ludus_ar_splunk
#terminal-command-local
ludus ansible roles add p4t12ick.ludus_ar_windows
#terminal-command-local
ludus ansible roles add p4t12ick.ludus_ar_linux
```

2. Add and build the Ubuntu 22.04 server template

```bash
#terminal-command-local
git clone https://gitlab.com/badsectorlabs/ludus
#terminal-command-local
cd ludus/templates
#terminal-command-local
ludus templates add -d ubuntu-22.04-x64-server
[INFO]  Successfully added template
#terminal-command-local
ludus templates build
[INFO]  Template building started - this will take a while. Building 1 template(s) at a time.
# Wait until the templates finish building, you can monitor them with `ludus templates logs -f` or `ludus templates status`
#terminal-command-local
ludus templates list
+----------------------------------------+-------+
|                TEMPLATE                | BUILT |
+----------------------------------------+-------+
| debian-11-x64-server-template          | TRUE  |
| debian-12-x64-server-template          | TRUE  |
| kali-x64-desktop-template              | TRUE  |
| win11-22h2-x64-enterprise-template     | TRUE  |
| win2022-server-x64-template            | TRUE  |
| ubuntu-22.04-x64-server-template       | TRUE  |
+----------------------------------------+-------+
```

3. Modify your ludus config to add the `p4t12ick.ludus_ar_splunk` role to a Ubuntu VM, the `p4t12ick.ludus_ar_windows` on Windows VMs and the `p4t12ick.ludus_ar_linux` on Ubuntu VM.

```shell-session
#terminal-command-local
ludus range config get > config.yml
```

```yaml title="config.yml"
ludus:
  - vm_name: "{{ range_id }}-ar-splunk"
    hostname: "{{ range_id }}-ar-splunk"
    template: ubuntu-22.04-x64-server-template
    vlan: 20
    ip_last_octet: 1
    ram_gb: 16
    cpus: 8
    linux: true
    // highlight-next-line
    roles:
    // highlight-next-line
      - p4t12ick.ludus_ar_splunk

  - vm_name: "{{ range_id }}-ar-windows"
    hostname: "{{ range_id }}-ar-windows"
    template: win2022-server-x64-template
    vlan: 20
    ip_last_octet: 3
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    // highlight-next-line
    roles:
    // highlight-next-line
      - p4t12ick.ludus_ar_windows
    // highlight-next-line
    role_vars:
    // highlight-next-line
      ludus_ar_windows_splunk_ip: "10.2.20.1"

  - vm_name: "{{ range_id }}-ar-linux"
    hostname: "{{ range_id }}-ar-linux"
    template: ubuntu-22.04-x64-server-template
    vlan: 20
    ip_last_octet: 2
    ram_gb: 8
    cpus: 4
    linux: true
    // highlight-next-line
    roles:
    // highlight-next-line
      - p4t12ick.ludus_ar_linux
    // highlight-next-line
    role_vars:
    // highlight-next-line
      ludus_ar_linux_splunk_ip: "10.2.20.1"

```

```shell-session
#terminal-command-local
ludus range config set -f config.yml
```

:::note

Make sure that the `ludus_ar_windows_splunk_ip` and `ludus_ar_linux_splunk_ip` are set to the IP address of the Splunk server.

:::

4. Deploy the range

```shell-session
#terminal-command-local
ludus range deploy
```

5. Have fun with your Splunk Attack Range. You can access the Splunk web interface via HTTP on port 8000 (`http://10.2.20.1:8000` in this example). The default username and password are `admin:changeme123!`.
![Splunk Attack Range](https://github.com/splunk/attack_range/blob/develop/docs/attack_range.png?raw=true)
