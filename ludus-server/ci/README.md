# Ludus CI

GitLab CI runs on a self-hosted custom-executor GitLab Runner installed on a
Proxmox host. The runner host keeps a small set of protected seed templates.
Each test series clones the seed it needs, runs against that clone, and then
destroys the clone only if the whole series succeeds. Failed series leave their
clone running for troubleshooting.

The pipeline is defined in `/.gitlab-ci.yml`. The custom executor scripts in
this directory live both in the repo and on the runner host at `/opt/ludus/ci/`.
The runner uses the deployed copies in `/opt/ludus/ci/`.

## Runner Host

The custom executor is wired up to:

```toml
prepare_exec = "/opt/ludus/ci/prepare.sh"
run_exec     = "/opt/ludus/ci/run.sh"
cleanup_exec = "/opt/ludus/ci/cleanup.sh"
```

`cleanup_exec` is intentionally a no-op. Successful clone removal is done by
explicit GitLab jobs in `release-claim`; failed clones are preserved.

## CI generations

The executor supports two explicit profiles through `ci-profile.sh`:

- Missing `LUDUS_CI_PROFILE`, or `host-2.3`: existing host-installed 2.3.x
  fixtures and runtime VMs. Existing maintenance branches need no YAML changes.
- `LUDUS_CI_PROFILE: lxc-2.4`: independent LXC-based fixtures beginning at VMID
  2400. The custom executor receives `CUSTOM_ENV_LUDUS_CI_PROFILE`.
- Unknown profiles fail before VM preparation. Branch names, tags and merge
  request refs do not determine the profile.

Permanent 2.4 fixtures are provisioned as independent full clones
(`qm clone --full 1`), with no dependency on legacy VM disks. During CI, both
profiles create disposable thin-linked clones (`qm clone --full 0`) on the
seed's storage. These job clones depend on their protected seed until released;
keep the seed intact while any job clone exists.

The 2.4 pipeline updates the server inside the container recorded by
`/etc/ludus-lxc.json`. `job-server.sh` keeps Proxmox commands on the nested host,
but executes server updates and server-filesystem operations inside the LXC.
Every operational seed must have that helper at `/opt/ludus/ci/job-server.sh`
before runner preparation. Candidate build artifacts refresh job helpers and
fixtures even for `GIT_STRATEGY: none`.


## VM Topology

### Dynamic Clone Seeds

These VMIDs are protected source templates. They are not used directly by test
jobs. The following IDs belong to the legacy `host-2.3` profile.

| VMID | Name                         | Starting state                         |
|------|------------------------------|----------------------------------------|
| 1000 | `ci-seed-base`               | Debian 13 CI base OS                   |
| 1001 | `ci-seed-clean-install`      | Ludus installed                        |
| 1002 | `ci-seed-templates-built`    | Ludus installed and templates built    |
| 1003 | `ci-seed-range-admin`        | Admin range deployed                   |
| 1004 | `ci-seed-range-user`         | User range deployed                    |
| 1007 | `ci-seed-integration`        | User range deployed for integration    |

`base.sh` maps `LUDUS_BUILD_TYPE` and `LUDUS_SNAPSHOT_NAME` to these seeds:

| Build type / snapshot             | Seed VMID |
|-----------------------------------|-----------|
| `full`                            | 1000      |
| `from-snapshot` / `clean_install` | 1001      |
| `from-snapshot` / `templates_built` | 1002    |
| `from-snapshot` / `range_built_admin` | 1003 |
| `from-snapshot` / `range_built_user` | 1004  |
| `from-snapshot` / `integration_ready` | 1007 |

### Runtime VMs

| VMID | Role                                           |
|------|------------------------------------------------|
| 1005 | Cluster node 1                                 |
| 1006 | Cluster node 2                                 |
| 1012 | Build VM for Go/web UI/docs/publishing jobs    |

Cluster jobs still use the explicit cluster lock because they share VMIDs
1005/1006. During bootstrap, `ci-vm-setup.yml` configures those VMs with
`lae.proxmox` as a two-node nested Proxmox cluster: node `ludus` on VMID 1005,
node `ludus2` on VMID 1006, RBD storage `ceph` for VM disks, and CephFS storage
`cephfs` for ISOs. Build jobs still use the dedicated build VM.

The `lxc-2.4` profile uses:

- `2400`, `ci-24-seed-base`: bare nested Proxmox, no Ludus server or container.
- `2401`, `ci-24-seed-clean-install`: migrated LXC installation.
- `2402`, `ci-24-seed-templates-built`: migrated installation with built templates.
- `2403` / `2404`: migrated admin/user range seeds.
- `2405` / `2406`: independent nested cluster nodes, never the legacy cluster.
- `2407`, `ci-24-seed-integration`: migrated integration-ready seed.
- `2409`, `ci-24-seed-legacy-migration`: an independent legacy installation,
  retained for migration tests rather than migrated during provisioning.
