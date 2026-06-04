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

## VM Topology

### Dynamic Clone Seeds

These VMIDs are protected source templates. They are not used directly by test
jobs.

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

The `release <series>-vm` jobs in `release-claim` call
`/opt/ludus/ci/release-vm.sh`. They run only when their upstream jobs succeed.
If any job in the series fails, the release job does not run and the clone stays
online.

## Host Setup

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

| Variable              | Values                                                                 |
|-----------------------|------------------------------------------------------------------------|
| `LUDUS_BUILD_TYPE`    | `full`, `from-snapshot`, `any-built`, `clean-cluster`, `cluster`, `cluster-from-snapshot`, `cluster-claim`, `cluster-release`, `vm-release` |
| `LUDUS_SNAPSHOT_NAME` | `clean_install`, `templates_built`, `range_built_admin`, `range_built_user`, `integration_ready`, `cluster_range_built` |
| `LUDUS_INSTALL_STEP`  | `kickoff`, `check`, `take-cluster-snapshot`                            |
| `LUDUS_CI_SERIES`     | Optional explicit dynamic clone series name                            |

Runner-host settings in `base.sh`:

| Variable           | Default | Purpose                                      |
|--------------------|---------|----------------------------------------------|
| `CI_CLONE_FULL`    | `0`     | Linked clones by default; set `1` for full clones |
| `CI_CLONE_STORAGE` | empty   | Optional target storage, for example `zfs`   |
| `CI_CLONE_IP_PREFIX` | `203.0.113` | CI control network prefix for dynamic clones |
| `CI_CLONE_IP_START` | `10`    | First host octet for dynamic clone control IPs |
| `CI_CLONE_IP_END` | `99`      | Last host octet for dynamic clone control IPs |
| `CI_CLONE_GATEWAY` | `203.0.113.254` | Gateway for dynamic clone control IPs |

## Deployment

After editing scripts in this directory, deploy them to the runner host:

```sh
scp ludus-server/ci/{base,claim-cluster,release-cluster,release-vm,prepare,prepare-cluster,run,cleanup,check-install-status}.sh \
    your-ludus-host:/opt/ludus/ci/
scp ludus-server/ci/{bootstrap-ci-host,configure-runner-custom-executor}.sh \
    your-ludus-host:/opt/ludus/ci/
scp ludus-server/ci/{ci-setup,ci-vm-setup}.yml your-ludus-host:/opt/ludus/ci/
ssh your-ludus-host 'chmod +x /opt/ludus/ci/*.sh'
```

`.gitlab-ci.yml` is read by GitLab from the repo on each pipeline run; no manual
deployment is needed for YAML changes.

## Debugging

```sh
glab ci status
ssh your-ludus-host 'ls -la /opt/ludus/ci/vm-assignments/'
ssh your-ludus-host 'qm list | grep -E "ci-[0-9]+-"'
ssh your-ludus-host 'qm terminal <vmid>'
```

To remove a successful clone manually:

```sh
ssh your-ludus-host 'CUSTOM_ENV_CI_PIPELINE_ID=<pipeline-id> CUSTOM_ENV_LUDUS_CI_SERIES=<series> /opt/ludus/ci/release-vm.sh'
```
