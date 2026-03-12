# GHOSTS Client

This role installs and configures the GHOSTS user emulation client on a Windows machine

Supported platforms:

- Windows 11
- Windows 10
- Windows Server 2022
- Windows Server 2019
- Windows Server 2016
- Windows Server 2012R2

It automatically finds the server by searching the range for the VM with the role `ludus_ghosts_server`. It can be manually specified with `ghosts_server_url`.

Windows clients will be rebooted.

## Role Variables

Available variables are listed below, along with default values.

```yaml
ludus_ghosts_client_install_geckodriver: true
ludus_ghosts_client_geckodriver_version: 0.36.0
ludus_ghosts_client_geckodriver_prefix_linux: /usr/local/bin
ludus_ghosts_client_geckodriver_prefix_windows: C:\Program Files\geckodriver
ludus_ghosts_client_install_chromedriver: true
ludus_ghosts_client_chromedriver_version_url: http://chromedriver.storage.googleapis.com/LATEST_RELEASE
ludus_ghosts_client_windows_64bit_client_url: https://cmu.box.com/shared/static/kqo5cl7f5f2v22xgud6o2fd26xrrwtpq.zip
ludus_ghosts_client_windows_32bit_client_url: https://cmu.box.com/shared/static/9oxugvdbiixbs1dtt98s609nbpbmpj7h.zip
ludus_ghosts_client_windows_lite_client_url: https://cmu.box.com/s/2nu9fvzkpp4ku7o2d4uk82lozpkacatn
ludus_ghosts_client_linux_client_url: https://cmu.box.com/shared/static/tdozbmmdyvajubohwtnon1huyfqjuwrz.zip
ludus_ghosts_client_prefix_windows: C:\ludus\ghosts_client
ludus_ghosts_client_prefix_linux: /opt/ghosts_client
ludus_ghosts_client_custom_timeline_json: # path to a custom timeline.json file on the Ludus host
ludus_ghosts_run_key_task_name: "ghosts"
```


## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-CLIENT-WIN10"
    hostname: "{{ range_id }}-CLIENT-WIN10"
    template: win10-21h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 20
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: member
    roles:
      - ludus_ghosts_client
    role_vars:
      
```
