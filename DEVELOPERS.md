# Ludus Development Workflow

This workflow uses a shared Ludus test VM on a Proxmox host. A VM is checked out for one Git branch, receives the current source through `dev.sh`, and is restored before returning to the shared pool.

## Prerequisites

- Key-based normal-user SSH access to the Proxmox host for `ProxyJump`.
- Key-based root SSH access to the Ludus test VMs.
- A privilege-separated Proxmox API token scoped to the test VM resource pool.
- A local Ludus client.
- Local `bash`, `curl`, `git`, `jq`, `nc`, `rsync`, and `ssh` commands.
- An available test VM cloned from `ci-seed-integration`, or an administrator-prepared installer test VM with a named baseline snapshot.
- On the build target: Linux amd64, Go 1.25 or newer (matching `go.work` and the modules), a C compiler and libc development headers for CGO, `git`, `rsync`, and CA certificates. On Debian/Proxmox, install `build-essential git rsync ca-certificates` and a suitable Go toolchain. Go must be on root's login-shell `PATH`; the distribution's default Go package may be too old. Dependency downloads require network access or a populated Go module/toolchain cache.
- Optional UI builds (`-w`) also require Bun on the build target. Optional plugins need their own build prerequisites; build-only mode skips plugin installation scripts.

## One-time Proxmox administrator setup

Normal developers do not need root access to the Proxmox host. A Proxmox administrator must perform the one-time setup:

1. Create a resource pool and add only the Ludus test VMs.
2. Create a role with these privileges:
   - `Pool.Audit`
   - `VM.Audit`
   - `VM.Config.Options`
   - `VM.PowerMgmt`
   - `VM.Snapshot`
   - `VM.Snapshot.Rollback`
3. Create or register each developer's Proxmox user.
4. Assign the role to both the user and a privilege-separated API token at the test pool path.
5. Give the developer the token value and the Proxmox cluster CA certificate through a secure channel.

Example administrator commands:

```bash
pveum pool add LUDUS --comment "Ludus development test VMs"
pveum pool modify LUDUS --vms 1008,1009,1010,1011

pveum role add LudusTestingUser \
  --privs "Pool.Audit VM.Audit VM.Config.Options VM.PowerMgmt VM.Snapshot VM.Snapshot.Rollback"

pveum user add developer@pam --comment "Ludus testing pool developer"
pveum acl modify /pool/LUDUS \
  --users developer@pam \
  --roles LudusTestingUser \
  --propagate 1

pveum user token add developer@pam ludus-testing \
  --privsep 1 \
  --expire <unix-expiration-time>

pveum acl modify /pool/LUDUS \
  --tokens 'developer@pam!ludus-testing' \
  --roles LudusTestingUser \
  --propagate 1
```

The PAM user must also exist as a normal Unix account when it is used for SSH. It needs no sudo privileges. SSH forwarding must be allowed so it can act as a jump host.

The API token does not need QEMU Guest Agent privileges. Release updates run over the developer's root SSH access to the checked-out test VM.

## Developer API configuration

Store the token in `~/.config/ludus/testing-pve.json` with mode `0600`:

```json
{
  "version": 1,
  "ssh_host": "developer@your-proxmox-host",
  "api_url": "https://proxmox.example:8006",
  "token_id": "developer@pam!ludus-testing",
  "token_secret": "<token-secret>",
  "ca_file": "/home/developer/.config/ludus/pve-root-ca.pem"
}
```

The API URL must be reachable from the development workstation. `ssh_host` is the normal Proxmox-host SSH account used for `ProxyJump`; it needs no sudo privileges. When `ca_file` is omitted, `testing.sh` validates the server certificate with the operating system trust store. Never store the token secret in the repository or a worktree.

The individual API values can instead be supplied through `LUDUS_TESTING_PVE_API_URL`, `LUDUS_TESTING_PVE_TOKEN_ID`, `LUDUS_TESTING_PVE_TOKEN_SECRET`, and `LUDUS_TESTING_PVE_CA_FILE`. `LUDUS_TESTING_PROXMOX_HOST` overrides the configured `ssh_host`, and `-H <proxmox-host>` overrides both. Certificate validation is enabled by default. For an isolated development host with an intentionally untrusted certificate, set `LUDUS_TESTING_PVE_INSECURE=true`; do not use that override when a trusted certificate or CA file is available.

## Complete flow

### 1. List available VMs

```bash
./testing.sh list
```

Only VMs tagged `available` are listed. Choose a VMID from the output.

### 2. Check out a VM

```bash
./testing.sh checkout 1008
```

Checkout:

- verifies that the VM is a running Ludus test instance;
- atomically replaces `available` with `in-use` and a Proxmox-safe form of the current branch name;
- writes the VMID, IP address, branch, node, Proxmox host, and selected local ports to the gitignored `.ludus-testing-vm.json` file.

Only one VM can be checked out in a worktree at a time. `dev.sh` automatically uses the checked-out VM unless `-t <target>` is supplied explicitly.

