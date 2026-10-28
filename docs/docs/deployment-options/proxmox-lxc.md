---
title: Proxmox LXC installer
---

# Install Ludus in a Proxmox LXC

The LXC installer runs the Ludus server in an unprivileged container on an
existing Proxmox VE host. Proxmox continues to run the range VMs. The container
runs the Ludus API, Ansible, Packer, WireGuard, and the other Ludus services.

This installation mode does not install Ludus packages directly on every
Proxmox node. The installer creates the container, Proxmox SDN objects, and an
API token that the container uses to manage the cluster.

## Requirements

Run the installer as `root` on an amd64 Proxmox VE 8 or 9 host. The host needs:

- `bash`, `curl`, and a SHA-256 checksum utility for the online bootstrap
- `openssl` for signed offline-media verification
- a healthy Proxmox cluster with quorum
- a storage pool that supports LXC root filesystems
- VM and ISO storage available to every node that will build or run VMs
- an unused static IP address and gateway on a Proxmox bridge available on every
  potential LXC node (see [Hosts with a single public address](#hosts-with-a-single-public-address)
  if the host has no spare address)
- working routing between the container and every Proxmox API endpoint

The installer creates an unprivileged LXC with 4 vCPUs, 4 GiB of memory, and
512 MiB of swap. The root filesystem defaults to 250 GiB. Use `--rootfs-size`
to change it; the minimum is 20 GiB. The compressed appliance size changes
between releases.

In a multi-node cluster, allow Proxmox SDN traffic, including VXLAN UDP 4789,
between the nodes. The installer creates a VXLAN zone named `ludus`, a VNet
named `ludusnat`, and the `192.0.2.0/24` subnet. Do not reuse those names or that
subnet for unrelated infrastructure.

## Install on a connected cluster

Download the installer to one Proxmox node so you can inspect it before running
it:

```shell
curl -fL https://ludus.cloud/install -o install.sh
chmod 0755 install.sh
less install.sh
./install.sh --server-only
```

The online script installs the client unless `--server-only` is supplied, then
hands server operations to the verified `ludus-installer` executable on Proxmox.
The bootstrap consumes `--server-only`; it is not a native engine flag.
The root `ludus-installer/` Go module owns the installation engine.

The native Bubble Tea/Lip Gloss/Huh UI shows the license first, followed by a
grouped settings form with a live sidebar. Scroll to the end of the license
before accepting it, or decline to exit. Each form option has an inline hint.

The form uses the 2.3.x installer's adaptive gold/green accents and cyan focused
field titles. It detects the controlling terminal's color profile and background
before reading input, with indexed-color fallbacks for fixed-palette terminals.

- **Tab / Enter** confirms and advances; the confirmed Live Settings row flashes
  Ludus gold for 700 ms. **Shift+Tab** goes back without confirming.
- **Arrow keys** select storage pools, disk formats, and management bridges.
  Storage choices are filtered by supported content and availability; block
  storage uses `raw`. Pool labels include the backend and available space.
- Text fields support cursor movement, Home/End, Backspace/Delete, and
  **Ctrl+U** to clear. Supplied flags and environment values prefill the form.
- Invalid values remain editable; both slash borders turn red on an error.
- Management addressing requires a static IP with CIDR prefix and a gateway.
  DHCP is not supported.
- The final review shows every setting. It defaults to **Back**, not
  **Install**. Select **Install** explicitly to proceed; **Esc / Ctrl+C**
  cancels without creating an API token or LXC.

The sidebar has 44–50 columns of content and appears at terminal widths of 108
columns or more, leaving at least 55 columns for the form. Narrower terminals
hide the sidebar; the minimum size is 54 columns by 20 rows. Review and live
settings use bold labels with grey values. Fields and hints scroll together
to keep the focused option visible
on short terminals. The form reads `/dev/tty`, so piped installer commands still work.
Without a controlling terminal, use `--no-prompt` and supply the required flags.
`--no-prompt` bypasses both the license dialog and the settings form.
No Python/curses UI or separate UI package download is required.

For host migrations, the native installer prepares a read-only migration plan
before the dialogs. Its management network and preserved WireGuard endpoint/port
are shown read-only; supply management-network flags before starting migration
if needed. Declining the license or cancelling the form exits before provisioning.
Fresh installs also show the dialogs before downloading the appliance.

The form collects:

| Setting | Use |
| --- | --- |
| Proxmox API endpoints | Space-separated `https://node:8006` URLs. Include every node that Ludus may manage. |
| LXC VMID | VMID for the Ludus container. The default is the next free cluster VMID. |
| LXC rootfs storage and size | Storage used for the container root filesystem. The minimum supported size is 20 GiB. |
| LXC bridge and VLAN | Management bridge available on every potential LXC node, plus an optional VLAN tag. |
| LXC IP, gateway, and DNS | Required static address with CIDR prefix and gateway. An explicit internal resolver is recommended offline. |
| WireGuard endpoint and port | Address and UDP port that Ludus clients use. The endpoint defaults to the local Proxmox node's IP; the port defaults to 51820. The installer creates the host-to-container forward unless disabled. |
| WireGuard forwarding | Enabled by default. Disable to manage forwarding yourself or use a directly reachable container endpoint. |
| VM storage and format | Proxmox storage for range VMs and templates, plus its supported `qcow2` or `raw` disk format. |
| ISO storage | Shared Proxmox storage used while building VM templates. |
| License | `community` or a Ludus license key. |
| CA certificate | Optional site-supplied PEM root CA copied into the LXC and templates built by Ludus. |
| Initial admin user | Display name, email, user ID, and password (entered twice) for the first Ludus administrator. Fresh installs only; migrations and `--import-db` keep their existing users. |

The form does not ask for the container hostname; it defaults to `ludus`. Pass
`--hostname NAME` (or set `LXC_HOSTNAME`) to choose another name. The name is
cosmetic and can be changed later with `pct set <vmid> --hostname NAME`.

If you have not supplied or edited the management IP, confirming the bridge
starts an IPv4 address suggestion for its untagged network in the background;
confirming a different VLAN restarts it for that VLAN. The installer reads
the Proxmox interface configuration and checks it against the active host
interface. On a `/24`, it starts at `.2`; other supported prefixes use the second
address after the network address.

The search skips host and cluster-node addresses, gateways, and resolved
neighbors. It sends a ping through the selected interface and checks ARP
resolution, so a host that blocks ICMP but answers ARP is also skipped. The
search examines at most 32 candidate addresses within a 15-second deadline.
A gateway is prefilled only when one is configured for that interface.

**A suggestion is not a reservation or proof that an address is unused.**
Verify it against your address inventory and DHCP allocation before accepting
it. Offline hosts may not respond to either check.

Changing the bridge or VLAN clears unedited automatic suggestions; supplied and
manually edited values are preserved. A tagged VLAN needs a matching active host
interface; the installer never borrows the untagged bridge's subnet. Missing or
ambiguous subnet information, probe errors, `/31` or `/32` prefixes, and IPv6
networks require manual entry. Migration keeps its prepared static addressing.

The installer persists a UDP forward from the Proxmox host's selected WireGuard
port to the container's static management address. Fresh installations apply
the rules live and merge them into `/etc/iptables/rules.v4` (`rules.v6` for an
IPv6 address), which `iptables-persistent` restores at boot. Other rules in those
files are preserved; rerunning the installer replaces only rules commented
`ludus-wireguard`. The forwarding sysctl is persisted in
`/etc/sysctl.d/99-ludus-wireguard-forward.conf`. The online installer installs
`iptables-persistent` when missing, without saving the current live ruleset.

Forwarding is not needed when the computers that will use WireGuard can already
reach the container's management address directly. In the interactive form,
confirming **Enabled** opens a short notice describing these host changes, with
a choice to keep forwarding or disable it. Select **Disabled**, pass
`--no-wg-forward`, or set `NO_WG_FORWARD=1` to opt out. Use `--wg-endpoint` to
select a directly reachable container address or an endpoint whose forwarding
you manage separately. Upstream routers still need their own UDP forward to the
Proxmox host. Host migrations use their existing forwarding service; opting out
skips WireGuard forwarding but preserves API forwarding.

When run as `root`, the installer creates `root@pam!ludus-lxc-<VMID>` if no token
was provided. Creation happens only after confirmation and validation, using the
selected VMID. Existing tokens are never rotated; supply an existing token and
secret together or choose another VMID.

The installer then:

1. Downloads and verifies the release LXC appliance, unless it is already in
   `/var/lib/vz/template/cache`.
2. Creates or validates the token against the local Proxmox API.
3. Creates and applies the `ludus` SDN zone and `ludusnat` VNet.
4. Creates and starts the container.
5. Writes `/opt/ludus/config.yml` inside the container, plus the initial admin
   details from the form.
6. Waits up to five minutes for the first-boot bootstrap, which creates the
   initial admin user, its Proxmox account, and its default range.
7. Creates the persistent WireGuard UDP forward unless disabled.

Interactive installs show the current step beside a spinner, with finished
steps listed above it, and end with a summary framed by rules above and below. Command output is shown only
when a step fails. `--no-prompt` installs print plain `[+]` step lines and a
text summary instead, which suits automation logs.

The final output contains the Ludus API and web UI addresses and three
commands that configure `ludus-client` with the initial admin's API key and
list all ranges. The summary has no side borders, so the commands can be
selected and pasted as-is:

```shell
export LUDUS_URL=https://<container-ip>:8080
export LUDUS_API_KEY=<initial-admin-api-key>
ludus range list all
```

Store the key securely; it is not shown again, but you can create a new one in
the web UI (`https://<container-ip>:8080/ui`), where you sign in with the admin
email and password. `--no-prompt` installs do
not create an admin user; use the root API key inside the container
(`/opt/ludus/install/root-api-key`) with `ludus user add`.
Continue with [Create additional users](../quick-start/create-a-user.md) and
[Build templates](../quick-start/build-templates.md).

## Hosts with a single public address

Cloud VMs and many rented servers have only one public address, so the network
has no spare address for the Ludus container. Put the container on a private
NAT bridge instead:

1. On the Proxmox host, create a private bridge with masquerading as described
   in the Proxmox [Network Configuration](https://pve.proxmox.com/wiki/Network_Configuration)
   guide ("Masquerading (NAT) with iptables"). Choose a subnet outside
   `10.0.0.0/8` (range networks), `192.0.2.0/24` (`ludusnat`), and
   `198.51.100.0/24` (WireGuard clients). For example, in
   `/etc/network/interfaces`:

   ```
   auto vmbr1
   iface vmbr1 inet static
       address 172.16.100.1/24
       bridge-ports none
       bridge-stp off
       bridge-fd 0
       post-up   echo 1 > /proc/sys/net/ipv4/ip_forward
       post-up   iptables -t nat -A POSTROUTING -s '172.16.100.0/24' -o vmbr0 -j MASQUERADE
       post-down iptables -t nat -D POSTROUTING -s '172.16.100.0/24' -o vmbr0 -j MASQUERADE
   ```

   Replace `vmbr0` with the interface that carries the host's address. On a
   cloud VM, leave the provider's network interface unbridged (for example
   `ens4` or `eth0`) and use it here. Apply the change with `ifreload -a`. If
   the Proxmox firewall is enabled, also add the conntrack zone rules from that
   guide.
2. In the installer, select `vmbr1` as the management bridge, an address such as
   `172.16.100.2/24`, and gateway `172.16.100.1`.
3. Set the WireGuard endpoint to the host's public address or DNS name, and keep
   WireGuard forwarding enabled. The default endpoint is the node's own address,
   which in a cloud VPC is usually private. Allow the WireGuard UDP port (51820 by
   default) in the provider's firewall.
4. The API and web UI listen on the container's private address, which only the
   Proxmox host can reach. Run the three commands from the installer summary on
   the Proxmox host (the install script installs the client there), then create
   users and download WireGuard configurations with `ludus user wireguard`.
   Connected WireGuard clients reach the API and web UI at
   `https://198.51.100.1:8080` (set `LUDUS_URL` to that address).

## Non-interactive install

Use `--no-prompt` for automation and supply `--ip STATIC_CIDR` and `--gw IP`.
Unattended installs do not probe for or choose a management address. Host
migrations can instead use the static address allocated by their private
management-network plan.
The WireGuard endpoint defaults to the local Proxmox node's IP; override it with
`--wg-endpoint`. Host UDP forwarding is enabled unless `--no-wg-forward` is set.

```shell
VERSION=2.3.0

VM_STORAGE=nfs ISO_STORAGE=local ./install.sh \
  --server-only \
  --version "$VERSION" \
  --no-prompt \
  --vmid 900 \
  --storage local-lvm \
  --ip 10.20.30.50/24 \
  --gw 10.20.30.1 \
  --nameserver 10.20.30.53 \
  --endpoints "https://10.20.30.11:8006 https://10.20.30.12:8006 https://10.20.30.13:8006" \
  --wg-endpoint ludus.example.com \
  --license community
```

`--storage` selects the LXC root filesystem storage. `VM_STORAGE` and
`ISO_STORAGE` select the range VM and ISO stores. If those environment variables
are omitted in non-interactive mode, both default to `local`.

`--nameserver` is assigned to the LXC and persisted as the upstream used by
dnsmasq for the template network.

You can supply an existing API token with `--token-id` and `--token-secret`.
Omit both when running as `root` if the installer should create the token.
Secrets passed on the command line may be visible in the process list and shell
history.

Run `./install.sh --help` for the complete option list.
`ludus-installer --build-version` prints the native executable's build version;
`--version TAG` selects the release to install, rather than printing a version.

## Use a local appliance archive

`--template-file` installs an appliance already on the Proxmox host and implies
`--server-only`:

```shell
./install.sh \
  --template-file /root/ludus-2.3.0-debian13-amd64.tar.zst
```

The installer infers the Ludus version from an archive that follows the release
filename format. Pass `--version` if the file was renamed.

## Add a private CA to built templates

Use `--ca-certificate` when an internal APT mirror or artifact server uses a
private certificate authority:

```shell
./install.sh \
  --server-only \
  --ca-certificate /root/lab-root-ca.crt
```

The input must be a readable PEM X.509 certificate. The installer stores it at:

```text
/opt/ludus/install/injected-ca-certificate.crt
```

Ludus adds that certificate to the root trust store of each built-in Linux and
Windows template during its Packer build. Templates built before the certificate
was added must be rebuilt or updated manually.

This option does not change the Proxmox host trust store or the Ludus
container's system trust store. It also runs after the guest operating system is
installed, so it cannot fix TLS trust required by a net installer before the
Ansible provisioning stage.

## Check the installation

Run these commands on the Proxmox node that hosts the container:

```shell
pct status 900
pct exec 900 -- systemctl is-active ludus ludus-admin
pct exec 900 -- test -f /opt/ludus/install/.bootstrap-complete
pct exec 900 -- tail -100 /opt/ludus/install/install.log
```

The API listens on TCP 8080. The admin API listens on TCP 8081 inside the
container and is bound to localhost unless `expose_admin_port` is enabled.
Use the public API for normal CLI operations; supported admin operations are
proxied internally. The Proxmox host's localhost is not the container's localhost.

Run `ludus-install-status` as root on Proxmox to check the LXC selected by
`/etc/ludus-lxc.json`. It can also be run directly inside the container. It checks
both services and API health using the configured ports. `--credentials` displays
initial-admin credentials only when the initial-admin marker exists, and never
rotates an API key. A migrated installation without that marker retains its
existing users; the status command does not create an administrator.
The status helper is the native installer exposed as `ludus-install-status`,
not a Python script. On the host its executable is
`/usr/local/libexec/ludus-installer`; inside the LXC it is
`/opt/ludus/install/ludus-installer`. Either executable also accepts `--status`
and optional `--credentials`.
WireGuard listens on UDP 51820.

The server configuration is `/opt/ludus/config.yml` inside the LXC. Restart both
services after changing it:

```shell
pct exec 900 -- systemctl restart ludus ludus-admin
```

If importing an existing installation, follow
[Migrate to LXC](../infrastructure-operations/migrate-to-lxc.md).

## Install on an offline Proxmox cluster

:::note

Offline installation is a Ludus Enterprise feature. The offline installer,
`install-offline.sh`, is distributed with Ludus Enterprise.

:::

An offline install needs more than the LXC archive. The archive contains the
Ludus server runtime, bundled Ansible content, Packer plugins, Python wheels, and
the Blocky binary used by range routers. It does not contain operating system
ISOs, Linux package repositories, Chocolatey packages, Office or Visual Studio
installers, or files downloaded by user-supplied Ansible roles.

The simplest reliable process is:

1. Stage the Ludus Enterprise offline installer, the same release's `ludus-installer`, and signed media before disconnecting the cluster.
2. Build every required Proxmox VM template while the cluster still has access
   to its package and artifact sources.
3. Preload any package caches using the same range configuration that will be
   deployed offline.
4. Block Internet access and deploy a canary range before the maintenance or
   exercise window.

If the cluster must be offline from its first boot, provide internal mirrors for
every required URL or import VM templates prepared elsewhere.

### Stage the offline distribution

Use the Ludus Enterprise `install-offline.sh`, not the public installer URL or
`install.sh --airgapped`. Place it beside the **same release's
`ludus-installer` executable**. It verifies that binary against the signed
manifest before running it in offline mode. It does not source or require
`install.sh` and never downloads a replacement.

Stage the following on a connected preparation system:

- `install-offline.sh` and the matching `ludus-installer` executable.
- The versioned `ludus-<version>-debian13-amd64.tar.zst` appliance.
- Every pinned ISO listed by `airgappedISOFilenames` in the root
  `ludus-installer/` module, including both VirtIO images, in one local directory.
- `bginfo.exe` from `https://live.sysinternals.com/bginfo.exe`. The appliance
  does not include BgInfo; Ludus copies it to Windows range VMs.
- A SHA-256 manifest, its detached signature, and a trusted release-signing
  public key. The manifest must cover `ludus-installer`, the appliance, all ISOs,
  `bginfo.exe`,
  and any plugin, offline license, or state archive supplied to the installer.
- Any site-specific CA certificate required by the environment.

Verify the distribution before executing it. Offline `--prefix` is rejected:
this entry point installs only the server, not the client. On the disconnected
Proxmox node, run it using a shared storage pool that supports ISO content:

```shell
VERSION=2.4.0
MEDIA=/root/ludus-offline

sudo bash "${MEDIA}/install-offline.sh" \
  --template-file "${MEDIA}/ludus-${VERSION}-debian13-amd64.tar.zst" \
  --version "${VERSION}" \
  --iso-directory "${MEDIA}/isos" \
  --iso-storage shared-isos \
  --bginfo-file "${MEDIA}/bginfo.exe" \
  --checksum-file "${MEDIA}/checksums.txt" \
  --checksum-signature "${MEDIA}/checksums.txt.sig" \
  --checksum-public-key "${MEDIA}/release-signing.pub" \
  --bridge vmbr0 \
  --nameserver 10.0.0.53 \
  --ca-certificate "${MEDIA}/site-root-ca.crt"
```

Omit `--ca-certificate` if the environment does not use a private CA. A version
can also be inferred from the standard appliance filename; an unrecognized
filename requires `--version` and never triggers public release discovery.
`--skip-verification` is only for deliberately unsigned development media; it
does not waive the requirement for all local files or shared ISO storage.

For a host-to-LXC migration, add `--migrate-host` and follow the
[migration prerequisites](../infrastructure-operations/migrate-to-lxc.md).
Install `conntrack` (migration) or `iptables-persistent` (fresh install) on the
host before disconnecting it, unless WireGuard forwarding is disabled: the
offline entry point never installs missing host dependencies. It does not
support updates of an existing LXC.

The shared installer retains `--template-file` for CI and development, but that
option alone does not enable the supported air-gap workflow.

### Offline infrastructure requirements

Provide these services inside the disconnected environment:

| Requirement | Why it is needed |
| --- | --- |
| Local DNS | Proxmox nodes, the Ludus LXC, template-build VMs, and range VMs must resolve internal mirrors and each other. `--nameserver` assigns the LXC resolver and dnsmasq upstream; set `router.upstream_dns` to internal resolvers for deployed ranges. |
| Local NTP | Kerberos, Active Directory, TLS validation, package metadata, and cluster operation depend on synchronized clocks. |
| Proxmox API reachability | The LXC must reach TCP 8006 on every configured endpoint. Proxmox nodes must remain quorate. |
| Internal artifact service | Packer source ISOs and any files fetched by template or role provisioning must be reachable from the Proxmox nodes and build VMs. |
| APT mirror or cache | Debian and Kali net installers need package indexes and packages. The APT mirror must allow HTTP connections so the installers can access them before injected with the CA certificate |
| Windows package source | Any requested Chocolatey, Office, Visual Studio, .NET, browser, or tool installation must be available internally. |
| Local license file | Community edition works without license activation. Pro or Enterprise air-gapped deployments need an offline license file issued before disconnection. |

The LXC has two interfaces: `eth0` on the bridge selected with `--bridge` and
`eth1` on `ludusnat` at `192.0.2.253/24`. An optional management VLAN can be set
with `--vlan-tag`. Internal mirrors must be reachable from the systems that use
them; reachability from only the Proxmox management addresses is insufficient.

For a licensed offline installation, also pass `--enterprise-plugin`,
`--license-file`, and `--license` to `install-offline.sh`. Include the plugin and
license in the signed artifact manifest. The wrapper installs the license at
`/opt/ludus/license.lic` before services restart; the configured license key
decrypts it. Without these three options, the Enterprise plugin and license are
not installed.

### ISOs and template builds

A fresh Ludus install has no built VM templates. Every range VM must clone an
existing Proxmox template, so build or import all required templates before the
no-egress test.

The current built-in template inputs are:

| Template | Required installation media and sources |
| --- | --- |
| Debian 13 | `debian-13.7.0-amd64-netinst.iso` and a Debian 13 APT repository |
| Debian 11 | `debian-11.7.0-amd64-netinst.iso` and a Debian 11 APT repository |
| Debian 12 | `debian-12.14.0-amd64-netinst.iso` and a Debian 12 APT repository |
| Kali | `kali-linux-2026.1-installer-netinst-amd64.iso`, a Kali package repository, and the pinned KasmVNC package bundled with the template |
| Windows 11 Enterprise 22H2 | Windows Enterprise evaluation ISO and `virtio-win-0.1.240.iso` |
| Windows Server 2022 | Windows Server evaluation ISO and `virtio-win-0.1.229.iso` |

Check the corresponding `.pkr.hcl` file and bundled `http/` assets in the Ludus
source for the authoritative inputs and checksums. These versions change as
template definitions are updated. Keep checksum validation when redirecting an
ISO URL to an internal server.

:::warning

The offline installer verifies and stages all pinned ISOs in the selected shared
pool and sets `airgapped_install: true`. The built-in Packer definitions then use
local ISO volume IDs instead of their public download URLs. Custom template
definitions may still contain external URLs: mirror or replace those sources,
or import compatible Proxmox templates built elsewhere. Staging ISOs does not
provide the package repositories used later in the guest installation.

:::

A manually built template still needs QEMU Guest Agent, DHCP, SSH and Python for
Linux, or HTTPS WinRM and PowerShell for Windows. It must use the credentials
Ludus expects and be in the `SHARED` pool. See
[Non-automated OS template builds](../using-ludus/templates.md#non-automated-os-template-builds).

Build range-router templates from a current Ludus release: their template builds
install the packages needed for offline router configuration. The LXC supplies
Blocky to routers during range deployment, so they do not download it from GitHub.

### APT mirror or cache

The Debian templates use netinst media, run a full package upgrade, and install
packages from `deb.debian.org`. The Kali installer uses `kali.download`, while
the installed system uses `http.kali.org`; the latter normally redirects back
to `kali.download`. Internal DNS must map both names to the complete Kali
mirror. Mirror the signed `Contents-amd64` indexes as well as package indexes
and packages. For `debmirror`, this requires `--getcontents`.

An APT caching proxy must be populated before egress is removed. A cache miss is
an Internet request and will fail. For predictable air-gapped operation, a
snapshot mirror is safer than an on-demand proxy cache.

The built-in Debian preseed files leave `mirror/http/proxy` empty. To use a
normal explicit proxy, copy the template and set the preseed proxy value. The
other choices are a transparent cache or an internal mirror that answers the
hostname and path already configured in the template.

If the mirror uses a private HTTPS CA, pass that CA to the LXC installer before
building templates. Remember that the injected CA is installed during Ansible
provisioning, after the base operating system install. The net installer itself
must use HTTP, a publicly trusted certificate, or another method of receiving
the CA.

### Chocolatey and the Nexus cache

Ludus checks for its Nexus service at `192.0.2.2:8081`. When reachable, Windows
package tasks use:

```text
http://192.0.2.2:8081/repository/raw/install.ps1
http://192.0.2.2:8081/repository/chocolatey/
```

Deploy Nexus and change its Chocolatey proxy to NuGet V2 while the cluster is
connected. Follow [Nexus cache](../infrastructure-operations/nexus-cache.md).
Then deploy a Windows canary with the exact package list that the offline range
will use. This primes the package metadata and `.nupkg` files.

A Chocolatey proxy does not guarantee that a package works offline. Many
packages contain scripts that download an installer from the software vendor.
Those vendor payloads must also be hosted internally or repackaged into an
internal Chocolatey package. This applies in particular to browsers, Office,
Visual Studio workloads, and packages that always install their latest release.

The offline installer sets `airgapped_install: true` in `/opt/ludus/config.yml`,
so Office 2016, 2019, and 2021 provisioning uses the pinned Chocolatey package
version instead of querying the GitHub API. Visual Studio and Office installers
still download large payloads from Microsoft unless those vendor sources are
mirrored internally.

For a range that does not need those tools, keep the Windows configuration
small:

```yaml
windows:
  install_additional_tools: false
```

Also omit `chocolatey_packages`, `office_version`, and
`visual_studio_version`. The bundled `simple-domain.yml` CI configuration does
this and can deploy from prebuilt templates without Chocolatey.

If Nexus is down, Ludus falls back to the public Chocolatey source. On an
offline cluster that fallback waits for network timeouts and then fails, so test
that `192.0.2.2:8081` is reachable before deploying a range that needs packages.

### Roles, collections, and other downloads

The LXC appliance includes the Ansible collections and roles shipped with that
Ludus release. Install additional sources, roles, collections, and template
definitions before disconnecting the cluster. Their installation may use Git,
Ansible Galaxy, or another remote registry.

Audit every user role for `get_url`, `uri`, package-manager, Git, PowerShell, and
shell download tasks. Mirror each referenced artifact and update the role to use
its internal URL. Include nested installers: downloading a bootstrap script is
not enough if that script downloads more files when it runs.

Configure `router.upstream_dns` to use an internal resolver if range VMs need
DNS outside their own range:

```yaml
router:
  upstream_dns:
    - 192.0.2.10
```

### Offline readiness checklist

Before removing egress, verify all of the following:

- `install-offline.sh`, its matching `ludus-installer`, the LXC archive, all
  pinned ISOs, `bginfo.exe`, and the signed manifest/signature/trusted key are
  stored locally.
- The Proxmox cluster is quorate and all configured API endpoints answer.
- DNS and NTP work without Internet access.
- `ludus templates list` reports `BUILT` for every template in the range.
- Required ISO URLs and package repositories resolve to internal services.
- The APT mirror contains all packages needed by a clean template build.
- Nexus and every vendor payload required by the Windows package list are
  already cached or mirrored.
- `airgapped_install: true` is set in `/opt/ludus/config.yml`.
- User roles and custom templates have no unresolved external URLs.
- Private CA certificates were supplied before the templates were built.
- A complete canary range deploys while egress is blocked.

During the canary deployment, follow `ludus range logs -f` and check
`ludus range errors`. A template build that waits for SSH or WinRM often means
the guest installer could not reach an ISO, package repository, or answer file.
APT errors identify a missing suite, metadata file, package, CA, or valid clock.
Chocolatey timeouts usually mean Nexus was unreachable or a package attempted a
second-stage vendor download.
