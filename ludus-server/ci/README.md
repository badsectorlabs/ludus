# Ludus CI

GitLab CI runs on a single self-hosted shell-custom GitLab Runner installed
on the `ci-proxmox` Proxmox host. Tests execute on a fixed set of
pre-snapshotted Proxmox VMs that are rolled back to a known `clean` state
on demand.

The pipeline is defined in `/.gitlab-ci.yml` at the repo root. The custom
executor scripts in this directory live both in the repo (here) and on the
runner host at `/opt/ludus/ci/`. The runner uses the deployed copies in
`/opt/ludus/ci/`; changes made here must be `scp`'d to the host (see
**Deployment** below).

## Runner host


The custom executor is wired up to:

```
prepare_exec = "/opt/ludus/ci/prepare.sh"
run_exec     = "/opt/ludus/ci/run.sh"
cleanup_exec = "/opt/ludus/ci/cleanup.sh"
```

## VM topology

Each numbered VMID is a long-lived VM, hand-prepared at a specific install
stage and snapshotted as `clean`. The CI rolls VMs back to `clean` and runs
test scripts against them. There is no automation here that creates the
`clean` snapshots — that's a manual setup step.

| VMIDs       | Role                                           |
|-------------|------------------------------------------------|
| 1000–1004   | **Pool A** test VMs                            |
| 1005, 1006  | Cluster nodes (1005 is what tests target)      |
| 1007–1011   | **Pool B** test VMs (same layout as Pool A)    |
| 1012        | Build VM (Go/web UI build, docs)               |

### Snapshot offset within a pool

Each pool is a chain of progressively-installed VMs. `LUDUS_SNAPSHOT_NAME`
in a job picks which VM in the pool is used:

| `LUDUS_SNAPSHOT_NAME` | Pool A VMID | Pool B VMID | What "clean" means on that VM   |
|-----------------------|-------------|-------------|---------------------------------|
| (none / full build)   | 1000        | 1007        | base OS                         |
| `clean_install`       | 1001        | 1008        | Ludus installed                 |
| `templates_built`     | 1002        | 1009        | + templates built               |
| `range_built_admin`   | 1003        | 1010        | + admin range deployed          |
| `range_built_user`    | 1004        | 1011        | + user range deployed           |

Mapping is implemented in `base.sh:get_vm_offset()` and `get_vmid_for_pool()`.

### Cluster

`cluster install kickoff` re-installs Ludus across both cluster nodes, then
the chain (`cluster-install-check` → `cluster-verify-sdn` → ... →
`cluster-test-range-default`) runs against VMID 1005. Cluster jobs do their
own snapshot management via `LUDUS_BUILD_TYPE: clean-cluster` /
`cluster-from-snapshot` / `take-cluster-snapshot` (see
`prepare-cluster.sh`).

### Build VM (1012)

`pages`, `documentation`, `build all`, `manual testing`, `beta-release`,
`gitlab-upload`, `keygen-upload`, and `release` all run on VMID 1012 with
`LUDUS_BUILD_TYPE: any-built`. The build VM is **not** rolled back; it is
designed to take concurrent builds (its `resource_group: ludus-build-vm`
serializes the publishing jobs only).

## How parallelism works

There are two non-cluster pools (A and B). One pipeline uses one pool; a
second pipeline gets the other. A third pipeline waits for one to free up.

### Pool selection

The `claim-pool` job (stage `claim`) atomically reserves a pool by
`mkdir`'ing a lock directory under
`/opt/ludus/ci/pool-assignments/pool-<A|B>.lock`. It tries `A`, then `B`;
if both are held, it sleeps and retries. **Hard fail after 1 hour.**

`claim-pool` writes `POOL=A` (or `B`) to a `pool.env` dotenv artifact.
GitLab forwards this as a job variable to every test job that lists
`claim-pool` in its `needs:`. The custom executor sees it as
`CUSTOM_ENV_POOL`; `prepare.sh` and `run.sh` re-export it as `POOL` and
`base.sh:resolve_vm()` uses it to compute the VMID.

### Cluster selection

`claim-cluster` does the same atomic-mkdir dance against
`pool-assignments/cluster.lock`. There is only one slot; pipelines queue.
Hard fail after 1 hour.

### Release

`release-pool` and `release-cluster` (stage `release`, `when: always`) run
after every terminal pool/cluster job and remove the lock directory. They
are idempotent and only release if they own the lock.

### Stale lock recovery

When a user **cancels** a pipeline mid-flight, GitLab does not run the
`when: always` release jobs. Without recovery the lock would sit until the
stale threshold expires.

`claim-pool.sh` and `claim-cluster.sh` therefore call
`is_pipeline_terminal` (in `base.sh`) before falling back to the time
check. That helper queries the GitLab API
(`projects/<id>/pipelines/<owner-pipeline-id>` via `glab`) and returns
true if the owning pipeline is `success`, `failed`, `canceled`, or
`skipped`. The lock is broken immediately on the next claim attempt.

If `glab` or `jq` is unavailable, or the API call fails, the time-based
fallback (locks > 6 hours old) takes over.

To recover manually from a wedged pipeline:

```sh
ssh ci-proxmox
ls /opt/ludus/ci/pool-assignments/
# Inspect lock owners
cat /opt/ludus/ci/pool-assignments/pool-A.lock/owner
cat /opt/ludus/ci/pool-assignments/cluster.lock/owner
# Force-release
sudo rm -rf /opt/ludus/ci/pool-assignments/pool-A.lock
sudo rm -rf /opt/ludus/ci/pool-assignments/cluster.lock
```