Checkout starts with local ports `8080` for Ludus, `8006` for Proxmox, and `2222` for SSH. If a default is already in use, checkout increments it until it finds an available port. This allows separate worktrees to run tunnels concurrently.

Set exact ports when needed:

```bash
./testing.sh checkout 1008 \
  --ludus-port 18080 \
  --proxmox-port 18006 \
  --ssh-port 12222
```

An explicitly selected port must be valid, distinct from the other two ports, and currently available.

Inspect the current worktree's checkout and tunnel state at any time:

```bash
./testing.sh status
```

The status command reads local state, so it does not require `-H` or `LUDUS_TESTING_PROXMOX_HOST`.

### 3. Start local tunnels

```bash
./testing.sh tunnel start
```

The background SSH tunnel uses the Proxmox host as a jump host and forwards the local ports selected during checkout:

- the selected Ludus port to the VM's `127.0.0.1:8080` endpoint;
- the selected Proxmox port to the VM's `127.0.0.1:8006` endpoint;
- the selected SSH port to the VM's `127.0.0.1:22` endpoint.

Run `./testing.sh status` to see the exact mappings for the current worktree. Tunnel state is stored in the gitignored `.ludus-testing-tunnel.json` file. Starting a second tunnel fails if one is already active in that worktree.

When Ludus runs in an LXC container, point only the Ludus forward at the container's IP as reachable from the nested Proxmox test VM:

```bash
./testing.sh tunnel start --ludus-host 192.0.2.253
./testing.sh status
```

The SSH connection still terminates on the checked-out test VM through the outer Proxmox jump host. Proxmox and SSH forwards remain on that VM's loopback address; `--ludus-host` changes only the destination of port 8080. Use the actual container address, not the outer Proxmox host's address. Stop an existing tunnel before starting one with a different destination. The destination is recorded in tunnel state and displayed by `status`; state written before this option existed still means `127.0.0.1`.

### 4. Sync and build Ludus

```bash
./dev.sh -d -s
```

`dev.sh` rsyncs the current worktree to `~/ludus-dev` on the checked-out VM and builds through the VM's root login environment. Run `./dev.sh -h` for all build, logging, plugin, and client options.

The ordinary flow installs into the SSH target: for an LXC installation, target the container's root SSH login with `-t` (and `-p` if needed), using an SSH config entry with the required jump host. Do not run the ordinary update flow against a legacy host or bare Proxmox when testing the installer; use `-B` as described below.

After the build, `dev.sh` checks the remote `~/.ludus-api-key` file. If it does not exist, the script uses `/opt/ludus/install/root-api-key` to create the following admin development user:

- user ID: `DEV`;
- name: `Dev`;
- email: `dev@localhost.local`;
- password: `password`.

The returned API key is saved remotely in `~/.ludus-api-key`. The file is reused by later builds so the user is not recreated.

### 5. Use the local Ludus client

An executed shell script cannot modify its parent shell environment. `dev.sh` therefore writes a mode `0600`, gitignored `.ludus-dev-env` file. Load it into the current shell:

```bash
source ./.ludus-dev-env
```

It exports:

```bash
LUDUS_URL=https://127.0.0.1:<selected-ludus-port>
LUDUS_API_KEY=<development-user-api-key>
```

Local Ludus commands now operate on the checked-out VM through the tunnel:

```bash
ludus user list
ludus range status
```

Do not commit or share `.ludus-dev-env`; it contains an administrator API key.

### 6. Stop the tunnel

```bash
./testing.sh tunnel stop
```

Stop the tunnel before releasing the VM. The command shuts down the SSH control process and removes its local state. It also cleans stale tunnel state if the SSH process has already exited.

### 7. Release the VM

```bash
./testing.sh release 1008
```

Release:

1. verifies that the VM is checked out by this worktree;
2. rolls back the newest `ludus-v*` snapshot;
3. starts the VM if the rollback leaves it stopped;
4. updates Ludus to the latest public release over root SSH to the test VM;
5. creates a new release snapshot when the public release differs from the restored snapshot;
6. replaces all VM tags with `available` and removes `.ludus-testing-vm.json`.

The rollback discards development changes on the VM. If rollback, update, or snapshot creation fails, the VM remains tagged `in-use` and the checkout state is retained. Inspect the failure and rerun `release` when it is safe.

## Snapshot-based installer and migration tests

Use an explicit baseline for bare Proxmox fresh-install tests and legacy host migration tests. A Proxmox administrator must prepare and verify the baseline in advance, including root SSH access, build prerequisites, the test-instance description/IP, and storage suitable for snapshots. The nested test VM must be running and tagged `available` before checkout. For example, use `installer-clean` for bare Proxmox and `legacy-host` for a populated legacy Ludus host. These are operator-chosen names, not automatically created snapshots.

```bash
# Select the VM and existing baseline prepared by the administrator.
./testing.sh checkout 1008 --baseline installer-clean
./testing.sh status

# Sync the branch and build the candidate on the nested host, without updating it.
./dev.sh -B -s -v installer-candidate
```

