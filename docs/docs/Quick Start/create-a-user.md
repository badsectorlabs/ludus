---
sidebar_position: 2
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Create a User

## Setting up the Ludus client

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
Copy the correct Ludus client binary to a location in your PATH and make it executable.

```
local:~$ sudo cp ludus-client_linux-[arch]-[version] /usr/local/bin/ludus
local:~$ sudo chmod +x /usr/local/bin/ludus
```
  </TabItem>
  <TabItem value="macos" label="macOS">
Copy the correct Ludus client binary to a location in your PATH and make it executable.

```
local:~$ sudo cp ludus-client_macOS-[arch]-[version] /usr/local/bin/ludus
local:~$ sudo chmod +x /usr/local/bin/ludus
local:~$ xattr -r -d com.apple.quarantine /usr/local/bin/ludus
```
:::note

macOS users need to remove the "quarantine" attribute as the ludus client binary is not (currently) signed

:::
  </TabItem>
  <TabItem value="windows" label="Windows">
:::note

This documentation assumes the use of the Windows Terminal and Powershell (not cmd.exe and batch).

:::

Copy the correct Ludus client binary to your Windows device.

`cd` to the directory that contains the binary or move the binary to a location in your PATH.

```plain
PS C:\> .\ludus-client_windows_[arch]-[version].exe
Ludus client v1.0.0

Ludus is a CLI application to control a Ludus server
This application can manage users as well as ranges.
...
```
  </TabItem>
</Tabs>



## Using the Ludus client to create a Ludus user

To perform user related actions, which modify the Ludus host as root, we must connect to the 
admin service which only listens on localhost. To do this we will create an SSH tunnel.

```plain title="Terminal 1 (Linux/macOS/Windows)"
ssh -L 8081:127.0.0.1:8081 user@<Ludus IP>
```

From a root shell run `ludus-install-status` which will print the root
API key.

```plain title="Terminal 1"
user@ludus:~$ sudo su -
root@ludus:~# ludus-install-status 
Ludus install completed successfully
Root API key: ROOT.o>T3BMm!^\As_0Fhve8B\VrD&zqc#kCk&B&?e|aF
```

Open a second terminal.

Now create your first ludus user! This user will be an admin as we specify `--admin`.
Initials are commonly used for the userID.

:::warning

If the user name you specify (converted to lowercase and spaces replaced with `-`) exists
on the system already, it's PAM password will be changed by Ludus!

:::

Prepend the LUDUS_API_KEY variable to the command to authenticate properly.

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
:::tip

Adding a space at the beginning of this command will prevent it from being written to the
shell's history file in most common shells.

:::
```plain title="Terminal 2 (Linux/macOS)"
local:~$  LUDUS_API_KEY='ROOT.o>T3BMm!^\As_0Fhve8B\VrD&zqc#kCk&B&?e|aF' \
 ludus user add --name "John Doe" --userid JD --admin --url https://127.0.0.1:8081
+--------+------------------+-------+---------------------------------------------+
| USERID | PROXMOX USERNAME | ADMIN |                   API KEY                   |
+--------+------------------+-------+---------------------------------------------+
| JD     | john-doe         | true  | JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt |
+--------+------------------+-------+---------------------------------------------+
```
  </TabItem>
  <TabItem value="macos" label="macOS">
:::tip

Adding a space at the beginning of this command will prevent it from being written to the
shell's history file in most common shells.

:::
```plain title="Terminal 2 (Linux/macOS)"
local:~$  LUDUS_API_KEY='ROOT.o>T3BMm!^\As_0Fhve8B\VrD&zqc#kCk&B&?e|aF' \
 ludus user add --name "John Doe" --userid JD --admin --url https://127.0.0.1:8081
+--------+------------------+-------+---------------------------------------------+
| USERID | PROXMOX USERNAME | ADMIN |                   API KEY                   |
+--------+------------------+-------+---------------------------------------------+
| JD     | john-doe         | true  | JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt |
+--------+------------------+-------+---------------------------------------------+
```
  </TabItem>
  <TabItem value="windows" label="Windows">
```plain title="Terminal 2 (Windows)"
PS C:\> $env:LUDUS_API_KEY='ROOT.o>T3BMm!^\As_0Fhve8B\VrD&zqc#kCk&B&?e|aF'
PS C:\> .\ludus-client.exe user add --name "John Doe" --userid JD --admin --url https://127.0.0.1:8081
+--------+------------------+-------+---------------------------------------------+
| USERID | PROXMOX USERNAME | ADMIN |                   API KEY                   |
+--------+------------------+-------+---------------------------------------------+
| JD     | john-doe         | true  | JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt |
+--------+------------------+-------+---------------------------------------------+

# Remove the LUDUS_API_KEY environment variable set in the previous command
PS C:\> Remove-Item Env:\LUDUS_API_KEY
```
  </TabItem>
</Tabs>
:::info

This command construct is only required for user add and delete actions. Normal user actions don't require the SSH tunnel or url parameter

:::


## WireGuard

