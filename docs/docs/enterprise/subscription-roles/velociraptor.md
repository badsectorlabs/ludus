# Velociraptor 

Installs and configures [Velociraptor](https://github.com/Velocidex/velociraptor).

## OS Platforms

This role has been tested on the following operating systems:

- Debian 12 (server)
- Ubuntu 20.04, 22.04 (client)
- Red Hat Enterprise Linux 7, 8 (client)
- Windows 10, 11, 2016, 2019, 2022 (client)


## Role Variables

```yaml
# whether or not you want to install/uninstall the server/client on that VM
velociraptor_install_or_uninstall: install

# Identifies nodes which should receive the Velociraptor Client
velociraptor_client: false

# Identifies nodes which should receive the Velociraptor Server
velociraptor_server: false

# What Velocirapter version to install
velociraptor_version: "v0.72.0"

# only if patch version and difference in versions used in download url
# leave undefined or empty else
velociraptor_version_topdir:
velociraptor_version_patch:

# Admin username for webgui
velociraptor_admin_user: admin

# Admin password for webgui
velociraptor_admin_password: changeme


velociraptor_host: "{{ ansible_default_ipv4.address }}"
velociraptor_port: 8000
velociraptor_selfsigned_ssl: true
velociraptor_gui_use_plain_http: false
velociraptor_frontend_host: 0.0.0.0
velociraptor_frontend_port: 8000
velociraptor_frontend_base_path: ''
velociraptor_frontend_use_plain_http: false
velociraptor_prevent_execve: false
velociraptor_api_host: '0.0.0.0'
velociraptor_api_port: 8001
velociraptor_gui_host: '0.0.0.0'
velociraptor_gui_port: 8889
velociraptor_datastore_path: /var/tmp/velociraptor

# This will set proxy in systemd environment
# alternative: https://github.com/Velocidex/velociraptor/blob/master/docs/references/server.config.yaml#L453
velociraptor_webproxy_host: ''
velociraptor_webproxy_port: ''
velociraptor_webproxy_ignore: ''

# using a reverse proxy?
velociraptor_reverseproxy: false
# https://github.com/Velocidex/velociraptor/pull/54
velociraptor_reverseproxy_proxy_header: X-Real-IP

velociraptor_is_container: false
```

## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: primary-dc
  
  - vm_name: "{{ range_id }}-vraptor"
    hostname: "{{ range_id }}-vraptor"
    template: debian-12-x64-server-template
    vlan: 10
    ip_last_octet: 50
    ram_gb: 8
    cpus: 4
    linux: true
    roles:
      - ludus_velociraptor
    role_vars:
      velociraptor_admin_user: admin
      velociraptor_admin_password: superstrongpassword
      velociraptor_server: true

  - vm_name: "{{ range_id }}-WIN11-01"
    hostname: "{{ range_id }}-WIN11-22H2-1"
    template: win11-22h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 21
    ram_gb: 8
    cpus: 4
    windows:
      install_additional_tools: true
      office_version: 2019
      office_arch: 64bit
    domain:
      fqdn: ludus.domain
      role: member
    roles:
      - ludus_velociraptor
    role_vars:
      velociraptor_client: true

  - vm_name: "{{ range_id }}-debian-12"
    hostname: "{{ range_id }}-debian-12"
    template: debian-12-x64-server-template
    vlan: 10
    ip_last_octet: 22
    ram_gb: 2
    cpus: 2
    linux: true
    roles:
      - ludus_velociraptor
    role_vars:
      velociraptor_client: true
```

## Credit

This role is based on [ansible_velociraptor](https://github.com/juju4/ansible_velociraptor) with the following license:

```
MIT License

Copyright (c) 2021 Chad Zimmerman

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```