- `2412`, `ci-24-build-machine`: independent build/docs/publishing environment.

The entire 2400–2412 block is reserved. Disposable LXC-profile clones use the
first available VMID from 2413 upward. `from-snapshot/proxmox` selects 2400;
`from-snapshot/legacy_migration` selects 2409. The other snapshot names retain
their meanings but select the corresponding new seeds.

After restoring a 2.4 cluster RAM snapshot, preparation restarts OSDs alongside
the Ceph monitors, managers and MDS daemons to discard restored connections and
leases. The legacy profile's recovery sequence is unchanged.


## How Dynamic Clones Work

For non-cluster tests, `prepare.sh` calls `base.sh:resolve_vm()`. That function:

1. Derives a CI series from `LUDUS_CI_SERIES` or the GitLab job name.
2. Derives the source seed from `LUDUS_BUILD_TYPE` and `LUDUS_SNAPSHOT_NAME`.
3. Creates or reuses an assignment file at
   `/opt/ludus/ci/vm-assignments/<pipeline-id>-<series>.env`.
4. Clones the source seed to a per-pipeline VM named
   `ci-<pipeline-id>-<series>-<source>`.
5. Starts the clone, assigns it a unique CI-network control IP via QEMU guest
   agent, and runs the job script over SSH as `gitlab-runner`.

Sequential jobs in the same series use the same clone, so state carries through
the series. Independent series get independent clones and can run in parallel.

Current series:

| Series                | Jobs                                                           |
|-----------------------|----------------------------------------------------------------|
| `install`             | `install kickoff` -> `install check`                           |
| `templates`           | `templates build` -> `templates check`                         |
| `client-basic`        | `client basic-commands`                                        |
| `range-admin`         | `range deploy-admin` -> `range check-admin`                    |
| `post-deploy-admin`   | all `post-deploy *-as-admin` jobs                              |
| `range-user`          | `range deploy-user` -> `range check-user`                      |
| `post-deploy-user`    | all `post-deploy *-as-user` jobs                               |
| `integration`         | `test-everything` from `ci-seed-integration`                   |

Testing-mode jobs check completed guest curl exit codes for blocked HTTPS:
DNS failure (`6`), connection failure (`7`), or timeout (`28`), not curl's
version-dependent error text. After allowing a domain, they clear the forwarding
DC and Windows client's DNS caches so the probe uses the router's newly pinned
address rather than a cached pre-allow answer.

The `release <series>-vm` jobs in `release-claim` call
`/opt/ludus/ci/release-vm.sh`. They run only when their upstream jobs succeed.
If any job in the series fails, the release job does not run and the clone stays
online.

LXC-profile assignments and cluster locks use the `lxc-2.4-` directory prefix;
clone names start with `ci-24-`. Legacy assignment paths and names are retained.
Both profiles share the control-IP allocator, lock, and lease directory, with
profile-qualified lease filenames. Do not create independent allocators over
the same IP range.

## Provisioning the independent 2.4 environment

Use `provision-ci.py`, or `LUDUS_CI_PROFILE=lxc-2.4 bootstrap-ci-host.sh`.
The latter dispatches before any legacy bootstrap side effects. **Never use
the legacy `CI_RECREATE=1` path for this rollout.**

`ci-provision-inventory.json` describes roles, names and resources; VMIDs come
from `ci-profile.sh`. The provisioner accepts existing destinations only when
both their name and JSON description match its owner and role. There is no
automatic destroy, replacement, or adoption operation. Each phase is explicit;
failed phases retain their destinations for inspection.

Prepare a network JSON file containing `bridge`, IPv4 `prefix`, `gateway`,
`dns` (one address), and `vms`. Each inventory role must have an `ip`, unique
`mac`, and management `interface` entry. Reserve those addresses outside dynamic
DHCP allocation before booting copies. For example, one role entry is:

```json
"base": {
  "ip": "203.0.113.220",
  "mac": "BC:24:24:00:00:00",
  "interface": "ens18"
}
```

Run on the outer Proxmox host with its existing runner SSH key/config and
`lae.proxmox` installed. The network phase boots copies with the NIC disconnected,
rewrites their own management configuration, includes `interfaces.d` so Proxmox
SDN can activate VNets, then reconnects them.

