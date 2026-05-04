---
sidebar_position: 6
title: "⏱️ Auto Shutdown"
---

# ⏱️ Auto Shutdown

## Overview

Ludus can automatically power off all VMs in a range after a configurable period of inactivity. This is useful for shared environments where users may forget to power down their ranges, saving resources on the host.

Inactivity is determined by two signals:

1. **Guest agent sessions** — the QEMU guest agent on each VM reports whether any user is logged in. If any VM in the range has an active session, the range is considered active.
2. **API activity** — any API call that targets the range (deploys, power operations, config changes, etc.) resets the inactivity timer.

A range is only powered off when **both** signals are idle for longer than the configured timeout.

:::note

VMs without the QEMU guest agent installed (e.g. anti-sandbox VMs, EDR appliances) cannot report sessions. Ranges composed entirely of such VMs fall back to API activity as the sole signal.

:::

## Configuration

Auto shutdown is **opt-in** and disabled by default. It can be configured at two levels:

### Server-wide default

Set a default timeout for all ranges in the Ludus server config file. This value applies to any range that does not have a per-range override.

```yaml title="/opt/ludus/config.yml"
inactivity_shutdown_timeout: 4h
```

The value is a Go duration string (e.g. `4h`, `30m`, `1h30m`). Set to `0` or omit to disable.

Changes to this value are picked up automatically without restarting the Ludus services.

### Per-range override

Individual ranges can override the server default using the CLI or API. A per-range override always takes precedence.

## Commands

### `ludus range auto-shutdown get`

**Description:** Retrieve the current auto-shutdown configuration for a range, showing the server default, per-range override, and effective timeout.

**Usage:**
```bash
ludus range auto-shutdown get
```

**Example output:**
```
+------------------------+----------------+----------------+-----------+
|        SETTING         | SERVER DEFAULT | RANGE OVERRIDE | EFFECTIVE |
+------------------------+----------------+----------------+-----------+
| Auto Shutdown Timeout  |       4h       |       2h       |    2h     |
+------------------------+----------------+----------------+-----------+
```

### `ludus range auto-shutdown set`

**Description:** Set a per-range auto-shutdown timeout.

**Usage:**
```bash
ludus range auto-shutdown set --timeout <duration>
```

**Arguments:**

* `--timeout, -t <duration>` (Required): A Go duration string (e.g. `4h`, `30m`), or `0` to explicitly disable auto shutdown for this range.

**Examples:**

```bash
# Set a 4 hour timeout
ludus range auto-shutdown set --timeout 4h

# Disable auto shutdown for this range even if the server has a default
ludus range auto-shutdown set --timeout 0
```

### `ludus range auto-shutdown reset`

**Description:** Clear the per-range override so the range falls back to the server default.

**Usage:**
```bash
ludus range auto-shutdown reset
```
