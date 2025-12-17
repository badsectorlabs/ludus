---
title: "Netexec Workshop (leHACK 2025)"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Netexec Workshop (leHACK 2025)

:::success Props!

Huge shout out to [@ladhaAleem](https://twitter.com/LadhaAleem) for creating this project and converting the leHACK 2025 workshop created by [@mpgn_x64](https://x.com/mpgn_x64) to an ansible playbook and making it work with Ludus as well!
:::

## Description from leHACK 2025

Welcome to the NetExec Active Directory Lab! This lab is designed to teach you how to exploit Active Directory (AD) environments using the powerful tool [NetExec](https://github.com/Pennyw0rth/NetExec).

Originally featured in the leHACK 2025 Workshop, this lab is now available for free to everyone! In this lab, you’ll explore how to use the powerful tool NetExec to efficiently compromise an Active Directory domain during an internal pentest.

The ultimate goal? Become Domain Administrator by following various attack paths, using nothing but NetExec and maybe BloodHound (Why not :P).

Obviously do not cheat by looking at the passwords and flags in the recipe files, the lab must start without user to full compromise

**Note**: One change has been made on this lab regarding the workshop, the part using msol module on nxc is replaced with a dump of lsass. The rest is identical.


### Public Writeups

- https://blog.anh4ckin.ch/posts/netexec-workshop2k25/ by [@LeandreOnizuka](https://x.com/LeandreOnizuka)



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

### 2. Deploy VMs

Set and deploy the configuration for the lab.

```bash
#terminal-command-local
git clone https://github.com/Pennyw0rth/NetExec-Lab
#terminal-command-local
ludus range config set -f NetExec-Lab/LEHACK-2025/ad/LEHACK/providers/ludus/config.yml
#terminal-command-local
ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```


### 3. Install requirements

Install ansible and its requirements for the NetExec lab on your local machine.

```shell-session
# You can use a virtualenv here if you would like
#terminal-command-local
python3 -m pip install ansible-core
#terminal-command-local
python3 -m pip install pywinrm
#terminal-command-local
git clone https://github.com/Pennyw0rth/NetExec-Lab
#terminal-command-local
cd LEHACK-2025/ansible
#terminal-command-local
ansible-galaxy install -r requirements.yml
```

### 4. Setup  the inventory files

The inventory file is already present in the providers folder and replace RANGENUMBER with your range number with sed (commands provided below)


<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-local
cd LEHACK-2025/ansible
# go the the ansible directory as above
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `sudo apt install jq` if you don't have jq
#terminal-command-local
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory.yml
#terminal-command-local
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory_disableludus.yml
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-local
cd LEHACK-2025/ansible
# paste in the inventory file above
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
#terminal-command-local
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory.yml
#terminal-command-local
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/LEHACK/providers/ludus/inventory_disableludus.yml
```
  </TabItem>
</Tabs>


### 5. Deploy the NetExec Workshop

:::note

If not running on the Ludus host, you must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-local
cd LEHACK-2025/ansible
# in the ansible folder perform the following
#terminal-command-local
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/LEHACK/data/inventory -i ../ad/LEHACK/providers/ludus/inventory.yml"
#terminal-command-local
export LAB="LEHACK"
#terminal-command-local
chmod +x ../scripts/provisionning.sh
#terminal-command-local
../scripts/provisionning.sh
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-local
cd LEHACK-2025/ansible
# In the ansible folder perform the following
#terminal-command-local
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/LEHACK/data/inventory -i ../ad/LEHACK/providers/ludus/inventory.yml"
#terminal-command-local
export LAB="LEHACK"
#terminal-command-local
export OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES
#terminal-command-local
../scripts/provisionning.sh
```
  </TabItem>
</Tabs>

Now you wait. `[WARNING]` lines are ok, and some steps may take a long time, don't panic!

This will take a few hours. You'll know it is done when you see:

```
May the gods of Gaul guide you as you embark on this dangerous quest!
```

### 5. Disable localuser

Once install has finished disable localuser user to avoid using it and avoid unintended secrets stored (*I'm looking at you Lsassy*).

:::note

You must be connected to your Ludus wireguard VPN for these commands to work

:::
```bash
# Still in the LEHACK-2025/ansible directory
ansible-playbook -i ../ad/LEHACK/providers/ludus/inventory_disableludus.yml disable_localuser.yml reboot.yml rebootsrv01.yml
```


### 6. Snapshot VMs

Take snapshots via the proxmox web UI or SSH run the following ludus command:

```bash
ludus snapshot create clean-setup -d "Clean setup of the netexec lab after ansible run"
```

### 7. Hack!

Access your Kali machine at `https://10.RANGENUMBER.10.99:8444` using the creds `kali:password`.

![Network Diagram](/img/envs/netexec.png)
