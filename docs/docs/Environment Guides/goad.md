---
title: "Game of Active Directory (GOAD)"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Game of Active Directory (GOAD)

:::success Props!

Huge shout out to [@M4yFly](https://twitter.com/M4yFly) for all the hard work to create GOAD!

:::

![GOAD Network Map](https://raw.githubusercontent.com/Orange-Cyberdefense/GOAD/main/docs/img/GOAD_schema.png)

### 1. Add the Windows 2019 and 2016 server templates to Ludus

```bash
#terminal-command-local
git clone https://gitlab.com/badsectorlabs/ludus
#terminal-command-local
cd ludus/templates
#terminal-command-local
ludus templates add -d win2016-server-x64
[INFO]  Successfully added template
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
| win2016-server-x64-template            | TRUE  |
+----------------------------------------+-------+
```

### 2. Set and deploy the following range configuration

```yaml title="config.yml"
ludus:
  - vm_name: "{{ range_id }}-GOAD-DC01"
    hostname: "{{ range_id }}-DC01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 10
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-GOAD-DC02"
    hostname: "{{ range_id }}-DC02"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-GOAD-DC03"
    hostname: "{{ range_id }}-DC03"
    template: win2016-server-x64-template
    vlan: 10
    ip_last_octet: 12
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-GOAD-SRV02"
    hostname: "{{ range_id }}-SRV02"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 22
    ram_gb: 4
    cpus: 2
    windows:
      sysprep: true
  - vm_name: "{{ range_id }}-GOAD-SRV03"
    hostname: "{{ range_id }}-SRV03"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 23
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
    cpus: 2
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

### 3. Update the SRV02 machine

```bash
#terminal-command-local
ludus testing update -n JD-GOAD-SRV02 # replace "JD" with your range ID
#terminal-command-local
ludus range logs -f
# Wait for all updates to be installed. 
# Be patient, this will take a long time.
# This required for the IIS install to work during GOAD setup.

# When you see the following, the updates are complete:
localhost                  : ok=5    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0   
JD-GOAD-SRV02              : ok=8    changed=5    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0 
```

### 4. Install ansible and its requirements for GOAD on your local machine

```bash
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

### 5. Create the following inventory file and replace RANGENUMBER with your range number with sed (commands provided below)

```ini title="inventory.yml"
[default]
; Note: ansible_host *MUST* be an IPv4 address or setting things like DNS
; servers will break.
; ------------------------------------------------
; sevenkingdoms.local
; ------------------------------------------------
dc01 ansible_host=10.RANGENUMBER.10.10 dns_domain=dc01 dict_key=dc01
;ws01 ansible_host=10.RANGENUMBER.10.30 dns_domain=dc01 dict_key=ws01
; ------------------------------------------------
; north.sevenkingdoms.local
; ------------------------------------------------
dc02 ansible_host=10.RANGENUMBER.10.11 dns_domain=dc01 dict_key=dc02
srv02 ansible_host=10.RANGENUMBER.10.22 dns_domain=dc02 dict_key=srv02
; ------------------------------------------------
; essos.local
; ------------------------------------------------
dc03 ansible_host=10.RANGENUMBER.10.12 dns_domain=dc03 dict_key=dc03
srv03 ansible_host=10.RANGENUMBER.10.23 dns_domain=dc03 dict_key=srv03

[all:vars]
; domain_name : folder inside ad/
domain_name=GOAD

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

### 6. Deploy GOAD

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
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/GOAD/data/inventory -i ./inventory.yml"
#terminal-command-goad
export LAB="GOAD"
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
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/GOAD/data/inventory -i ./inventory.yml"
#terminal-command-goad
export LAB="GOAD"
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
your lab: GOAD is successfully setup ! have fun ;)
```

:::tip It's always DNS...

If you encounter errors with `TASK [groups_domains : synchronizes all domains]` or similar, manually remove the `10.ID.10.254` entry from the DNS servers for the host. You can do this via the GUI (Network and Internet -> Change Adaptor Options -> Right-click -> Properties -> Internet Protocol Version 4 (TCP/IPv4) -> Properties) or via Powershell:

```powershell
# Run this on the failing host
$adapter = Get-WmiObject Win32_NetworkAdapterConfiguration | Where-Object { $_.IPAddress -ne $null }
$dnsServers = $adapter.DNSServerSearchOrder
$newDnsServers = $dnsServers | Where-Object { $_ -notmatch ".*\.254$" }
$adapter.SetDNSServerSearchOrder($newDnsServers)
```

:::

### 7. Snapshot VMs

Take snapshots via the proxmox web UI or SSH into ludus and as root run the following

```bash
export RANGEID=JD # <= change to your ID
vms=("$RANGEID-GOAD-DC01" "$RANGEID-GOAD-DC02" "$RANGEID-GOAD-DC03" "$RANGEID-GOAD-SRV02" "$RANGEID-GOAD-SRV03")
COMMENT="Clean GOAD setup after ansible run"
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

Follow [the GOAD guide](https://mayfly277.github.io/posts/GOADv2-pwning_part1/) or explore the network on your own.