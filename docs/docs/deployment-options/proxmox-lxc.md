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

- `bash`, `curl`, `grep`, and `python3`
- `openssl` when using `--ca-certificate`
- a healthy Proxmox cluster with quorum
- a storage pool that supports LXC root filesystems
- VM and ISO storage available to every node that will build or run VMs
- an address on `vmbr0` for the Ludus container, supplied by DHCP or configured
  statically
- working routing between the container and every Proxmox API endpoint

The installer creates an unprivileged LXC with 4 vCPUs, 4 GiB of memory, 512 MiB
of swap, and a 20 GiB root filesystem. The compressed appliance is about 200 MB;
the exact size changes between releases.

In a multi-node cluster, allow Proxmox SDN traffic, including VXLAN UDP 4789,
between the nodes. The installer creates a VXLAN zone named `ludus`, a VNet
named `ludusnat`, and the `192.0.2.0/24` subnet. Do not reuse those names or that
subnet for unrelated infrastructure.

:::warning

The installer creates or reuses cluster-wide SDN objects and creates a
non-privilege-separated `root@pam!ludus` API token by default. Review the script
and take a Proxmox configuration backup before running it on a cluster that
already has custom SDN configuration.

:::

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
| LXC rootfs storage | Storage used for the 20 GiB container root filesystem. |
| LXC IP and gateway | Address on `vmbr0`. A static address is recommended for an API server. |
| WireGuard endpoint | IP address or hostname that Ludus clients use for UDP 51820. |
| VM storage | Proxmox storage for range VMs and templates. It must be available on the intended nodes. |
| ISO storage | Proxmox storage used while building VM templates. |
| License | `community` or a Ludus license key. |
| CA certificate | Optional PEM root CA copied into templates built by Ludus. |

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
  --endpoints "https://10.20.30.11:8006 https://10.20.30.12:8006 https://10.20.30.13:8006" \
  --wg-endpoint ludus.example.com \
  --license community
```

`--storage` selects the LXC root filesystem storage. `VM_STORAGE` and
`ISO_STORAGE` select the range VM and ISO stores. If those environment variables
are omitted in non-interactive mode, both default to `local`.

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

1. Stage the installer and LXC appliance before disconnecting the cluster.
2. Build every required Proxmox VM template while the cluster still has access
   to its package and artifact sources.
3. Preload any package caches using the same range configuration that will be
   deployed offline.
4. Block Internet access and deploy a canary range before the maintenance or
   exercise window.

If the cluster must be offline from its first boot, provide internal mirrors for
every required URL or import VM templates prepared elsewhere.

### Stage the installer and appliance

On a connected Linux system, download one release and verify it before transfer:

```shell
VERSION=2.3.0
BASE="https://lxc.ludus.cloud/ludus-lxc/${VERSION}"
IMAGE="ludus-${VERSION}-debian13-amd64.tar.zst"

curl -fLO https://ludus.cloud/install
mv install install.sh
curl -fLO "${BASE}/${IMAGE}"
curl -fLO "${BASE}/checksums.txt"
sha256sum -c checksums.txt --ignore-missing
```

Transfer `install.sh`, the `.tar.zst` archive, and `checksums.txt` through the
approved path into the offline environment. Verify the checksum again after the
transfer.

Run the local install on a Proxmox node:

```shell
chmod 0755 /root/install.sh
/root/install.sh \
  --template-file "/root/ludus-${VERSION}-debian13-amd64.tar.zst" \
  --version "$VERSION" \
  --ca-certificate /root/lab-root-ca.crt
