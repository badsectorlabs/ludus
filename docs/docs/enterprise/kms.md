---
sidebar_position: 5
title: "🪪 Windows Licensing (KMS)"
---

# 🪪 Windows Licensing - Key Management Service (KMS)

:::note

Ludus users are responsible for ensuring they have valid licenses for all Windows machines licensed via the KMS.

:::

## Overview

Ludus provides commands to manage a Key Management Service (KMS) server for activating Windows virtual machines within your ranges. This functionality is available in Ludus enterprise.

The KMS server runs on the Ludus host at a static IP address (`192.0.2.1`) and is used to activate volume-licensed Windows VMs.



## Commands

This section details the available KMS commands.

### `ludus kms install`

**Description:** Installs a Key Management Service (KMS) server on the Ludus host.
The server will be configured to listen on `192.0.2.1`.

**Usage:**
```bash
ludus kms install
```

**Arguments:** None

### `ludus kms license`

**Description:** Licenses one or more Windows VMs using the installed KMS server.

**Usage:**
```bash
ludus kms license --vmids <vmids> [--product-key <key>]
```

**Arguments:**

*   `--vmids, -n <vmids>` (Required): A comma-separated list of VM IDs to license (e.g., `104`, `104,105`).
*   `--product-key, -p <key>` (Optional): The specific volume license product key to use for activation. If not provided, Ludus attempts to determine the appropriate key based on the Windows version of the VM.
*   `--user <userID>` (Optional, Admin only): Impersonate a specific user to license VMs in their range.

## Usage Examples

**1. Install the KMS server:**

```bash
ludus kms install
# Wait for the installation to complete.
```

**2. License a single Windows VM:**

:::note

Windows servers that have been promoted to Domain Controllers cannot be licensed. License the VM before configuring Active Directory.

:::

```bash
# Assuming VM ID 110 is a Windows VM
ludus kms license --vmids 110
```

**3. License multiple Windows VMs:**

```bash
# Assuming VM IDs 110 and 112 are Windows VMs
ludus kms license --vmids 110,112
```

**4. License VMs with a specific product key:**

Keys can be found at [learn.microsoft.com](https://learn.microsoft.com/en-us/windows-server/get-started/kms-client-activation-keys)

```bash
ludus kms license --vmids 110,112 --product-key TVRH6-WHNXV-R9WG3-9XRFY-MY832
```

**5. License VMs for another user (as admin):**

```bash
ludus kms license --vmids 205 --user JD
```