## Pipeline shape

```
pages → documentation → build → claim → test → release → upload → release-final
```

| Stage          | Jobs                                                             |
|----------------|------------------------------------------------------------------|
| `pages`        | `pages` (docs site)                                              |
| `documentation`| `documentation` (embedded docs)                                  |
| `build`        | `build all` (Go binaries on VMID 1012)                           |
| `claim`        | `claim-pool`, `claim-cluster`                                    |
| `test`         | All install/template/range/post-deploy/integration/cluster jobs  |
| `release`      | `release-pool`, `release-cluster` (`when: always`)               |
| `upload`       | `gitlab-upload`, `keygen-upload`                                 |
| `release-final`| `beta-release`, `release` (publishing on tags)                   |

## Files in this directory

| File                  | Purpose                                                                  |
|-----------------------|--------------------------------------------------------------------------|
| `base.sh`             | Common env (Proxmox auth, VM topology), `resolve_vm()` helper            |
| `claim-pool.sh`       | Atomic pool-A/B claim with 1h timeout                                    |
| `claim-cluster.sh`    | Atomic cluster claim with 1h timeout                                     |
| `release-pool.sh`     | Release the pool lock if owned by this pipeline                          |
| `release-cluster.sh`  | Release the cluster lock if owned by this pipeline                       |
| `prepare.sh`          | Custom-executor prepare phase: VM resolve + snapshot rollback            |
| `prepare-cluster.sh`  | Cluster-specific prep (rollback / take-snapshot / SSH wait)              |
| `run.sh`              | Custom-executor run phase: SSHs the job script onto the resolved VM     |
| `cleanup.sh`          | Custom-executor cleanup phase (no-op; release is now job-driven)         |
| `check-install-status.sh` | Polled by `run.sh` during install check                              |
| `setup.sh`            | One-time runner host bootstrap (Ansible roles + playbook)                |
| `ci-setup.yml`        | Ansible: install runner + deps on a fresh Proxmox host                   |
| `ci-vm-setup.yml`     | Ansible: build the CI VM templates (the 1000-series VMs)                 |
| `prepare.yml`         | Ansible playbook used during VM template setup                           |
| `cleanup.yml`         | Ansible playbook used during VM template setup                           |
| `roles/`              | Custom Ansible roles used by `ci-setup.yml` / `ci-vm-setup.yml`          |
| `configs/`            | Ludus range configuration files used as fixtures by tests                |

## Environment variables (the contract)

Set per-job in `.gitlab-ci.yml`:

| Variable               | Values                                                              |
|------------------------|---------------------------------------------------------------------|
| `LUDUS_BUILD_TYPE`     | `full`, `from-snapshot`, `any-built`, `clean-cluster`, `cluster`, `cluster-from-snapshot`, `pool-claim`, `pool-release`, `cluster-claim`, `cluster-release` |
| `LUDUS_SNAPSHOT_NAME`  | `clean_install` / `templates_built` / `range_built_admin` / `range_built_user` / `cluster_range_built` |
| `LUDUS_INSTALL_STEP`   | `kickoff` / `check` / `take-snapshot` / `take-cluster-snapshot`     |

Set by `claim-pool` (passed via dotenv to every job that `needs:` it):

| Variable | Values |
|----------|--------|
| `POOL`   | `A` or `B` |

## Deployment

After editing scripts in this directory, push them to the runner host:

```sh
cd ludus
scp ludus-server/ci/{base,claim-pool,claim-cluster,release-pool,release-cluster,prepare,prepare-cluster,run,cleanup,check-install-status}.sh \
    ci-proxmox:/opt/ludus/ci/
ssh ci-proxmox 'chmod +x /opt/ludus/ci/*.sh'
```

The `.gitlab-ci.yml` is read by GitLab from the repo on each pipeline run;
no deployment needed for YAML changes.

## Debugging

```sh
# Pipeline state
glab ci status
glab ci view

# Pool/cluster lock state on the runner
ssh ci-proxmox 'ls -la /opt/ludus/ci/pool-assignments/'

# Active jobs / VM state
ssh ci-proxmox 'sudo qm list | grep -E "^\s*1[0-9]{3}"'

# Tail a job script in the runner builds dir (last running job)
ssh ci-proxmox 'sudo ls -lt /home/gitlab-runner/builds/ | head'
```

## Commit message magic

The pipeline reacts to bracketed tokens in commit messages. Common ones:

| Token                       | What runs                                                        |
|-----------------------------|------------------------------------------------------------------|
| `[full build]`              | Full pipeline (install → templates → ranges → post-deploy → cluster) |
| `[template tests]`          | Just the template build/check jobs                               |
| `[range tests]`             | Just the range deploy/check jobs                                 |
| `[post-deploy tests]`       | All post-deploy-* jobs                                           |
| `[client tests]`            | Client basic commands                                            |
| `[cluster tests]`           | Cluster chain                                                    |
| `[integration test]`        | `test-everything`                                                |
| `[start-at templates]` etc. | Begin the chain at a later snapshot                              |
| `[manual]`                  | Triggers the manual-testing job (build VM)                       |
| `[skip ci]`                 | Suppresses everything                                            |
| `[skip build]`              | Suppresses the docs/pages build                                  |
