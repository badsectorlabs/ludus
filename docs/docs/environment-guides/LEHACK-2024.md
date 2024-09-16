---
title: "LEHACK 2024 WORKSHOP"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# LEHACK FREE Workshop 2024 (Active Directory pwnage with NetExec)

:::success Props!

Huge shout out to  [@ladhaAleem](https://twitter.com/LadhaAleem) for creating this project and converting the LEHACK-2024 workshop created by [@mpgn_x64](https://x.com/mpgn_x64)  to an ansible playbook and making it work with LUDUS as well
:::

## Description from LEHACK-2024

Welcome to the NetExec Active Directory Lab! This lab is designed to teach you how to exploit Active Directory (AD) environments using the powerful tool NetExec.

Originally featured in the LeHack2024 Workshop, this lab is now available for free to everyone! In this lab, you’ll explore how to use the powerful tool NetExec to efficiently compromise an Active Directory domain during an internal pentest.

The ultimate goal? Become Domain Administrator by following various attack paths, using nothing but NetExec! and Maybe BloodHound (Why not :P)

Obviously do not cheat by looking at the passwords and flags in the recipe files, the lab must start without user to full compromise

Note: On change has been made on this lab regarding the workshop, the part using msol module on nxc is replaced with a dump of lsass. The rest is identical.

**Note**: On change has been made on this lab regarding the workshop, the part using msol module on nxc is replaced with a dump of lsass. The rest is identical.

### Original pitch

The Gallic camp was attacked by the Romans and it seems that a traitor made this attack possible! Two domains must be compromised to find it 🔥

### Public Writeups

- https://www.rayanle.cat/lehack-2024-netexec-workshop-writeup/ by [https://x.com/rayanlecat](@rayanlecat)
- https://blog.lasne.pro/posts/netexec-workshop-lehack2024/ by [https://x.com/0xFalafel](@0xFalafel)



## Deployment

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


```bash
# In the config above (adjust cpus and ram_gb values if you have the resources to allocate more 2gb ram is enough)
#terminal-command-local
ludus range config set -f LeHack-2024/ad/LEHACK/providers/ludus/config.yml
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
git clone https://github.com/Pennyw0rth/NetExec-Lab
#terminal-command-local
cd LeHack-2024/ansible
#terminal-command-goad
ansible-galaxy install -r requirements.yml
```

### 4. The inventory file is already present in the providers folder and replace RANGENUMBER with your range number with sed (commands provided below)


<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-goad
cd LeHack-2024/ansible
# go the the ansible directory as above
#terminal-command-goad
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `sudo apt install jq` if you don't have jq
#terminal-command-goad
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory.yml
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-goad
cd LeHack-2024/ansible
# paste in the inventory file above
#terminal-command-goad
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
#terminal-command-goad
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory.yml
```
  </TabItem>
</Tabs>


### 5. Deploy LEHACK FREE Workshop 2024 

:::note

You must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-goad
cd LeHack-2024/ansible
# in the ansible folder perform the following
#terminal-command-goad
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/LEHACK/data/inventory -i ../ad/LEHACK/providers/ludus/inventory.yml"
#terminal-command-goad
export LAB="LEHACK"
#terminal-command-goad
../scripts/provisionning.sh
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-goad
cd LeHack-2024/ansible
# In the ansible folder perform the following
#terminal-command-goad
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/LEHACK/data/inventory -i ../ad/LEHACK/providers/ludus/inventory.yml"
#terminal-command-goad
export LAB="LEHACK"
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
your lab : LEHACK is successfully setup ! have fun ;)
```

### 5. Once install has finished disable localuser user to avoid using it and avoid unintended secrets stored / am looking at you Lsassy

:::note

You must be connected to your Ludus wireguard VPN for these commands to work

:::
```bash
cd LeHack-2024/ansible
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory_disableludus.yml
ansible-playbook -i ../ad/LEHACK/providers/ludus/inventory_disableludus.yml disable_localuser.yml reboot.yml
```


### 6. Snapshot VMs

Take snapshots via the proxmox web UI or SSH into ludus and as root run the following

```bash
export RANGEID=JD # <= change to your ID
vms=("$RANGEID-LEHACK-DC01" "$RANGEID-LEHACK-DC02" "$RANGEID-LEHACK-SRV01" "$RANGEID-LEHACK-SRV02")
COMMENT="Clean LEHACK setup after ansible run"
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

Access your Kali machine at `http://10.RANGENUMBER.10.99:8444` using the creds `kali:password`.
