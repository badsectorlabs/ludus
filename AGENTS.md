# Agent Notes

This repo is being worked on for Ludus LXC/containerization. The branch used for
the latest validation was `ludus-lxc-containerization` at commit `cb40044`.

## Lab Access

- Primary remote access: `ssh dev`.
- The outer `dev` Proxmox test cluster VMs are:
  - `300` = `nfs`
  - `301` = `node01`
  - `302` = `node02`
  - `303` = `node03`
- Nested Proxmox access from `dev`:
  ```sh
  ssh -i /root/.ssh/proxmox-lab-ed25519 \
    -o UserKnownHostsFile=/dev/null \
    -o StrictHostKeyChecking=no \
    root@198.18.1.11
  ```
- Nested node management IPs:
  - `node01`: `198.18.1.11/24`
  - `node02`: `198.18.1.12/24`
  - `node03`: `198.18.1.13/24`
  - gateway: `198.18.1.1`

## Latest Known Good Validation

Completed on `2026-06-10` UTC:

- Outer `dev` resources `300-303` were reverted to the `clean` snapshot, started,
  clock-synced, and verified healthy.
- Old LX26C resources were removed.
- Old Ludus LXC `900` was removed and a fresh nested `900 ludus` container was
  installed on `node01`.
- Ludus API was reachable at `https://198.18.1.50:8080`.
- Initial admin was created:
  - user ID: `IA`
  - name: `Initial Admin`
  - email: `initial.admin@example.com`
  - password path on nested `node01`: `/root/ludus-reinstall/initial-admin-password`
  - API key path on nested `node01`: `/root/ludus-reinstall/ia-api-key`
- Do not print the password or API key in chat or logs.
- Validation client on nested `node01`: `/usr/local/bin/ludus-validate`.

The default `IA` range deployed successfully:

| VMID | Name | IP | Node |
| --- | --- | --- | --- |
| `102` | `IA-router-debian11-x64` | `10.1.10.254` | `node02` |
| `105` | `IA-ad-dc-win2022-server-x64` | `10.1.10.11` | `node02` |
| `106` | `IA-ad-win11-22h2-enterprise-x64-1` | `10.1.10.21` | `node02` |
| `107` | `IA-kali` | `10.1.99.1` | `node02` |

Templates restored in the nested cluster:

- `100` `debian-11-x64-server-template`
- `101` `kali-x64-desktop-template`
- `103` `win11-22h2-x64-enterprise-template`
- `104` `win2022-server-x64-template`

## Useful Validation Commands

Poll range state without exposing secrets:

```sh
ssh dev <<'OUTER'
ssh -n -i /root/.ssh/proxmox-lab-ed25519 \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  root@198.18.1.11 '
set -euo pipefail
export LUDUS_API_KEY=$(cat /root/ludus-reinstall/ia-api-key)
ludus-validate range list
ludus-validate range errors || true
ludus-validate range inventory
'
OUTER
```

Check nested cluster health:

```sh
ssh dev <<'OUTER'
ssh -n -i /root/.ssh/proxmox-lab-ed25519 \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  root@198.18.1.11 '
set -euo pipefail
pvecm status | sed -n "1,24p"
pvesm status
for node in node01 node02 node03; do
  ssh -o BatchMode=yes -o StrictHostKeyChecking=no "$node" \
    "printf \"$node \"; date -u +%Y-%m-%dT%H:%M:%SZ"
done
'
OUTER
```

Check key nested resources:

```sh
ssh dev <<'OUTER'
ssh -n -i /root/.ssh/proxmox-lab-ed25519 \
  -o UserKnownHostsFile=/dev/null \
  -o StrictHostKeyChecking=no \
  root@198.18.1.11 '
pvesh get /cluster/resources --type vm --output-format json |
python3 -c '"'"'import json,sys
data=json.load(sys.stdin)
names={"ludus","debian-11-x64-server-template","kali-x64-desktop-template",
       "win11-22h2-x64-enterprise-template","win2022-server-x64-template"}
for x in sorted(data, key=lambda d: int(d.get("vmid", 0))):
    name=str(x.get("name", ""))
    if name.startswith("IA-") or name in names:
        print("{} {} node={} status={} template={}".format(
            x.get("vmid"), name, x.get("node"), x.get("status"),
            x.get("template", 0)))
'"'"'
'
OUTER
```

## Known Fixes From Validation

The live nested LXC was patched during validation, and matching local repo changes
exist in these areas:

- Router VLAN and firewall router lookups were changed to use cluster-wide Proxmox
  resource queries instead of node-local VM lists.
- Dynamic inventory parsing in VM deployment was guarded so empty/non-JSON output
  does not fail with `from_json`.
- IP/hostname configuration refreshes the host from dynamic inventory before
  Windows/Linux includes.
- `proxmox.py` falls back to the configured static IP when the guest agent does
  not return a usable address. This mattered for Windows VMs that sometimes
  showed `null` in Proxmox while WinRM was reachable.
- Windows reboot timeouts were increased to handle slow nested Windows hosts.
- DC event-log clearing is best-effort with a longer async startup timeout; it
  previously failed the range despite being non-critical.

If asked to validate the installer archive itself, rebuild the LXC archive first;
the archive used during the successful run predated some live playbook patches.

## Operational Cautions

- The worktree may contain unrelated dirty changes. Do not revert changes unless
  the user explicitly asks.
- Do not print contents of `/root/ludus-reinstall/initial-admin-password` or
  `/root/ludus-reinstall/ia-api-key`.
- Windows and Office installation tasks in the nested lab can be slow. Confirm the
  ansible worker and WinRM connection before assuming a hang.
- Chocolatey cache probes to `192.0.2.2:8081` timed out during validation but were
  ignored by the playbook, and deployment still succeeded.

## Local Verification

These passed after the latest changes:

```sh
git diff --check
go test ./ludus-api/... ./ludus-client/... ./ludus-server/...
```
