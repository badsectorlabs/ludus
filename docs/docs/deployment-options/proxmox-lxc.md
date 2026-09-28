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

Run the installer as `root` on an amd64 Proxmox VE 9 host. The host needs:

- `bash`, `curl`, `grep`, and `python3`
- `openssl` when using `--ca-certificate`
- a healthy Proxmox cluster with quorum
- a storage pool that supports LXC root filesystems
- VM and ISO storage available to every node that will build or run VMs
- an address on a Proxmox bridge available on every potential LXC node, supplied
  by DHCP or configured statically
- working routing between the container and every Proxmox API endpoint

The installer creates an unprivileged LXC with 4 vCPUs, 4 GiB of memory, and
512 MiB of swap. The root filesystem defaults to 20 GiB and can be enlarged
with `--rootfs-size`. The compressed appliance size changes between releases.

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

The interactive installer asks for:

| Setting | Use |
| --- | --- |
| Proxmox API endpoints | Space-separated `https://node:8006` URLs. Include every node that Ludus may manage. |
| LXC VMID | VMID for the Ludus container. The default is the next free cluster VMID. |
| LXC rootfs storage and size | Storage used for the container root filesystem. The minimum supported size is 20 GiB. |
| LXC hostname | Hostname assigned to the Ludus container. |
| LXC bridge and VLAN | Management bridge available on every potential LXC node, plus an optional VLAN tag. |
| LXC IP, gateway, and DNS | Static address and gateway, or DHCP with a stable reservation. An explicit internal resolver is recommended offline. |
| WireGuard endpoint and port | Address and UDP port that Ludus clients use to reach the container. |
| VM storage and format | Proxmox storage for range VMs and templates, plus its supported `qcow2` or `raw` disk format. |
| ISO storage | Shared Proxmox storage used while building VM templates. |
| License | `community` or a Ludus license key. |
| CA certificate | Optional site-supplied PEM root CA copied into the LXC and templates built by Ludus. |

When run as `root`, the installer creates `root@pam!ludus` if no token was
provided. If that token already exists, the interactive installer asks whether
to recreate it or use its existing secret.

The installer then:

1. Validates the token against the local Proxmox API.
2. Creates and applies the `ludus` SDN zone and `ludusnat` VNet.
3. Downloads and verifies the release LXC appliance, unless it is already in
   `/var/lib/vz/template/cache`.
4. Creates and starts the container.
5. Writes `/opt/ludus/config.yml` inside the container.
6. Waits up to five minutes for the first-boot bootstrap.

The final output contains the Ludus API address and the WireGuard endpoint.
Continue with [Create additional users](../quick-start/create-a-user.md) and
[Build templates](../quick-start/build-templates.md).

## Non-interactive install

Use `--no-prompt` for automation. Supply a static LXC address or provide
`--wg-endpoint` explicitly when using DHCP.

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
WireGuard listens on UDP 51820.

The server configuration is `/opt/ludus/config.yml` inside the LXC. Restart both
services after changing it:

```shell
pct exec 900 -- systemctl restart ludus ludus-admin
```

If importing an existing installation, follow
[Migrate to LXC](../infrastructure-operations/migrate-to-lxc.md).

## Install on an offline Proxmox cluster

An offline install needs more than the LXC archive. The archive contains the
Ludus server runtime, bundled Ansible content, Packer plugins, Python wheels, and
the Blocky binary used by range routers. It does not contain operating system
ISOs, Linux package repositories, Chocolatey packages, Office or Visual Studio
installers, or files downloaded by user-supplied Ansible roles.

The simplest reliable process is:

1. Stage the separate offline installer, its matching shared installer, and signed media before disconnecting the cluster.
2. Build every required Proxmox VM template while the cluster still has access
   to its package and artifact sources.
3. Preload any package caches using the same range configuration that will be
   deployed offline.
4. Block Internet access and deploy a canary range before the maintenance or
   exercise window.

If the cluster must be offline from its first boot, provide internal mirrors for
every required URL or import VM templates prepared elsewhere.

### Stage the offline distribution

Use `install-offline.sh`, not the public installer URL or `install.sh --airgapped`.
The offline entry point must be supplied alongside the **same release's
`install.sh`**. It sources the shared provisioning code locally and never
downloads a replacement. Public installer uploads do not include
`install-offline.sh`; obtain the two-script distribution through your approved
offline delivery channel.

Stage the following on a connected preparation system:

- Both installer scripts from the same release.
- The versioned `ludus-<version>-debian13-amd64.tar.zst` appliance.
- Every pinned ISO listed by `AIRGAPPED_ISO_FILENAMES` in `install-offline.sh`,
  including both VirtIO images, in one local ISO directory.
- A SHA-256 manifest, its detached signature, and a trusted release-signing
  public key. The manifest must cover the appliance, all ISOs, and any plugin,
  offline license, or state archive supplied to the installer.
- Any site-specific CA certificate required by the environment.

Verify the distribution before executing either script. On the disconnected
Proxmox node, run the offline entry point using a shared storage pool that
supports ISO content:

```shell
VERSION=2.4.0
MEDIA=/root/ludus-offline

sudo bash "${MEDIA}/install-offline.sh" \
  --template-file "${MEDIA}/ludus-${VERSION}-debian13-amd64.tar.zst" \
  --version "${VERSION}" \
  --iso-directory "${MEDIA}/isos" \
  --iso-storage shared-isos \
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
Install `conntrack` on the host before disconnecting it: the offline entry point
never installs missing host dependencies. It does not support updates of an
existing LXC.

The shared installer retains `--template-file` for CI and development, but that
option alone does not enable the supported air-gap workflow. The distribution
split does not change runtime licensing or restrict access to repository source.

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
decrypts it. Community installations can omit those three options.

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

- `install-offline.sh`, its matching `install.sh`, the LXC archive, all pinned
  ISOs, and the signed manifest/signature/trusted key are stored locally.
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