```

Omit `--ca-certificate` if the environment does not use a private CA. With a
local template and an explicit or filename-derived version, the server-only
installer does not need to contact the Ludus release servers.

### Offline infrastructure requirements

Provide these services inside the disconnected environment:

| Requirement | Why it is needed |
| --- | --- |
| Local DNS | Proxmox nodes, the Ludus LXC, template-build VMs, and range VMs must resolve internal mirrors and each other. The default range router forwards external queries to Cloudflare DNS-over-HTTPS, which will not work without egress. |
| Local NTP | Kerberos, Active Directory, TLS validation, package metadata, and cluster operation depend on synchronized clocks. |
| Proxmox API reachability | The LXC must reach TCP 8006 on every configured endpoint. Proxmox nodes must remain quorate. |
| Internal artifact service | Packer source ISOs and any files fetched by template or role provisioning must be reachable from the Proxmox nodes and build VMs. |
| APT mirror or cache | Debian and Kali net installers need package indexes and packages. Netinst ISOs alone are not enough. |
| Windows package source | Any requested Chocolatey, Office, Visual Studio, .NET, browser, or tool installation must be available internally. |
| Local license file | Community edition works without license activation. Pro or Enterprise air-gapped deployments need an offline license file issued before disconnection. |

The LXC has two interfaces: `eth0` on `vmbr0` and `eth1` on `ludusnat` at
`192.0.2.253/24`. Internal mirrors must be reachable from the systems that use
them. A mirror reachable only from a Proxmox management address may still be
unreachable from a template-build VM or a range VM.

For Pro or Enterprise, copy the signed offline license into the container and
restart the services:

```shell
pct push 900 /root/license.lic /opt/ludus/install/license.lic --perms 0600
pct exec 900 -- chown root:root /opt/ludus/install/license.lic
pct exec 900 -- systemctl restart ludus ludus-admin
```

The configured license key is used to decrypt that file.

### ISOs and template builds

A fresh Ludus install has no built VM templates. Every range VM must clone an
existing Proxmox template, so build or import all required templates before the
no-egress test.

The current built-in template inputs are:

| Template | Required installation media and sources |
| --- | --- |
| Debian 11 | `debian-11.7.0-amd64-netinst.iso` and a Debian 11 APT repository |
| Debian 12 | `debian-12.14.0-amd64-netinst.iso` and a Debian 12 APT repository |
| Kali | `kali-linux-2026.1-installer-netinst-amd64.iso`, a Kali package repository, and the KasmVNC package used by the template playbook |
| Windows 11 Enterprise 22H2 | Windows Enterprise evaluation ISO and `virtio-win-0.1.240.iso` |
| Windows Server 2022 | Windows Server evaluation ISO and `virtio-win-0.1.229.iso` |

Check the corresponding `.pkr.hcl` file in the Ludus source for the authoritative
URL and checksum. These versions change as template definitions are updated.
Keep the checksum validation when redirecting a URL to an internal server.

:::warning

Do not assume that uploading an ISO to Proxmox storage is sufficient. The
built-in Packer definitions contain `iso_url` values and may retrieve those
URLs through the Proxmox download API. Test reuse of the staged ISO, make the
configured URL available through an internal mirror, copy the template
definition and point it at an internal HTTP(S) URL, or import a compatible
Proxmox template built elsewhere.

:::

A manually built template still needs QEMU Guest Agent, DHCP, SSH and Python for
Linux, or HTTPS WinRM and PowerShell for Windows. It must use the credentials
Ludus expects and be in the `SHARED` pool. See
[Non-automated OS template builds](../using-ludus/templates.md#non-automated-os-template-builds).

The Debian 11 template is also the default range router. Build it from a current
Ludus release: its template build installs the packages needed for offline
router configuration. The LXC supplies Blocky to the router during range
deployment, so the router does not download Blocky from GitHub.

### APT mirror or cache

The Debian templates use netinst media, run a full package upgrade, and install
packages from `deb.debian.org`. The Kali template installs its desktop and tools
from Kali repositories. Prepare the exact distribution suites, components, and
`amd64` packages referenced by the templates.

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

Set `airgapped_install: true` in `/opt/ludus/config.yml` so Office 2016, 2019,
and 2021 provisioning uses the pinned Chocolatey package version instead of
querying the GitHub API. Visual Studio and Office installers still download
large payloads from Microsoft unless those vendor sources are mirrored
internally.

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

- The installer, LXC archive, and checksums are stored inside the environment.
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
