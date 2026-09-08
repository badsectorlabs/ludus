---
title: "🪄 Developer Tips and Tricks"
---

# 🪄 Developer Tips and Tricks

## dev.sh

When modifying Ludus itself, it's helpfully to quickly build and test your changes on a development machine.

To do this, you can use the `./dev.sh` script in the root of the Ludus repository.

```shell-session
./dev.sh -h
Usage: ./dev.sh [-h] [-l] [-a] [-t target] [-n lines] [-c] [-d] [-p] [-w] [-s] [-D] [-C]
  -h  Show this help message
  -l  Show Ludus service logs (default 100 lines)
  -a  Show Ludus admin service logs (requires -l)
  -n  Number of log lines to show (default 100)
  -t  Target development hostname (required unless a test VM is checked out)
  -p  Port to use for SSH/rsync
  -c  Build and install client locally
  -C  Build and install client remotely
  -w  Build and install web UI
  -s  Skip plugins
  -S  Skip building the server, just sync the code
  -d  Enable debug mode for Ludus server
  -D  Enable debug mode for database
  -P  Enable debug mode for Proxmox
  -L  Enable debug mode for license requests

Examples:
  # Build and install client remotely; Build and install Ludus server with debug mode, skip plugins
  ./dev.sh -t ludus-dev-hostname -C -d -s 
```
This script copies your current code to the target machine via rsync at `~/ludus-dev` then calls the `dev.sh` scripts in `ludus-server` or `ludus-client` respectively with appropriate options.

The script **always** sets the `LUDUS_ENABLE_SUPERADMIN` variable to enable the [PocketBase web interface](../administration/pocketbase.md).

## testing.sh

Developers use a normal Proxmox-host SSH account for `ProxyJump` and a privilege-separated Proxmox API token for VM inventory, tags, power, and snapshots. The SSH account needs no sudo access. Store the SSH host, API URL, token ID, token secret, and optional CA path in the mode `0600` file `~/.config/ludus/testing-pve.json`; see the root `DEVELOPERS.md` guide for the required role and administrator setup.

If `ca_file` is omitted, the Proxmox API certificate is checked against the operating system trust store. Validation is enabled by default. `LUDUS_TESTING_PVE_INSECURE=true` disables it for isolated hosts with intentionally untrusted certificates.

`LUDUS_TESTING_PROXMOX_HOST` overrides the configured `ssh_host`, and `-H <user@proxmox-host>` overrides both.

```shell-session
# List test VMs currently tagged "available"
./testing.sh list

# Reserve a VM for the current Git branch
./testing.sh checkout 1008

# Sync and build the current source on the reserved VM
./dev.sh

# Restore the VM and return it to the pool
./testing.sh release 1008
```

Checkout replaces the `available` tag with `in-use` and a Proxmox-safe version of the current branch name. It writes the VMID, IP address, and Proxmox host to the gitignored `.ludus-testing-vm.json` file. When that file exists, `dev.sh` uses the stored Proxmox host as an SSH jump host. Supplying `dev.sh -t <target>` explicitly bypasses the checked-out VM.

Checkout uses local ports `8080` for Ludus, `8006` for Proxmox, and `2222` for SSH. It increments any default port that is already in use. Override them with `--ludus-port`, `--proxmox-port`, and `--ssh-port`; explicitly selected ports must be available and distinct.

Use `./testing.sh status` to show the checked-out VM and whether the local development tunnel is running. Status reads the worktree's local state and does not require a Proxmox host option.

While a VM is checked out, create local forwards for the Ludus web interface, Proxmox web interface, and SSH:

```shell-session
./testing.sh tunnel start
./testing.sh status
./testing.sh tunnel stop
```

The tunnel uses the local ports selected during checkout and runs in the background. It is tracked in a gitignored local state file. Stop it before releasing the VM.

After `dev.sh` builds the server, it reuses the remote `~/.ludus-api-key` or creates an admin development user and saves its key there. Because an executed script cannot modify its parent shell environment, `dev.sh` writes the tunneled client settings to the gitignored `.ludus-dev-env` file:

```shell-session
source ./.ludus-dev-env
ludus user list
```

This sets `LUDUS_URL` to `https://127.0.0.1:<selected-ludus-port>` and `LUDUS_API_KEY` to the development user's key. Start the tunnel before using the local client.

Release rolls the VM back through the Proxmox API, starts it if necessary, and updates it to the latest public Ludus release over root SSH to the VM. If that release does not match the restored snapshot, release creates a new snapshot before replacing all VM tags with `available`. If any restore, update, or snapshot step fails, the VM remains checked out so the failure can be inspected and the release retried.

## DEBUG logging

Ludus logs at the INFO level by default, but you can get DEBUG logging from different components by setting environment variables.

The `dev.sh` script can set these automatically with flags `-d`, `-D`, `-P` and `-L`, but you can set them manually as well.

```shell-session
# terminal-command-ludus-root
systemctl set-environment LUDUS_DEBUG=1
# terminal-command-ludus-root
systemctl restart ludus ludus-admin
```

The following environment variables can be used to enable DEBUG level logging for components:

- `LUDUS_DEBUG` can be set to `1` to enable debug logging from the backend
- `LUDUS_PROXMOX_DEBUG` can be set to `1` to enable debug logging of requests from Ludus to Proxmox
- `LUDUS_DATABASE_DEBUG` can be set to `1` to enable debug logging of every SQLite query
- `LUDUS_DEBUG_LICENSE` can be set to `1` to enable debug logging of every license request

To unset the variables, use

```shell-session
# terminal-command-ludus-root
systemctl unset-environment LUDUS_DEBUG
# terminal-command-ludus-root
systemctl restart ludus ludus-admin
```

## Ansible variables

These variables are set/unset the same way as Ludus DEBUG variables

- `LUDUS_SECRET_*` variables are injected into the environment for Ansible
- `LUDUS_ANSIBLE_BINARY` can be used to overwrite the default `ansible-playbook` binary (if you are using your own in a venv)