The easiest way to interact with Ludus VMs is directly - via SSH, RDP, or KasmVNC.
This is possible thanks to a WireGuard VPN hosted on the Ludus server.
To get the WireGuard configuration file for your user, run the `user wireguard` command.
Admins can get WireGuard configurations for other users with the `user wireguard --user <UserID>` command.

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```plain title="Terminal 2 (Linux/macOS)"
local:~$  LUDUS_API_KEY='JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt' \
 ludus user wireguard --user JD --url https://127.0.0.1:8081 | tee ludus.conf
[Interface]
PrivateKey = KBxrT+PFLClI+uJo9a6XLm/b23vbqL5KmNQ5Ac6uwGI=
Address = 198.51.100.2/32

[Peer]
PublicKey = 5nlDO6gtqVXI89xQNkd2c2L0US7RnPinbAlfiyWHHBM=
Endpoint = 10.2.99.240:51820
AllowedIPs = 10.2.0.0/16, 198.51.100.1/32
PersistentKeepalive = 25
```
  </TabItem>
  <TabItem value="macos" label="macOS">
```plain title="Terminal 2 (Linux/macOS)"
local:~$  LUDUS_API_KEY='JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt' \
 ludus user wireguard --user JD --url https://127.0.0.1:8081 | tee ludus.conf
[Interface]
PrivateKey = KBxrT+PFLClI+uJo9a6XLm/b23vbqL5KmNQ5Ac6uwGI=
Address = 198.51.100.2/32

[Peer]
PublicKey = 5nlDO6gtqVXI89xQNkd2c2L0US7RnPinbAlfiyWHHBM=
Endpoint = 10.2.99.240:51820
AllowedIPs = 10.2.0.0/16, 198.51.100.1/32
PersistentKeepalive = 25
```
  </TabItem>
  <TabItem value="windows" label="Windows">
```plain title="Terminal 2 (Windows)"
PS C:\> $env:LUDUS_API_KEY='JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt'
PS C:\> .\ludus-client.exe ludus user wireguard --user JD --url https://127.0.0.1:8081 | Tee-Object -Variable luduswg; $luduswg  | Set-Content -Encoding ASCII ludus.conf
[Interface]
PrivateKey = KBxrT+PFLClI+uJo9a6XLm/b23vbqL5KmNQ5Ac6uwGI=
Address = 198.51.100.2/32

[Peer]
PublicKey = 5nlDO6gtqVXI89xQNkd2c2L0US7RnPinbAlfiyWHHBM=
Endpoint = 10.2.99.240:51820
AllowedIPs = 10.2.0.0/16, 198.51.100.1/32
PersistentKeepalive = 25

# Remove the LUDUS_API_KEY environment variable set in the previous command
PS C:\> Remove-Item Env:\LUDUS_API_KEY
```
  </TabItem>
</Tabs>
Import this WireGuard configuration (`ludus.conf`) into the [WireGuard client](https://www.wireguard.com/install/) and connect. Once connected, the ludus client's default url (`https://198.51.100.1:8080`)
will work for all future commands.

## Set the API Key

Using the key from the previous step, run `ludus apikey` and provide the user API key.

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```plain
local:~$ ludus apikey
[INFO]  Enter your Ludus API Key for https://198.51.100.1:8080:
JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt
[INFO]  Ludus API key set successfully
```

:::tip

On headless Linux systems or Linux systems without a keyring, set the LUDUS_API_KEY environment variable instead

`export LUDUS_API_KEY='JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt'`

:::
</TabItem>
  <TabItem value="macos" label="macOS">
```plain
local:~$ ludus apikey
[INFO]  Enter your Ludus API Key for https://198.51.100.1:8080:
JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt
[INFO]  Ludus API key set successfully
```

:::tip

On headless macOS systems or macOS systems without a keyring, set the LUDUS_API_KEY environment variable instead

`export LUDUS_API_KEY='JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt'`

:::
  </TabItem>
  <TabItem value="windows" label="Windows">
```plain
PS C:\> .\ludus-client.exe apikey
[INFO]  Enter your Ludus API Key for https://198.51.100.1:8080:
JD._7Gx2T5kTUSD%uTWZ*lFi=Os6MpFR^OrG+yT94Xt
[INFO]  Ludus API key set successfully
```
  </TabItem>
</Tabs>

With the API key set, all user commands are available!

## Get Proxmox Credentials

Ludus is built on top of the [Proxmox](https://www.proxmox.com/en/) hypervisor which has a web interface.
It's available at `https://<ludus IP>:8006` and the credentials for the web GUI can be retrieved with `ludus user creds get`.

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
```plain
local:~$ ludus user creds get
+------------------+----------------------+
| PROXMOX USERNAME |   PROXMOX PASSWORD   |
+------------------+----------------------+
| john-doe         | oQjQC76Ny0HQfpNV31zK |
+------------------+----------------------+
```
</TabItem>
  <TabItem value="macos" label="macOS">
```plain
local:~$ ludus user creds get
+------------------+----------------------+
| PROXMOX USERNAME |   PROXMOX PASSWORD   |
+------------------+----------------------+
| john-doe         | oQjQC76Ny0HQfpNV31zK |
+------------------+----------------------+
```
  </TabItem>
  <TabItem value="windows" label="Windows">
```plain
PS C:\> .\ludus-client.exe user creds get
+------------------+----------------------+
| PROXMOX USERNAME |   PROXMOX PASSWORD   |
+------------------+----------------------+
| john-doe         | oQjQC76Ny0HQfpNV31zK |
+------------------+----------------------+
```
  </TabItem>
</Tabs>

Now that you've created the user, grabbed your WireGuard config, and obtained your user creds for proxmox, you can build templates!