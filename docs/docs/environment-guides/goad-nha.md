---
title: "GOAD - NHA"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Game of Active Directory (GOAD) - NHA - Ninja Hacker Academy

:::success Props!

Huge shout out to [@M4yFly](https://twitter.com/M4yFly) for all the hard work to create GOAD NHA, and [@ladhaAleem](https://twitter.com/LadhaAleem) for getting GOAD NHA to work with Ludus!

:::

## Description from GOAD

- NINJA HACKER ACADEMY (NHA) is written as a training challenge where GOAD was written as a lab with a maximum of vulns.

- You should find your way in to get domain admin on the 2 domains (academy.ninja.lan and ninja.hack)

- Starting point is on srv01 : "WEB"

- Flags are disposed on each machine, try to grab all. Be careful all the machines are up to date with defender enabled.

- Some exploits needs to modify path so this lab is not very multi-players compliant (unless you do it as a team ;))

- Obviously do not cheat by looking at the passwords and flags in the recipe files, the lab must start without user to full compromise.

- Hint: No bruteforce, if not in rockyou do not waste your time and your cpu/gpu cycle.

## Community maintained Linux deployment script 

This script assumes a Linux host and that `jq` and the `ludus` client are installed

```bash

wget https://raw.githubusercontent.com/aleemladha/Ludus-Lab-Auto-Deployment/main/ludus_autodeploy_nha_lab.sh && chmod +x ludus_autodeploy_nha_lab.sh && ./ludus_autodeploy_nha_lab.sh

```
## Manual Deployment

### 1. Add the Windows 2019 template to Ludus

```bash
#terminal-command-local
git clone https://gitlab.com/badsectorlabs/ludus
#terminal-command-local
cd ludus/templates
#terminal-command-local
ludus templates add -d win2019-server-x64
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
| win2019-server-x64-template            | TRUE  |
+----------------------------------------+-------+
```

### 2. Set and deploy the following range configuration

```yaml title="config.yml"
ludus:
  - vm_name: "{{ range_id }}-NHA-DC01"
    hostname: "{{ range_id }}-DC01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 30
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-NHA-DC02"
    hostname: "{{ range_id }}-DC02"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 31
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-NHA-SRV01"
    hostname: "{{ range_id }}-SRV01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 32
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-NHA-SRV02"
    hostname: "{{ range_id }}-SRV02"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 33
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-NHA-SRV03"
    hostname: "{{ range_id }}-SRV03"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 34
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-kali"
    hostname: "{{ range_id }}-kali"
    template: kali-x64-desktop-template
    vlan: 10
    ip_last_octet: 99
    ram_gb: 4
    cpus: 4
    linux: true
    testing:
      snapshot: false
      block_internet: false	  
```

```bash
#terminal-command-local
vim config.yml
# paste in the config above (adjust cpus and ram_gb values if you have the resources to allocate more)
#terminal-command-local
ludus range config set -f config.yml
#terminal-command-local
ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```


### 3. Install ansible and its requirements for GOAD on your local machine

```shell-session
# You can use a virtualenv here if you would like
#terminal-command-local
python3 -m pip install ansible-core
#terminal-command-local
python3 -m pip install pywinrm
#terminal-command-local
git clone https://github.com/Orange-Cyberdefense/GOAD
#terminal-command-local
cd GOAD/ansible
#terminal-command-goad
ansible-galaxy install -r requirements.yml
```

### 4. Create the following inventory file and replace RANGENUMBER with your range number with sed (commands provided below)

```ini title="inventory.yml"
[default]
; Note: ansible_host *MUST* be an IPv4 address or setting things like DNS
; servers will break.
; ------------------------------------------------
; ninja.local
; ------------------------------------------------
dc01 ansible_host=10.RANGENUMBER.10.30 dns_domain=dc01 dns_domain=dc02 dict_key=dc01
dc02 ansible_host=10.RANGENUMBER.10.31 dns_domain=dc02 dict_key=dc02
srv01 ansible_host=10.RANGENUMBER.10.32 dns_domain=dc02 dict_key=srv01
srv02 ansible_host=10.RANGENUMBER.10.33 dns_domain=dc02 dict_key=srv02
srv03 ansible_host=10.RANGENUMBER.10.34 dns_domain=dc02 dict_key=srv03


[all:vars]
; domain_name : folder inside ad/
domain_name=NHA

force_dns_server=yes
dns_server=10.RANGENUMBER.10.254

two_adapters=no
; adapter created by vagrant and virtualbox (comment if you use vmware)
nat_adapter=Ethernet
domain_adapter=Ethernet

; adapter created by vagrant and vmware (uncomment if you use vmware)
; nat_adapter=Ethernet0
; domain_adapter=Ethernet1

; winrm connection (windows)
ansible_user=localuser
ansible_password=password
ansible_connection=winrm
ansible_winrm_server_cert_validation=ignore
ansible_winrm_operation_timeout_sec=400
ansible_winrm_read_timeout_sec=500

; proxy settings (the lab need internet for some install, if you are behind a proxy you should set the proxy here)
enable_http_proxy=no
ad_http_proxy=http://x.x.x.x:xxxx
ad_https_proxy=http://x.x.x.x:xxxx
```

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-goad
vim inventory.yml
# paste in the inventory file above
#terminal-command-goad
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `sudo apt install jq` if you don't have jq
#terminal-command-goad
sed -i "s/RANGENUMBER/$RANGENUMBER/g" inventory.yml
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-goad
vim inventory.yml
# paste in the inventory file above
#terminal-command-goad
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
#terminal-command-goad
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" inventory.yml
```
  </TabItem>
</Tabs>


### 5. Deploy GOAD - NINJA HACKER ACADEMY 

:::note

You must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-goad
vim build.yml
# Edit the keyboard layout to your preferred layout (or remove that whole line)
#terminal-command-goad
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/NHA/data/inventory -i ./inventory.yml"
#terminal-command-goad
export LAB="NHA"
#terminal-command-goad
../scripts/provisionning.sh
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-goad
vim build.yml
# Edit the keyboard layout to your preferred layout (or remove that whole line)
#terminal-command-goad
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/NHA/data/inventory -i ./inventory.yml"
#terminal-command-goad
export LAB="NHA"
#terminal-command-goad
export OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES
#terminal-command-goad
../scripts/provisionning.sh
```
  </TabItem>
</Tabs>

Now you wait. `[WARNING]` lines are ok, and some steps may take a long time, don't panic!

This will take a few hours. You'll know it is done when you see:

```
your lab : NHA is successfully setup ! have fun ;)
```

### 6. Snapshot VMs

Take snapshots via the proxmox web UI or SSH into ludus and as root run the following

```bash
export RANGEID=JD # <= change to your user ID
vms=("$RANGEID-NHA-DC01" "$RANGEID-NHA-DC02" "$RANGEID-NHA-SRV01" "$RANGEID-NHA-SRV02" "$RANGEID-NHA-SRV03")
COMMENT="Clean NHA setup after ansible run"
# Loop over the array
for vm in "${vms[@]}"
do
  echo "[+] Create snapshot for $vm"
  id=$(qm list | grep $vm  | awk '{print $1}')
  echo "[+] VM id is : $id"
  qm snapshot "$id" 'snapshot-'$(date '+%Y-%m-%d--%H-%M') --vmstate 1 --description "$COMMENT"
done
```

### 7. Hack!

Access your Kali machine at `https://10.RANGENUMBER.10.99:8444` using the creds `kali:password`.