`--baseline SNAPSHOT` (also `--baseline=SNAPSHOT`) validates that the named snapshot exists and is not Proxmox's synthetic `current` entry, then stores `baseline_snapshot` in `.ludus-testing-vm.json`. Checkout reserves the VM; it does not roll back or create the snapshot. Start from an administrator-restored baseline for the first test.

Build-only mode produces the actual Linux server binary at `~/ludus-dev/ludus-server/ludus-server` on the SSH target. It builds the dynamic-inventory binary first and embeds it in the server. It does not install Ludus, run `--update`, set/unset systemd environments, create a development API user, or write `.ludus-dev-env`. Existing API keys and environment files are left unchanged. Optional plugin installation scripts are skipped even if their source is present. `-B` cannot be combined with `-S`, `-c`, `-C`, or `-l`; `-v VERSION` and optional UI building with `-w` remain available.

For a build target outside the checkout flow, or a source tree already on a Linux build host:

```bash
# From the workstation; the target can be a root SSH config alias.
./dev.sh -B -t root@build-host -v installer-candidate

# Directly on the Linux build host, from the repository root.
./ludus-server/dev.sh -B -v installer-candidate
```

Both build scripts exit nonzero on server build failures, and the top-level script stops on failed source sync or remote build rather than reporting successful installation. Neither command packages the binary as an LXC template.

Build installation media on the checked-out nested host as root. In addition to the Go build prerequisites, appliance packaging needs Proxmox Debian Appliance Builder (`dab`), `make`, `curl`, `unzip`, Python 3, `ansible-galaxy`, `git`, `tar`, `sha256sum`, and the usual coreutils. Its pinned dependency downloads and Debian package installation need network access. Prepare these dependencies before taking the baseline snapshot.

```bash
# Run these commands in a root SSH session on the checked-out nested host.
CANDIDATE=installer-candidate
mkdir -p ~/ludus-dev/binaries
cp ~/ludus-dev/ludus-server/ludus-server ~/ludus-dev/binaries/ludus-server
cd ~/ludus-dev/ludus-server/lxc
LUDUS_VERSION="$CANDIDATE" ./build.sh
```

For a fresh installation, run the branch's installer on that same host. Replace the example bridge, management IP/gateway, VMID, storage, and DNS server with values reserved for your test:

```bash
bash ~/ludus-dev/install.sh --no-prompt \
  --template-file ~/ludus-dev/ludus-installer-candidate-debian13-amd64.tar.zst \
  --skip-verification \
  --bridge vmbr0 --ip 10.0.0.50/24 --gw 10.0.0.1 \
  --vmid 200 --storage local-lvm --nameserver 10.0.0.1
```

For same-host migration, begin with the `legacy-host` baseline instead, build the same candidate, then invoke:

```bash
bash ~/ludus-dev/install.sh --no-prompt --migrate-host \
  --template-file ~/ludus-dev/ludus-installer-candidate-debian13-amd64.tar.zst \
  --skip-verification
```

Omitting `--bridge` and `--ip` on the migration path lets the installer provide private management networking and endpoint forwarding. `--skip-verification` is only for this locally built development archive; production installations should use signed installation media. Use the checked-out branch's `install.sh`, not a separately downloaded installer.

After installing or migrating, start the tunnel using the actual container IP. Fresh installations normally use `192.0.2.253`; same-host migration preserves the legacy service address `192.0.2.254`:

```bash
# Fresh installation:
./testing.sh tunnel start --ludus-host 192.0.2.253
# For migration, use --ludus-host 192.0.2.254 instead.
./testing.sh status
```

Use the installed instance's credentials for API checks. Build-only mode deliberately does not provision or refresh the development credentials. Then stop the tunnel and release:

```bash
./testing.sh tunnel stop
./testing.sh release 1008
```

With an explicit baseline, release rechecks that snapshot, rolls back to it, reasserts the checkout tags restored by rollback, starts the VM if necessary, and waits for root SSH. It then marks the VM `available` and removes checkout state. It does **not** query the latest public Ludus release, run a public update, or create a release snapshot, so releasing bare Proxmox does not require Ludus to be installed. Failure retains checkout state and reasserts `in-use` ownership. A missing baseline is an error, never a fallback to the public-release flow.

For a migration test, use `./testing.sh checkout 1008 --baseline legacy-host` and `./dev.sh -B` instead. Keep the legacy installation untouched until invoking the installer migration command. Release discards the installed container and other test changes by restoring the entire nested-host snapshot. Stop the worktree's tunnel before release; even stale tunnel state must be cleaned with `tunnel stop`.

Without `--baseline`, checkout and release retain the public-release workflow documented above.

## Typical session

```bash
./testing.sh list
./testing.sh checkout 1008
./testing.sh tunnel start

./dev.sh -d -s
source ./.ludus-dev-env
ludus user list

./testing.sh tunnel stop
./testing.sh release 1008
```
