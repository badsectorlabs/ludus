# Ludus Development Workflow

This workflow uses a shared Ludus test VM on a Proxmox host. A VM is checked out for one Git branch, receives the current source through `dev.sh`, and is restored before returning to the shared pool.

## Prerequisites

- Key-based normal-user SSH access to the Proxmox host for `ProxyJump`.
- Key-based root SSH access to the Ludus test VMs.
- A privilege-separated Proxmox API token scoped to the test VM resource pool.
- A local Ludus client.
- Local `bash`, `curl`, `git`, `jq`, `nc`, `rsync`, and `ssh` commands.
- An available test VM cloned from `ci-seed-integration`.

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

### 4. Sync and build Ludus

```bash
./dev.sh -d -s
```

`dev.sh` rsyncs the current worktree to `~/ludus-dev` on the checked-out VM and builds through the VM's root login environment. Run `./dev.sh -h` for all build, logging, plugin, and client options.

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
