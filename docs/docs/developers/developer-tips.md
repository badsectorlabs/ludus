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
  -t  Target development hostname (default: lkdev2)
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
  ./dev.sh -t ludus-dev-hostname -C -d -s # Build and install client remotely, Build and install Ludus server with debug mode, skip plugins
```
This script copies your current code to the target machine via rsync at `~/ludus-dev` then calls the `dev.sh` scripts in `ludus-server` or `ludus-client` respectively with appropriate options.

## DEBUG logging

Ludus logs at the INFO level by default, but you can get DEBUG logging from different components by setting environment variables.

The `dev.sh` script can set these automatically with flags `-d`, `-D`, `-P` and `-L`, but you can set them manually as well.

```shell-session
set-environment LUDUS_DEBUG=1
systemctl restart ludus ludus-admin
```

The following environment variables can be used to enable DEBUG level logging for components:

- `LUDUS_DEBUG` can be set to `1` to enable debug logging from the backend
- `LUDUS_PROXMOX_DEBUG` can be set to `1` to enable debug logging of requests from Ludus to Proxmox
- `LUDUS_DATABASE_DEBUG` can be set to `1` to enable debug logging of every SQLite query
- `LUDUS_DEBUG_LICENSE` can be set to `1` to enable debug logging of every license request

To unset the variables, use

```shell-session
unset-environment LUDUS_DEBUG
systemctl restart ludus ludus-admin
```

## Ansible variables

These variables are set/unset the same way as Ludus DEBUG variables

- `LUDUS_SECRET_*` variables are injected into the environment for Ansible
- `LUDUS_ANSIBLE_BINARY` can be used to overwrite the default `ansible-playbook` binary (if you are using your own in a venv)