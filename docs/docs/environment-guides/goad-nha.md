---
title: "GOAD - NHA"
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Game of Active Directory (GOAD) - NHA - Ninja Hacker Academy

:::success Props!

Huge shout out to [@M4yFly](https://twitter.com/M4yFly) for all the hard work to create GOAD NHA!

:::

## Description from GOAD

- NINJA HACKER ACADEMY (NHA) is written as a training challenge where GOAD was written as a lab with a maximum of vulns.

- You should find your way in to get domain admin on the 2 domains (academy.ninja.lan and ninja.hack)

- Starting point is on srv01 : "WEB"

- Flags are disposed on each machine, try to grab all. Be careful all the machines are up to date with defender enabled.

- Some exploits needs to modify path so this lab is not very multi-players compliant (unless you do it as a team ;))

- Obviously do not cheat by looking at the passwords and flags in the recipe files, the lab must start without user to full compromise.

- Hint: No bruteforce, if not in rockyou do not waste your time and your cpu/gpu cycle.

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

### 2. On the Ludus host, clone and setup the GOAD project

```bash
git clone https://github.com/Orange-Cyberdefense/GOAD.git
cd GOAD
sudo apt install python3.11-venv
export LUDUS_API_KEY='myapikey'  # put your Ludus admin api key here
./goad.sh -p ludus
GOAD/ludus/local > check
GOAD/ludus/local > set_lab NHA # GOAD/GOAD-Light/NHA/SCCM
GOAD/ludus/local > install
```

Now you wait. `[WARNING]` lines are ok, and some steps may take a long time, don't panic!

This will take a few hours. You'll know it is done when you see:

```
[*] Lab successfully provisioned in XX:YY:ZZ
```

### Optional: Add a Kali VM

```
ludus --user GOADd126ca range config get > config.yml # Replace GOADd126ca with your GOAD UserID
vim config.yml # Edit the file to add a Kali VM (see below)
ludus --user GOADd126ca range config set -f config.yml
ludus --user GOADd126ca range deploy -t vm-deploy
# Wait for the deployment to finish
ludus --user GOADd126ca range logs -f
# Deploy the Kali VM
ludus --user GOADd126ca range deploy --limit localhost,GOADd126ca-kali
```

The added Kali VM should look like this at the end of the `ludus:` block:

```yaml
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

### 3. Snapshot VMs

Take snapshots via the proxmox web UI or SSH into ludus and as root run the following

```bash
export RANGEID=GOADd126ca # <= change to your user GOAD ID
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

### 4. Hack!

With your WireGuard connected on a client machine (your laptop, etc.), access your Kali machine (if you deployed one) at `https://10.RANGENUMBER.10.99:8444` using the creds `kali:password`. Or you can access the lab directly from your client machine with WireGuard connected and attack the 10.RANGENUMBER.10.X subnet.