```sh
P=/path/to/ludus-server/ci/provision-ci.py
N=/path/to/network.json
export ANSIBLE_ROLES_PATH=/root/.ansible/roles

python3 "$P" clone --network "$N" --storage nvme
python3 "$P" network --network "$N"
python3 "$P" nested --network "$N" --roles base,cluster-node1,cluster-node2
python3 "$P" lxc-network --network "$N" \
  --roles base,clean-install,templates-built,range-admin,range-user,integration,cluster-node1,cluster-node2,build
python3 "$P" build --network "$N" --roles build
python3 "$P" cluster --network "$N" --roles cluster-node1,cluster-node2
```

The default private management bridge is `ci24mgmt`, gateway `172.31.24.1/24`,
with LXC address `172.31.24.2/24`. These addresses are isolated inside each
nested host; only that host's control address is on the outer CI network.
Fresh installer jobs use the same bridge/address settings. The migration
fixture retains its original network until its disposable test clone migrates.
Migrated standalone seeds use `https://172.31.24.1:8006` as their Proxmox
endpoint so each clone reaches its own nested host even while the seed is
stopped. The executor rewrites the container's CI-network WireGuard endpoint
to the clone's allocated control IP.

For `migrate` (roles `clean-install,templates-built,range-admin,range-user,integration,build`)
and `cluster-install` (roles `cluster-node1,cluster-node2`), supply:

```sh
--version "$VERSION" --installer "$MEDIA/install.sh" \
--template "$MEDIA/ludus-$VERSION-debian13-amd64.tar.zst" \
--server "$MEDIA/binaries/ludus-server" \
--client "$MEDIA/binaries/ludus-client_linux-amd64"
```

Supply signed checksum inputs (`--checksum-file`, `--checksum-signature`,
`--checksum-public-key`), or explicitly use `--skip-verification` only for
trusted local CI media. Populated enterprise installations additionally require
their matching `--enterprise-plugin`. Cluster installation creates one LXC on
the primary, uses API port 9090, and targets the new Ceph/CephFS cluster.
Its VXLAN zone supplies layer-2 connectivity; the provisioner explicitly assigns
the `192.0.2.254/24` NAT gateway and uplink masquerade rule to the primary node,
because VXLAN subnet settings do not configure host gateway/SNAT themselves.

Provisioning refreshes the server through its normal `--update` path, including
embedded Ansible, inventory and Packer resources; it never replaces only the
executable. A failed installation retains its VM for inspection. If the installer
created an API token before rolling back, inspect that new VM and remove only
that failed attempt's unused token before retrying; never revoke a legacy token.

After migration, provisioning removes any stale `lo.dnsmasq` resolver registration
when host `dnsmasq` is inactive. This lets the management interface's upstream
resolver take over instead of leaving host DNS pointed at an inactive loopback
service. Build-VM readiness also requires resolving `gitlab.com` before freezing.

Deploy `job-server.sh` to the nested hosts before freezing. Finally run
`validate --network "$N" --version "$VERSION"` and then
`freeze --network "$N" --version "$VERSION"`. Seeds become stopped protected
templates; runtime VMs receive their first `clean` snapshot. Existing snapshots
are never silently replaced. Readiness receipts live in
`/var/lib/ludus-ci/lxc-2.4`; keep them with the provisioned environment.


## Legacy host setup (2.3.x only)

After `debian-13-x64-server-template` has been built in Ludus and a GitLab
Runner has been installed/registered on the host, run:

```sh
LUDUS_ADMIN_API_KEY=JD... ./ludus-server/ci/bootstrap-ci-host.sh
```

The script can also use explicit Proxmox credentials:

```sh
PROXMOX_USERNAME=john-doe@pam \
PROXMOX_PASSWORD='...' \
./ludus-server/ci/bootstrap-ci-host.sh
```

`bootstrap-ci-host.sh` copies the repo's CI scripts into `/opt/ludus/ci`, builds
seed Ludus binaries into `/opt/ludus/ci/binaries/`, creates
`debian-13-x64-server-ludus-ci-template` from the base Debian 13 template,
converts the existing GitLab Runner to the custom executor, and runs
`ci-vm-setup.yml` to create the seed templates and runtime VMs listed above.

Useful options:

