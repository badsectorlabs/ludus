---
sidebar_position: 12
title: "🆙 Updating"
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# 🆙 Updating Ludus

:::tip[Don't trust the binaries?]

    Ludus binaries are built in CI, but you can always [build them from source](./Developers/building-from-source) yourself.

:::

## Updating a Ludus server

Updating a Ludus server is easy:

1. Download the server [release binary](https://gitlab.com/badsectorlabs/ludus/-/releases) (or build from source) of the version you wish to update to.
1. Copy the Ludus server binary to the Ludus server host
1. Run the Ludus server binary as root with the `--update` flag.

```
local:~$ scp ludus-server user@ludus:
local:~$ ssh user@ludus
user@ludus:~$ chmod +x ludus-server
user@ludus:~$ sudo ./ludus-server --update
Backed up /opt/ludus/ansible to /opt/ludus/previous-versions/1707349133263620491/ansible
Backed up /opt/ludus/packer to /opt/ludus/previous-versions/1707349133263620491/packer
Backed up /opt/ludus/ci to /opt/ludus/previous-versions/1707349133263620491/ci
Extracting ludus to /opt/ludus...
Ludus files extracted successfully
Ludus updated to v1.0.2+6a96b3ef
```

:::note

    If you are updating from < 1.3.0 see [UPDATING.md](https://gitlab.com/badsectorlabs/ludus/-/blob/main/UPDATING.md?ref_type=heads)

:::

:::warning

    The Ludus server binary will refuse to update if any `ansible` or `packer` processes are active on the machine to prevent possible interruption to active range or template activity.

:::

## Updating the Ludus client

Download the client [release binary](https://gitlab.com/badsectorlabs/ludus/-/releases) (or build from source) of the version you wish to update to.

<Tabs groupId="operating-systems">
  <TabItem value="linux" label="Linux">
Copy the correct Ludus client binary to a location in your PATH and make it executable.

```
local:~$ sudo cp ludus-client_linux-[arch]-[version] /usr/local/bin/ludus
local:~$ chmod +x /usr/local/bin/ludus
```
  </TabItem>
  <TabItem value="macos" label="macOS">
Copy the correct Ludus client binary to a location in your PATH and make it executable.

```
local:~$ sudo cp ludus-client_macOS-[arch]-[version] /usr/local/bin/ludus
local:~$ chmod +x /usr/local/bin/ludus
local:~$ xattr -r -d com.apple.quarantine /usr/local/bin/ludus
```
:::note

macOS users need to remove the "quarantine" attribute as the ludus client binary is not (currently) signed

:::
  </TabItem>
  <TabItem value="windows" label="Windows">
Copy the correct Ludus client binary to your Windows device.

`cd` to the directory that contains the binary or move the binary to a location in your PATH.

```
PS C:\> .\ludus-client_windows_[arch]-[version].exe
Ludus client v1.0.0

Ludus is a CLI application to control a Ludus server
This application can manage users as well as ranges.
...
```
  </TabItem>
</Tabs>
