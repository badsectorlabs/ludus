---
title: "SANS Workshop: Active Directory Privilege Escalation with Empire"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# SANS Workshop: Active Directory Privilege Escalation with Empire!

:::success Props!

Huge shout out to [@ladhaAleem](https://twitter.com/LadhaAleem) converting the "SANS Workshop: Active Directory Privilege Escalation with Empire" workshop created by [Jean-François Maes](https://www.sans.org/profiles/jeanfrancois-maes/) to an ansible playbook and making it work with Ludus as well!
:::

## Description from SANS Workshop: Active Directory Privilege Escalation with Empire

Welcome to this workshop where we are going to dive into a core active directory component - Kerberos!

This lab is a self-guided Active Directory security exercise designed to help participants understand Kerberos-based privilege escalation attacks. Originally part of a SANS workshop, this lab is now freely available for local deployment on VMware, VirtualBox, and Ludus.

Participants will build their own AD lab, configure attack tools, and execute real-world attack techniques to escalate privileges in an Active Directory environment.

This workshop is ideally suited for blue teamers that want to peek behind the curtain and understand how adversaries attack AD and pentesters that may not be as familiar with AD environments yet.

Attacks Covered:

- Kerberoasting – Extracting service tickets to crack passwords
- DCSyncing – Extracting credentials by simulating a domain controller
- SID History Abuse – Hopping across parent/child domain trusts
- Unconstrained Delegation Abuse – Capturing privileged credentials

:::note

The following lab only uses of Empire & Starkiller and no other tools

:::

Have fun !

### Access the workbook here:

- https://logout.gitbook.io/ad-privesc-with-empire 


## Deployment

### 1. Deploy the VMs

Set and deploy the configuration for the lab.

```bash
#terminal-command-local
git clone https://github.com/aleemladha/SANS-Workshop-Lab
#terminal-command-local
ludus range config set -f SANS-Workshop-Lab/ad/SANS/providers/ludus/config.yml
#terminal-command-local
ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```

### 2. Install requirements

Install ansible and its requirements for the lab on your local machine.

```shell-session
# You can use a virtualenv here if you would like
#terminal-command-local
python3 -m venv sans-ludus
#terminal-command-local
source sans-ludus/bin/activate
#terminal-command-local
python3 -m pip install ansible-core
#terminal-command-local
python3 -m pip install pywinrm
#terminal-command-local
ansible-galaxy install -r SANS-Workshop-Lab/ansible/requirements.yml
```

### 3. Setup  the inventory files

The inventory file is already present in the providers folder and replace RANGENUMBER with your range number with sed (commands provided below)


<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-local
cd SANS-Workshop-Lab/ansible
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `sudo apt install jq` if you don't have jq
#terminal-command-local
sed -i "s/RANGENUMBER/$RANGENUMBER/g" ../ad/SANS/providers/ludus/inventory.yml
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
#terminal-command-local
cd SANS-Workshop-Lab/ansible
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
#terminal-command-local
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/SANS/providers/ludus/inventory.yml
```
  </TabItem>
</Tabs>


### 4. Deploy the SANS Workshop

:::note

If not running on the Ludus host, you must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
# in the SANS-Workshop-Lab/ansible folder perform the following
#terminal-command-local
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/SANS/data/inventory -i ../ad/SANS/providers/ludus/inventory.yml"
#terminal-command-local
export LAB="SANS"
#terminal-command-local
chmod +x ../scripts/provisionning.sh
#terminal-command-local
../scripts/provisionning.sh
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```bash
# In the SANS-Workshop-Lab/ansible folder perform the following
#terminal-command-local
export ANSIBLE_COMMAND="ansible-playbook -i ../ad/SANS/data/inventory -i ../ad/SANS/providers/ludus/inventory.yml"
#terminal-command-local
export LAB="SANS"
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
The Empire's dominion is complete! But Rebel operatives remain hidden. Your mission: eliminate them.
```
:::note

You must be connected to your Ludus wireguard VPN for these commands to work

:::

### 5. Snapshot VMs

Take snapshots via the proxmox web UI or SSH into ludus and as root run the following

```bash
ludus snapshot create clean-setup -d "Clean SANS Lab setup after ansible run"
```

### 6. Hack!

Access your Kali machine at `https://10.RANGENUMBER.50.99:8444` using the creds `kali:password`.

Then [Setup Empire & Starkiller](https://logout.gitbook.io/ad-privesc-with-empire/installing-the-environment/empire).

Once done, follow lab 2 in the workbook above, without the need to use any OpenVPN configuration.

:::note

Replace this part with your RANGENUMBER `xfreerdp /v:10.RANGENUMBER.20.10 /u:jross /p:'0nz2xQ44GumoWpl' +clipboard`

You can also use a standard RDP client on your local machine if your WireGuard is connected.

:::

If you want a challange and want to do the lab with defender enabled, edit the `ad/SANS/data/inventory` file and change the last part to look like this

```
; allow defender
; usage : security.yml
[defender_on]
dc01
dc02
dc03
srv02

; disable defender
; usage : security.yml
[defender_off]
```

