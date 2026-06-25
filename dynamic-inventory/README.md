# dynamic-inventory

An [Ansible dynamic inventory](https://docs.ansible.com/ansible/latest/inventory_guide/intro_dynamic_inventory.html) "script" (binary) that returns the VMs of a Proxmox cluster as an Ansible inventory, with Ludus-specific filtering and host-variable resolution. It is a Go rewrite of the original `proxmox.py` script that previously lived in `ludus-server/ansible/range-management/`.

The compiled binary is dropped at `ludus-server/ansible/range-management/dynamic-inventory` and is passed to `ansible-playbook -i ...` / `ansible-inventory -i ...` by the Ludus API.

## Building

```sh
cd dynamic-inventory
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" \
  -o ../ludus-server/ansible/range-management/dynamic-inventory
```

This is what `.gitlab-ci.yml` does during the server build.

## Ansible contract

Ansible calls dynamic inventory scripts in two modes:

| Flag             | What Ansible expects                                                            | Implementation        |
|------------------|----------------------------------------------------------------------------------|-----------------------|
| `--list`         | JSON with `all`, `_meta.hostvars`, and one key per group.                       | `mainList` in `main.go` |
| `--host <name>`  | JSON object of hostvars for a single host (`{}` if unknown).                    | `mainHost` in `main.go` |

`--list` performs:

1. Pool lookup — in unfiltered mode, `GET /pools` then `GET /pools/{id}` for each pool; in range-filtered mode, only the requested pool plus `ADMIN` for admins. This populates visible pool-named groups and the set of VMIDs that may be returned.
2. `GET /cluster/resources?type=vm` — a single typed call for every qemu/lxc/openvz on the cluster.
3. For each VM that survives the range filter, in parallel (worker pool of 20):
   - `GET /nodes/{node}/{type}/{vmid}/config` — for the `description`/notes JSON and (for LXC) the `net0` line.
   - For running qemu VMs: `agent network-get-interfaces` and `agent os-info` via the QEMU guest agent.

`--host` applies the same range filter as `--list`, then reuses `buildHostVars` against the cluster-resources list so a single-host lookup avoids per-node inventory walks.

## Configuration

Proxmox credentials are resolved from CLI flags first, then environment variables.

| CLI flag                  | Env var                | Notes                                          |
|---------------------------|------------------------|------------------------------------------------|
| `--url`                   | `PROXMOX_URL`          | e.g. `https://10.0.0.1:8006`                   |
| `--username`              | `PROXMOX_USERNAME`     |                                                |
| `--password`              | `PROXMOX_PASSWORD`     | Used if no token/secret provided.              |
| `--token`                 | `PROXMOX_TOKEN`        | API token ID (`user@pve!tokenname`).           |
| `--secret`                | `PROXMOX_SECRET`       | API token secret.                              |
| `--trust-invalid-certs`   | `PROXMOX_INVALID_CERT` | Boolean value (`true`, `1`, or `yes`) disables TLS verification. |
| `--pretty`                |                        | Indent the JSON output.                        |

If a token+secret pair is present it is preferred; otherwise the script logs in with username+password via `client.CreateSession`.

## Ludus integration

These environment variables are set by `ludus-api` before invoking ansible-playbook and are read by the inventory script:

| Variable                  | Meaning                                                                                  |
|---------------------------|------------------------------------------------------------------------------------------|
| `LUDUS_RANGE_ID`          | The pool ID for the calling user's range. Used both for filtering and `{{ range_id }}` template substitution. |
| `LUDUS_RANGE_NUMBER`      | Second octet for the range's 10.X.0.0/16 network. Used to compute the per-VM "config IP". |
| `LUDUS_USER_IS_ADMIN`     | When true, the `ADMIN` pool is also considered "in range" for filtering.                  |
| `LUDUS_RETURN_ALL_RANGES` | When true, the range filter is disabled and every VM on the cluster is returned.          |
| `LUDUS_RANGE_CONFIG`      | Path to the user's `range-config.yml`. Used to resolve per-VM IP and OS hints.            |

### Range filtering

When `LUDUS_RANGE_ID` is set and `LUDUS_RETURN_ALL_RANGES` is false, the inventory only returns VMs that belong to that pool (plus the `ADMIN` pool for admins). VMs outside the range are skipped before the per-VM config/agent calls are made — this both prevents leakage and keeps the call count bounded.

The requested `LUDUS_RANGE_ID` is always emitted as a group, even when the pool is missing or empty, so playbooks can target it without conditional logic.

### IP resolution (`checkIPAddresses`)

For running qemu VMs, the guest agent typically reports several IPs. The script picks `ansible_host` in this priority:

1. The "config IP" computed from `LUDUS_RANGE_NUMBER` + the VM's `vlan` + `ip_last_octet` in the range config — if the guest agent actually reports it.
2. Any address inside `192.0.2.0/24` (Ludus's WireGuard client range).
3. The first non-loopback / non-link-local / non-`172.16.0.0/12` address (skipping the Docker default network).
4. If the VM's range-config entry has `force_ip: true` and nothing usable was reported, fall back to the config IP anyway.

For LXC containers, `ansible_host` is parsed out of the `ip=` field in the container's `net0` line, since LXCs don't run a guest agent.

### OS detection

`proxmox_os_id` comes from the QEMU guest agent's `os-info` call. `mswindows` is normalized to `windows`. If the agent isn't reachable, the script falls back to the range-config flags (`windows` / `linux` / `macOS`) for that VM. As a last resort for macOS, if the VM name contains `macos` it's tagged as such — macOS guests don't expose a usable agent os-id.

### Notes-based groups and hostvars

The Proxmox VM `description` field is parsed as JSON and merged into the VM's hostvars. A `groups` key whose value is an array adds the host to each named group. Single-quoted JSON is tolerated (matches the old Python script's fallback).

## Groups emitted

For each VM that survives filtering and is not a template:

- One group per visible pool the VM is in. In range-filtered mode, only the requested range pool is emitted, plus the `ADMIN` pool for admins.
- `running` — qemu/lxc VMs whose `proxmox_status == "running"`.
- One group per resolved `proxmox_os_id` (e.g. `windows`, `linux`, `macos`).
- One group per entry in the VM's notes `groups` array.

Templates are excluded from `all` and from every group.

## Testing

```sh
go test ./...
```

`inventory_test.go` uses `gock` to mock the Proxmox HTTP API and exercises `--list` / `--host` end-to-end. `helpers_test.go` covers `parseMetadata`, `checkIPAddresses`, and the Ludus-config lookup.