| Variable            | Default | Purpose                                             |
|---------------------|---------|-----------------------------------------------------|
| `CI_RECREATE`       | `0`     | Destroy VMIDs 1000-1007 and 1012 before setup       |
| `CI_RECREATE_TEMPLATE` | `0`  | Also destroy the CI base template when recreating    |
| `CI_BUILD_BINARIES` | `1`     | Build `ludus-server` and Linux client seed binaries |
| `CI_TEMPLATE_PARALLEL` | `2`  | Per-seed template build parallelism                  |
| `CI_VM_DISK_SIZE`   | `250G`  | Root disk size for CI template, seeds, and runtime VMs |
| `CI_SETUP_TEMPLATE` | `auto`  | Create the CI base template if it does not exist    |
| `CI_SETUP_SEEDS`    | `1`     | Run `ci-vm-setup.yml`                               |
| `CI_CLUSTER_NODE1_IP` | `203.0.113.184` | Static CI-network IP for nested Proxmox node `ludus` |
| `CI_CLUSTER_NODE2_IP` | `203.0.113.185` | Static CI-network IP for nested Proxmox node `ludus2` |
| `CI_CLUSTER_CEPH_OSD_DISK_SIZE` | `100G` | Extra OSD disk size attached to each cluster VM |
| `CI_CLUSTER_CEPH_PG_NUM` | `32` | Placement group count used for CI Ceph pools |

Example:

```sh
cd /opt/ludus/ci
ansible-playbook ci-vm-setup.yml \
  -e api_user=gitlab-runner@pam \
  -e api_password="$(cat /opt/ludus/ci/.gitlab-runner-password)" \
  -e api_host=127.0.0.1 \
  -e node_name="$(hostname -s)" \
  -e ludus_install_path=/opt/ludus \
  -e proxmox_vm_storage_pool=zfs \
  -e proxmox_vm_storage_format=raw
```

## Environment Variables

Set per job in `.gitlab-ci.yml`:


`LUDUS_CI_PROFILE` selects the generation as described above. Keep it defined
at pipeline level so preparation, test jobs and release jobs agree.

| Variable              | Values                                                                 |
|-----------------------|------------------------------------------------------------------------|
| `LUDUS_BUILD_TYPE`    | `full`, `from-snapshot`, `any-built`, `clean-cluster`, `cluster`, `cluster-from-snapshot`, `cluster-claim`, `cluster-release`, `vm-release` |
| `LUDUS_SNAPSHOT_NAME` | `clean_install`, `templates_built`, `range_built_admin`, `range_built_user`, `integration_ready`, `cluster_range_built` |
| `LUDUS_INSTALL_STEP`  | `kickoff`, `check`, `take-cluster-snapshot`                            |
| `LUDUS_CI_SERIES`     | Optional explicit dynamic clone series name                            |

Runner-host settings in `base.sh`:

| Variable           | Default | Purpose                                      |
|--------------------|---------|----------------------------------------------|
| `CI_CLONE_FULL`    | `0`     | Enforced linked clones for disposable CI jobs; fixed-fixture provisioning remains full-clone |
| `CI_CLONE_IP_PREFIX` | `203.0.113` | CI control network prefix for dynamic clones |
| `CI_CLONE_IP_START` | `10`    | First host octet for dynamic clone control IPs |
| `CI_CLONE_IP_END` | `99`      | Last host octet for dynamic clone control IPs |
| `CI_CLONE_GATEWAY` | `203.0.113.254` | Gateway for dynamic clone control IPs |

## Deployment

After editing scripts in this directory, deploy them to the runner host:

```sh
scp ludus-server/ci/{ci-profile,base,claim-cluster,release-cluster,release-vm,prepare,prepare-cluster,run,cleanup,check-install-status,job-server}.sh \
    your-ludus-host:/opt/ludus/ci/
scp ludus-server/ci/bootstrap-ci-host.sh ludus-server/ci/provision-ci.py \
    ludus-server/ci/ci-provision-inventory.json your-ludus-host:/opt/ludus/ci/
scp ludus-server/ci/{ci-setup,ci-vm-setup,ci-nested-setup,ci-cluster-setup}.yml \
    your-ludus-host:/opt/ludus/ci/
ssh your-ludus-host 'chmod +x /opt/ludus/ci/*.sh'
```

`.gitlab-ci.yml` is read by GitLab from the committed ref on each pipeline run.
Local YAML changes do not affect GitLab until committed and pushed. Deploy
the profile-aware executor before enabling the new profile in a pipeline.
Runner registration and the legacy runtime VMs do not need to be changed.

## Debugging

```sh
glab ci status
ssh your-ludus-host 'ls -la /opt/ludus/ci/vm-assignments/'
ssh your-ludus-host 'qm list | grep -E "ci-[0-9]+-"'
ssh your-ludus-host 'qm terminal <vmid>'
```

To remove a successful clone manually:

```sh
ssh your-ludus-host 'CUSTOM_ENV_LUDUS_CI_PROFILE=lxc-2.4 CUSTOM_ENV_CI_PIPELINE_ID=<pipeline-id> CUSTOM_ENV_LUDUS_CI_SERIES=<series> /opt/ludus/ci/release-vm.sh'
```
