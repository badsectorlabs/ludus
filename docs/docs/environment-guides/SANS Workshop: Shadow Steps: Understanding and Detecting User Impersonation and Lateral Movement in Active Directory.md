---
title: "SANS Workshop: Shadow Steps: Understanding and Detecting User Impersonation and Lateral Movement in Active Directory"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# SANS Workshop: Shadow Steps: Understanding and Detecting User Impersonation and Lateral Movement in Active Directory

:::success Props!

Huge shout out to [@ladhaAleem](https://twitter.com/LadhaAleem) converting the "SANS Workshop: Shadow Steps: Understanding and Detecting User Impersonation and Lateral Movement in Active Directory" workshop created by [Jean-François Maes](https://www.sans.org/profiles/jeanfrancois-maes/) to an ansible playbook and making it work with Ludus as well!
:::

## Description from SANS Workshop: Shadow Steps: Understanding and Detecting User Impersonation and Lateral Movement in Active Directory

This hands-on, scenario-driven workshop delves into how attackers move stealthily through Active Directory environments using user impersonation and lateral movement techniques. Participants will explore how attackers exploit credentials and trust relationships to expand their access, and how defenders can detect, prevent, and respond to such threats.

Through simulated exercises and guided labs, participants will walk through real-world attack paths such as (over)Pass-the-Hash, Kerberoasting, and token impersonation.

Learning Objectives:

- Understand the key mechanisms behind user impersonation in Active Directory.
- Demonstrate how attackers perform lateral movement via tools and techniques such as:
- Pass-the-Hash
- Pass-the-Ticket/Overpass-the-Hash
- Remote Services Abuse (SMB, WMI, RDP, WinRM)\
- SOCKS PTH
- Kerberoasting
- Token Impersonation
- Token Creation
- This hands-on workshop is ideal for Penetration Testers with limited knowledge about AD internals.


Have fun !

### Access the workbook here:

- https://logout.gitbook.io/lateral-movement-in-ad-with-empire 


## Deployment

### 1. Add the `badsectorlabs.ludus_elastic_container` and `badsectorlabs.ludus_elastic_agent` roles to your Ludus server

```shell-session
#terminal-command-local
ludus ansible roles add badsectorlabs.ludus_elastic_container
#terminal-command-local
ludus ansible roles add badsectorlabs.ludus_elastic_agent
```



### 2. Deploy the VMs

Set and deploy the configuration for the lab.

```bash
#terminal-command-local
git clone https://github.com/aleemladha/SANS-Workshop-LateralMovement
#terminal-command-local
ludus range config set -f SANS-Workshop-LateralMovement/ad/SANS/providers/ludus/config.yml
#terminal-command-local
ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```

### 3. Install requirements

Install ansible and its requirements for the lab on your local machine.

```shell-session
# You can use a virtualenv here if you would like
#terminal-command-local
python3 -m venv sans-lat-ludus
#terminal-command-local
source sans-lat-ludus/bin/activate
#terminal-command-local
python3 -m pip install ansible-core
#terminal-command-local
python3 -m pip install pywinrm
#terminal-command-local
ansible-galaxy install -r SANS-Workshop-LateralMovement/ansible/requirements.yml
```

### 4. Setup  the inventory files

The inventory file is already present in the providers folder and replace RANGENUMBER with your range number with sed (commands provided below)


<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
#terminal-command-local
cd SANS-Workshop-LateralMovement/ansible
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
cd SANS-Workshop-LateralMovement/ansible
#terminal-command-local
export RANGENUMBER=$(ludus range list --json | jq '.rangeNumber')
# `brew install jq` if you don't have jq
#terminal-command-local
sed -i '' "s/RANGENUMBER/$RANGENUMBER/g" ../ad/SANS/providers/ludus/inventory.yml
```
  </TabItem>
</Tabs>


### 5. Deploy the SANS Workshop

:::note

If not running on the Ludus host, you must be connected to your Ludus wireguard VPN for these commands to work

:::

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```bash
# in the SANS-Workshop-LateralMovement/ansible folder perform the following
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
# In the SANS-Workshop-LateralMovement/ansible folder perform the following
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

### 6. Snapshot VMs

Take snapshots via the proxmox web UI or SSH run the following ludus command:

```bash
ludus snapshot create clean-setup -d "Clean SANS Lab setup after ansible run"
```

### 7. Hack!

Access your Kali machine at `https://10.RANGENUMBER.50.99:8444` using the creds `kali:password`.

Access your Elastic SIEM at `https://10.RANGENUMBER.20.1:5601` using the creds `elastic:elasticpassword`

Then [Setup Empire & Starkiller](https://logout.gitbook.io/lateral-movement-in-ad-with-empire/installing-the-environment/empire).

Once done, follow lab 2 in the workbook above, without the need to use any OpenVPN configuration.

:::note

Replace this part with your RANGENUMBER `xfreerdp /v:10.RANGENUMBER.20.11 /u:Administrator /p:'AnsibleAutomation123!' +clipboard /dynamic-resolution`

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

