---
sidebar_position: 7
title: "🌐 WebUI"
description: "Visual front-end for managing Ludus ranges, blueprints, templates, roles, and users"
keywords: [webui, web ui, gui, frontend, telemetry, sso]
---

# 🌐 WebUI

:::note[🏛️ `Available in Ludus Pro and Ludus Enterprise`]
:::

The WebUI is a visual front-end for Ludus. It covers the same range, blueprint, template, role, and user/group operations as the CLI without requiring a local install.

## How it works

The WebUI is a static web application embedded into the Ludus server binary. It is served by the same process that serves the API, on the same port (default `8080`), under the path `/ui`.

| | |
|---|---|
| URL | `https://<ludus-host>:8080/ui` |
| Port | Same as the API. Set with the `port` key in `/opt/ludus/config.yml` — see [External Access](../administration/security#external-access) |
| Auth | Email and password ([SSO](../administration/sso) is also supported) |
| TLS | Self-signed certificate by default — accept the browser warning on first visit |

## Logging in

Log in with the email and password set when the account was created — at `ludus install` for the initial admin, or at `ludus user add` for everyone else. The same password is used for the WebUI and Proxmox. Users migrated from Ludus v1 log in as `<proxmox-username>@ludus.internal` with their existing Proxmox password; see [Upgrading from v1](../upgrading-from-v1).

![A screenshot showing the login page](/img/enterprise/webui/WebUI-Login.jpg)

## Pages

### Ranges

![A screenshot showing the default homepage](/img/enterprise/webui/WebUI-Homepage.jpg)

Lists every range owned by the user. Selecting a range opens the **Editor**.

More information: [Range Configuration](../configuration), [Environment Guides](../category/environment-guides).

### Editor

![A screenshot showing the Range Page](/img/enterprise/webui/WebUI-Range.jpg)

Visual builder for a single range. From the Editor you can:

- Add VMs from available templates and edit per-VM settings.
- Save the range config and deploy.
- Power VMs on and off.
- Create, list, and roll back snapshots — see [Snapshots](../using-ludus/snapshots).
- Start and stop testing mode and manage allow/deny lists during a test — see [Testing mode](../quick-start/testing-mode).
- Configure router and per-VLAN networking, including [Outbound WireGuard](outbound-wireguard) routes.
- Apply [KMS licensing](kms) and [Anti-Sandbox](anti-sandbox) to specific VMs.

The top-left arrow returns to the Ranges page.

### VM Console

A browser-based VNC console for a single VM. Useful for VMs without network access or for inspecting the desktop directly.

Open the console from the per-VM card in the Editor:

![A screenshot showing the Open VM console button on a VM card in the Editor](/img/enterprise/webui/console-prompt.png)

The console opens as another node on the canvas:

![A screenshot showing the VNC console for a Kali VM](/img/enterprise/webui/kali-console.png)

### Blueprints

![A screenshot showing the Blueprints Page](/img/enterprise/webui/WebUI-Blueprints.jpg)

Lists available blueprints. Use **+ Blueprint** to create a new one from a deployed range.

![A screenshot showing the Blueprints Create Box](/img/enterprise/webui/WebUI-Blueprints2.jpg)

More information: [Blueprints](../using-ludus/blueprints).

### Ansible

![A screenshot showing the Ansible (Roles) Overview](/img/enterprise/webui/WebUI-Ansible.jpg)

Lists Ansible roles available for use in ranges. **+ Roles** lets users add roles from a local folder, Ansible Galaxy, or the private roles published with their license.

![A screenshot showing the Ansible (Roles) add Box](/img/enterprise/webui/WebUI-Ansible2.jpg)

If `prevent_user_ansible_add: true` is set in `/opt/ludus/config.yml`, only admins can add roles.

More information: [Roles](../using-ludus/roles).

### Templates

![A screenshot showing the Templates Overview](/img/enterprise/webui/WebUI-Templates.jpg)

Lists templates currently built and available for use. **+ Templates** uploads a new template; **>_ Logs** streams the build log.

![A screenshot showing the Templates Add Box](/img/enterprise/webui/WebUI-Templates2.jpg)

More information: [Templates](../using-ludus/templates).

### Admin → Users and Groups

Admin-only. Create, list, edit, and delete users and groups. The user detail page also surfaces:

- The user's WireGuard config — copy or download as `<userID>-wireguard.conf`.
- Per-user quota limits (Enterprise) — see [Quotas](quotas).

The group detail page surfaces per-group default quotas (Enterprise).

### Settings

User preferences (theme, telemetry) and admin-only Enterprise toggles.

| Section | Visibility | Documentation |
|---|---|---|
| KMS server | Admin | [Windows Licensing (KMS)](kms) |
| Anti-Sandbox | Admin (Enterprise add-on) | [Anti-Sandbox](anti-sandbox) |
| License | Admin | [Pro/Enterprise Overview](enterprise) |
| Quota defaults | Admin | [Quotas](quotas) |
| Telemetry | Everyone | [Telemetry](#telemetry) |
| Theme | Everyone | — |

Settings holds the server-wide install and default state for these features. Per-VM application (KMS, Anti-Sandbox, Outbound WireGuard routing) happens in the **Editor**; per-user and per-group quota assignments happen in **Admin → Users** and **Admin → Groups**.

## Telemetry

The WebUI sends anonymous usage telemetry to a self-hosted PostHog instance at `ph.ludus.cloud`. Sensitive identifiers (range IDs, blueprint IDs) are hashed before being sent, and the user identity is a random anonymous ID — not the account email or user ID.

Telemetry is enabled by default for Ludus Pro NFR (Not-For-Resale) licenses and disabled by default for all other licenses. The setting is per-browser (stored in `localStorage`).

To toggle telemetry, open the **Settings** page and use the **Privacy** toggle.
