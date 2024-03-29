---
title: "GOAD - SCCM"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Game of Active Directory (GOAD) - SCCM

:::success Props!

Huge shout out to [@M4yFly](https://twitter.com/M4yFly) for all the hard work to create GOAD SCCM, and Errorix404 on the [Ludus Discord](https://discord.gg/HryzhdUSYT) for getting GOAD SCCM to work with Ludus!

:::

![GOAD SCCM Network Map](https://raw.githubusercontent.com/Orange-Cyberdefense/GOAD/main/docs/img/SCCMLAB_overview.png)

### 1. Add the Windows 2019 template to Ludus

```plain
local:~$ git clone https://gitlab.com/badsectorlabs/ludus
local:~$ cd ludus/templates
local:~$ ludus templates add -d win2019-server-x64
[INFO]  Successfully added template
local:~$ ludus templates build
[INFO]  Template building started - this will take a while. Building 1 template(s) at a time.
# Wait until the templates finish building, you can monitor them with `ludus templates logs -f` or `ludus templates status`
local:~$ ludus templates list
+----------------------------------------+-------+
|                TEMPLATE                | BUILT |
+----------------------------------------+-------+
| debian-11-x64-server-template          | TRUE  |
| debian-12-x64-server-template          | TRUE  |
| kali-x64-desktop-template              | TRUE  |
| win11-22h2-x64-enterprise-template     | TRUE  |
| win2022-server-x64-template            | TRUE  |
| win2019-server-x64-template            | TRUE  |
+----------------------------------------+-------+
```

### 2. Set and deploy the following range configuration

```plain title="config.yml"
ludus:
  - vm_name: "{{ range_id }}-SCCM-DC"
    hostname: "{{ range_id }}-DC01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 40
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-SCCM-MECM"
    hostname: "{{ range_id }}-SRV01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 41
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-SCCM-MSSQL"
    hostname: "{{ range_id }}-SRV02"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 42
    ram_gb: 4
    cpus: 4
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-SCCM-CLIENT"
    hostname: "{{ range_id }}-WS01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 43
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-kali"
    hostname: "{{ range_id }}-kali"
    template: kali-x64-desktop-template
    vlan: 99
    ip_last_octet: 1
    ram_gb: 4
    cpus: 4
    linux: true
    testing:
      snapshot: false
      block_internet: false
```

```plain
local:~$ vim config.yml
# paste in the config above (adjust cpus and ram_gb values if you have the resources to allocate more)
local:~$ ludus range config set -f config.yml
local:~$ ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```


### 3. Install ansible and its requirements for GOAD on your local machine

```
# You can use a virtualenv here if you would like
local:~$ python3 -m pip install ansible-core
local:~$ python3 -m pip install pywinrm
local:~$ git clone https://github.com/Orange-Cyberdefense/GOAD
local:~$ cd GOAD/ansible
local:~/GOAD/ansible$ ansible-galaxy install -r requirements.yml
```

### 4. Create the following inventory file and replace RANGENUMBER with your range number with sed (commands provided below)

```plain title="inventory.yml"
[default]
; Note: ansible_host *MUST* be an IPv4 address or setting things like DNS
; servers will break.
; ------------------------------------------------
; sccm.lab
; ------------------------------------------------
dc01 ansible_host=10.RANGENUMBER.10.40 dns_domain=dc01 dict_key=dc01
srv01 ansible_host=10.RANGENUMBER.10.41 dns_domain=dc01 dict_key=srv01
srv02 ansible_host=10.RANGENUMBER.10.42 dns_domain=dc01 dict_key=srv02
ws01 ansible_host=10.RANGENUMBER.10.43 dns_domain=dc01 dict_key=ws01

[all:vars]
force_dns_server=yes
dns_server=10.RANGENUMBER.10.254

two_adapters=no
; adapter created by proxmox (change them if you get an error)
; to get the name connect to one vm and run ipconfig it will show you the adapters name
nat_adapter=Ethernet
domain_adapter=Ethernet

; winrm connection (windows)
ansible_user=localuser
ansible_password=password
ansible_connection=winrm
ansible_winrm_server_cert_validation=ignore
ansible_winrm_operation_timeout_sec=400
ansible_winrm_read_timeout_sec=500
```

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```
local:~/GOAD/ansible$ vim inventory.yml
# paste in the inventory file above
local:~/GOAD/ansible$ export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `sudo apt install jq` if you don't have jq
local:~/GOAD/ansible$ sed -i "s/RANGENUMBER/$RANGENUMBER/g" inventory.yml
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```
local:~/GOAD/ansible$ vim inventory.yml
# paste in the inventory file above
local:~/GOAD/ansible$ export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
local:~/GOAD/ansible$ sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" inventory.yml
```
  </TabItem>
</Tabs>

### 5. A little manual work

Until [this pull request](https://github.com/Orange-Cyberdefense/GOAD/pull/206) is merged, you'll need to make the following changes:

1. Edit the IIS install task to start the windows update service

```
local:~/GOAD/ansible$ vim roles/sccm/install/iis/tasks/main.yml

# Add the following at line 24 (between the 'Reboot if installing windows feature requires it' task and the 'install .NET Framework 3.5 with DISM' task)
- name: Enable update service
  ansible.windows.win_service:
    name: Windows Update
    state: started
    start_mode: auto
```

### 6. Deploy GOAD

:::note

You must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```
local:~/GOAD/ansible$ vim build.yml
# Edit the keyboard layout to your preferred layout (or remove that whole line)
local:~/GOAD/ansible$ export ANSIBLE_COMMAND="ansible-playbook -i ../ad/SCCM/data/inventory -i ./inventory.yml"
local:~/GOAD/ansible$ export LAB="SCCM"
local:~/GOAD/ansible$ ../scripts/provisionning.sh
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```
local:~/GOAD/ansible$ vim build.yml
# Edit the keyboard layout to your preferred layout (or remove that whole line)
local:~/GOAD/ansible$ export ANSIBLE_COMMAND="ansible-playbook -i ../ad/SCCM/data/inventory -i ./inventory.yml"
local:~/GOAD/ansible$ export LAB="SCCM"
local:~/GOAD/ansible$ export OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES
local:~/GOAD/ansible$ ../scripts/provisionning.sh
```
  </TabItem>
</Tabs>

Now you wait. `[WARNING]` lines are ok, and some steps may take a long time, don't panic!

This will take a few hours. You'll know it is done when you see:

```
your lab : SCCM is successfully setup ! have fun ;)
```


### 7. Snapshot VMs

Take snapshots via the proxmox web UI or SSH into ludus and as root run the following

```
export RANGEID=JD # <= change to your ID
vms=("$RANGEID-SCCM-DC" "$RANGEID-SCCM-MECM" "$RANGEID-SCCM-MSSQL" "$RANGEID-SCCM-CLIENT")
COMMENT="Clean GOAD SCCM setup after ansible run"
# Loop over the array
for vm in "${vms[@]}"
do
  echo "[+] Create snapshot for $vm"
  id=$(qm list | grep $vm  | awk '{print $1}')
  echo "[+] VM id is : $id"
  qm snapshot "$id" 'snapshot-'$(date '+%Y-%m-%d--%H-%M') --vmstate 1 --description "$COMMENT"
done
```

### 8. Hack!

Access your Kali machine at `http://10.RANGENUMBER.99.1:8444` using the creds `kali:password`.
