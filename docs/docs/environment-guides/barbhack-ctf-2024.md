---
title: "BarbHack CTF 2024 (Gotham City - Active Directory Lab)"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# BarbHack CTF 2024 (Gotham City - Active Directory Lab)

:::success Props!

Huge shout out to [@ladhaAleem](https://twitter.com/LadhaAleem) converting the "BarbHack CTF 2024 (Gotham City - Active Directory Lab)" workshop created by [@mpgn_x64](https://x.com/mpgn_x64) to an ansible playbook and making it work with Ludus as well!

:::

## Description from BarbHack CTF 2024

Welcome to the NetExec Active Directory Lab! This lab is designed to teach you how to exploit Active Directory (AD) environments using the powerful tool NetExec.

Originally featured in the Barbhack 2024 CTF, this lab is now available for free to everyone! In this lab, you’ll explore how to use the powerful tool NetExec to efficiently compromise an Active Directory domain during an internal pentest.

The ultimate goal? Become Domain Administrator by following various attack paths, using nothing but NetExec! and Maybe BloodHound (Why not?) 

Obviously do not cheat by looking at the passwords and flags in the recipe files, the lab must start without user to full compromise.

:::note

Use nothing but NetExec! and Maybe BloodHound (Why not?) 

:::

Have fun !

## Deployment

### 1. Deploy VMs

Set and deploy the configuration for the lab.

```bash
#terminal-command-local
git clone https://github.com/Pennyw0rth/NetExec-Lab
#terminal-command-local
ludus range config set -f NetExec-Lab/BARBHACK-2024/ad/BARBHACK/providers/ludus/config.yml
#terminal-command-local
ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```

### 2. Install requirements

:::warning

If you are running this guide on the Ludus host you can skip this step, it already has all the requirements.

:::

Install ansible and its requirements for the BarbHack lab on your local machine.

```shell-session
# You can use a virtualenv here if you would like
#terminal-command-local
python3 -m pip install ansible-core
#terminal-command-local
python3 -m pip install pywinrm
#terminal-command-local
cd NetExec-Lab/BARBHACK-2024/ansible
#terminal-command-local
ansible-galaxy install -r requirements.yml
```

### 4. Setup  the inventory files

The inventory file is already present in the providers folder and replace RANGENUMBER with your range number with sed (commands provided below)


<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux or Ludus host">
```bash
#terminal-command-local
cd NetExec-Lab/BARBHACK-2024/ansible
# go the the ansible directory as above
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `sudo apt install jq` if you don't have jq
#terminal-command-local
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/BARBHACK/providers/ludus/inventory.yml
#terminal-command-local
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/BARBHACK/providers/ludus/inventory_disableludus.yml
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-local
cd NetExec-Lab/BARBHACK-2024/ansible
# paste in the inventory file above
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
#terminal-command-local
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/BARBHACK/providers/ludus/inventory.yml
#terminal-command-local
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/BARBHACK/providers/ludus/inventory_disableludus.yml
```
  </TabItem>
</Tabs>


### 5. Deploy the BarbHack Workshop

:::note

If not running on the Ludus host, you must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux or Ludus host">
```bash
# in the ansible folder perform the following
#terminal-command-local
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/BARBHACK/data/inventory -i ../ad/BARBHACK/providers/ludus/inventory.yml"
#terminal-command-local
export LAB="BARBHACK"
#terminal-command-local
chmod +x ../scripts/provisionning.sh
#terminal-command-local
../scripts/provisionning.sh
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
# In the ansible folder perform the following
#terminal-command-local
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/BARBHACK/data/inventory -i ../ad/BARBHACK/providers/ludus/inventory.yml"
#terminal-command-local
export LAB="BARBHACK"
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
Gotham needs you! A villain is still at large in the shadows. It's your mission to track them down!
```

### 5. Disable localuser

Once install has finished disable localuser user to avoid using it and avoid unintended secrets stored (*I'm looking at you Lsassy*).

:::note

If not running on the Ludus host, you must be connected to your Ludus wireguard VPN for this command to work

:::
```bash
# Still in the BARBHACK-2024/ansible directory
ansible-playbook -i ../ad/BARBHACK/providers/ludus/inventory_disableludus.yml disable_localuser.yml reboot.yml
```



### 5. Snapshot VMs

Take snapshots via the proxmox web UI or run the following ludus command

```bash
ludus snapshot create clean-setup -d "Clean BarbHack Lab setup after ansible run"
```

### 6. Hack!

Access your Kali machine at `https://10.RANGENUMBER.10.99:8444` using the creds `kali:password` (sudo password is `kali`).


If you want a challange and want to do the lab with defender enabled, edit the `ad/BARBHACK/data/inventory` file and change the last part to look like this

```
; allow defender
; usage : security.yml
[defender_on]
dc01
srv01
srv02

; disable defender
; usage : security.yml
[defender_off]
```

