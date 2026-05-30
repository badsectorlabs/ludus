# Ludus LXC Containerization & Air-Gap Support — Design Spec

**Date:** 2026-05-30
**Status:** Approved for implementation planning
**Branch base:** `main` @ `8bfa2bd`

---

## 1. Problem

Ludus currently installs and runs directly on the Proxmox host. This:

- Risks breakage when Proxmox upgrades (shared Debian base, package conflicts).
- Is explicitly discouraged by Proxmox.
- Couples Ludus to host-local resources: `pvesh`/`pveum`/`pvesm` binaries, `/etc/pve/` certs, `/etc/network/interfaces`, host systemd.
- Hardcodes `127.0.0.1:8006` as the Proxmox API endpoint in Go defaults and ~13 ansible tasks.
- Has no air-gapped install path.

## 2. Goal

Move the Ludus runtime into an **unprivileged Debian 13 LXC** that runs on the Proxmox host/cluster and talks to Proxmox **exclusively via the HTTP API** using a root API token. The host is left untouched except for: the SDN zone/VNet (created via API), the LXC itself, and the cached template tarball.

The LXC template must be **air-gap capable**: once `config.yml` is pushed, the container reaches a working state with zero outbound internet.

## 3. Non-Goals

- Automated migration of existing on-host installs (manual DB import is supported; see §10).
- Multi-cluster management (endpoints array is HA failover within one cluster only).
- Changing the range-deployment ansible model (users keep extending ranges with roles as today).
- arm64 LXC template (amd64 only for first release).

---

## 4. Runtime Architecture

```
┌──────────────────────────── Proxmox cluster ────────────────────────────┐
│  pve-01:8006   pve-02:8006   pve-03:8006        ← proxmox_endpoints[]   │
│       │                                                                 │
│       │ HTTPS + root@pam!ludus token (cluster-wide via pmxcfs)          │
│       ▼                                                                 │
│  ┌──────────────── LXC: ludus (unpriv, Debian 13) ──────────────────┐   │
│  │ eth0 → vmbr0      <user IP>      API :8080/:8081, WG :51820       │   │
│  │ eth1 → ludusnat   192.0.2.253    ansible→routers, dnsmasq DHCP    │   │
│  │                                                                   │   │
│  │ /opt/ludus/ludus-server   (Go, embeds ansible+packer)             │   │
│  │ /opt/ludus/db             (PocketBase — importable)               │   │
│  │ /opt/ludus/tls            (self-signed, generated on first boot)  │   │
│  │ /etc/wireguard/wg0.conf   (keys generated on first boot)          │   │
│  │ systemd: ludus, ludus-admin, wg-quick@wg0, dnsmasq                │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  SDN zone "ludus" (Simple if 1 node, VXLAN if >1):                      │
│    VNet ludusnat → subnet 192.0.2.0/24, gateway .254 (host), SNAT on    │
│    VNet r1, r2…  → per-range, VLAN-aware, created at runtime via API    │
└─────────────────────────────────────────────────────────────────────────┘
```

**Network roles on `192.0.2.0/24`:**

| IP | Owner | Purpose |
|---|---|---|
| `.254` | Proxmox host (SDN subnet gateway) | Default gateway + SNAT for range routers' WAN side |
| `.253` | Ludus LXC (`eth1`) | dnsmasq DHCP for packer builds; ansible/SSH source to routers; WG-traffic next-hop into ranges |
| `.50–.100` | DHCP pool | Packer template builds |
| `.{100+N}` | Range `N` router WAN | Per-range router uplink |

LXC maintains internal routes `10.{N}.0.0/16 → 192.0.2.{100+N}` so WireGuard clients reach range subnets.

---

## 5. Configuration Schema (`ludus-api/config.go`)

### 5.1 New / changed fields

| Field | Type | Default | Notes |
|---|---|---|---|
| `proxmox_endpoints` | `[]string` | `[]` (required) | Ordered list of `https://host:8006`. Failover in order. Startup rejects `127.0.0.1`/`localhost`. |
| `proxmox_token_id` | `string` | — | e.g. `root@pam!ludus` |
| `proxmox_token_secret` | `string` | — | Plaintext in `config.yml` (mode 0600). Env override `LUDUS_PROXMOX_TOKEN_SECRET` via Viper `AutomaticEnv()`. |
| `proxmox_user_realm` | `string` | `pve` | Realm for Ludus-created Proxmox users. `@pve` is fully API-manageable. |
| `wireguard_endpoint` | `string` | LXC eth0 IP | Host/IP WireGuard clients dial. Replaces `proxmox_public_ip`. |
| `ludus_nat_interface` | `string` | `ludusnat` | SDN VNet name (was `vmbr1000`). |
| `ludus_nat_ip` | `string` | `192.0.2.253` | LXC's IP on the NAT VNet. |
| `ludus_nat_gateway` | `string` | `192.0.2.254` | Host's SDN-gateway IP on the NAT VNet. |
| `tls_cert_file` | `string` | `/opt/ludus/tls/server.crt` | Generated if absent. |
| `tls_key_file` | `string` | `/opt/ludus/tls/server.key` | Generated if absent. |

### 5.2 Removed fields

`proxmox_url` (shimmed — see below), `proxmox_public_ip`, `proxmox_interface`, `proxmox_local_ip`, `proxmox_gateway`, `proxmox_netmask`, `cluster_mode`.

### 5.3 Backward-compat shim

If `proxmox_url` is set and `proxmox_endpoints` is empty, load `[proxmox_url]` and log a deprecation warning. If `proxmox_public_ip` is set and `wireguard_endpoint` is empty, copy it over with a warning. Lets a copied-in legacy `config.yml` boot.

---

## 6. `pveclient` Package (`ludus-api/pveclient/`)

Single owner of all Proxmox HTTP interaction. Wraps the existing `badsectorlabs/go-proxmox` fork and adds missing operations + endpoint failover.

### 6.1 Public surface

```go
package pveclient

type Config struct {
    Endpoints   []string
    TokenID     string
    TokenSecret string
    InsecureTLS bool
    Timeout     time.Duration // per-request, default 30s
    Logger      *slog.Logger
}

type Client struct { /* unexported */ }

func New(cfg Config) (*Client, error)
func (c *Client) Raw() *proxmox.Client          // pass-through for already-typed ops
func (c *Client) ActiveEndpoint() string

// Additions (direct REST where go-proxmox lacks coverage):
func (c *Client) Version(ctx) (Version, error)                          // replaces pveversion
func (c *Client) StorageStatus(ctx, node string) ([]Storage, error)     // replaces pvesm status
func (c *Client) NodeStatus(ctx, node string) (NodeStatus, error)       // replaces pveperf (partial)
func (c *Client) CreateUser(ctx, userid, password string, groups []string) error
func (c *Client) CreateToken(ctx, userid, name string, privsep bool) (Token, error)
func (c *Client) DeleteToken(ctx, userid, name string) error
func (c *Client) ClusterNodeCount(ctx) (int, error)                     // replaces ls /etc/pve/nodes
func (c *Client) NextVMID(ctx) (int, error)
func (c *Client) EnsureRole(ctx, name string, privs []string) error
func (c *Client) EnsureGroup(ctx, name string) error
func (c *Client) EnsurePool(ctx, name string) error
func (c *Client) EnsureACL(ctx, path, role string, groups, users []string) error
func (c *Client) EnsureSDNZone(ctx, name, kind string, peers []string) error
func (c *Client) EnsureVNet(ctx, zone, name string, tag int, vlanaware bool) error
func (c *Client) EnsureSubnet(ctx, vnet, cidr, gw string, snat bool) error
func (c *Client) ApplySDN(ctx) error
func (c *Client) ReloadNodeNetwork(ctx, node string) error
```

**Not provided:** `SetUserPassword` / `ResetUserPassword`. `PUT /access/password` is blocked for API tokens. Initial passwords are set via `CreateUser` (`POST /access/users` accepts `password` for `@pve` realm and works with tokens). Any password-change/reset flow emits:

> `⚠ Cannot change Proxmox password via API token. Run on any cluster node: pveum passwd <user>@pve`

When `proxmox_user_realm: pam`, `CreateUser` omits the `password` field and returns a warning instructing the admin to create the system user + `pveum passwd` manually.

### 6.2 Failover

- `New()` probes each endpoint with `GET /version` (2s timeout); first responder is active.
- All requests route through `c.do()`. On `net.OpError`, `context.DeadlineExceeded`, or HTTP 502/503/504: mark endpoint unhealthy, advance, retry once. 4xx and other 5xx do **not** failover.
- Background goroutine re-probes unhealthy endpoints every 60s.
- Bootstrap preflight additionally calls `GET /version` against **every** endpoint and fails fast if any rejects the token (catches typos / un-joined nodes).

### 6.3 Token cluster-wide validity

`root@pam!ludus` is stored in `/etc/pve/priv/token.cfg` (pmxcfs, corosync-replicated). It validates on every cluster node regardless of which node created it, and survives the creating node going down. Token validation is read-only against pmxcfs, so it works even when a minority partition is read-only; only token create/delete requires quorum.

### 6.4 Unit tests

- `httptest.Server` fixtures for 2–3 endpoints; assert failover on conn-refused and 503; assert no failover on 400/403.
- `Ensure*` idempotency: second call against same mock makes no `POST`.
- Table-driven JSON unmarshal for `Version`, `StorageStatus`, `NodeStatus`.
- Health-restore: dead endpoint comes back, re-enters rotation.
- `CreateUser`: assert `password` in body for `@pve`, omitted + warning for `@pam`.
- `RoundTripper` injection — no real Proxmox required. Target ≥80% coverage on package.

---

## 7. Go Bootstrap (`ludus-server/bootstrap.go`)

Replaces `ansible/proxmox-install/{stage-1,2,3,existing-proxmox}.yml`. Runs when `/opt/ludus/install/.bootstrap-complete` is absent. Every step idempotent; safe to re-run after crash.

```
bootstrap():
  1. Load config.yml → build pveclient.Client
  2. Preflight
       Version() ≥ 8.0
       GET /access/permissions → token has Sys.Audit, SDN.Allocate, Pool.Allocate,
                                  User.Modify, Realm.AllocateUser
       GET /version on every endpoint (fail fast on any auth reject)
       ClusterNodeCount() → zoneType = simple|vxlan
  3. Proxmox objects (Ensure*, idempotent)
       Roles:  LudusPacker, LudusUser, LudusAdmin (privs match current stage-3)
       Groups: ludus_users, ludus_admins
       Pools:  SHARED, ADMIN
       ACLs:   /pool/SHARED, /pool/ADMIN, /sdn/zones/ludus → groups
       SDN:    zone "ludus" (zoneType) + VNet "ludusnat" (vlanaware)
               + subnet 192.0.2.0/24 gw .254 snat=1
               (skip-if-exists; installer normally pre-creates these)
       ApplySDN()
  4. Local LXC state
       /opt/ludus/tls/server.{crt,key}  (ecdsa P-256, 10y, SAN = eth0 IP + hostname)
       /etc/wireguard/server-{private,public}-key + wg0.conf  (if absent)
       /etc/dnsmasq.d/ludus.conf  (bind 192.0.2.253, dhcp .50-.100, opt:router .254)
       systemctl enable --now wg-quick@wg0 dnsmasq
       mkdir /opt/ludus/{users,ci,previous-versions}; chown -R ludus:ludus /opt/ludus
  5. Route table
       For each range in DB: append 10.{N}.0.0/16 via 192.0.2.{100+N}
       to /etc/network/if-up.d/ludus-routes; ip route add now
  6. reconcileImportedState()  (§10) if DB non-empty
  7. ROOT user + admin API key  (existing runBootstrapOnly() logic)
  8. touch /opt/ludus/install/.bootstrap-complete
```

Logs to `/opt/ludus/install/install.log`.

### 7.1 Unit tests

- `PVEClient` interface at call-site; mock records calls.
- Golden: config X → exact `Ensure*` call sequence.
- TLS: generated cert parses, SAN contains eth0 IP, key matches.
- WG/dnsmasq conf: golden-file compare.
- Idempotency: run twice, second pass = zero mutating calls.
- Filesystem via `t.TempDir()`.

---

## 8. Installer (`install.sh`)

Single bash script, ~250 lines, runs on any Proxmox node. Client-install branch (downloads `ludus-client`) kept unchanged; server-install branch replaced.

**Dependencies on host:** `pveversion`, `curl`, `python3` — all ship with PVE. **No `jq`** (not in stock PVE). JSON parsed via:

```bash
_json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }
```

### 8.1 Flow

```
0. Preflight: PVE ≥ 8.0, curl + python3 present, detect node name + vmbr0 CIDR.

1. Root token
     EUID==0 && no --token-* flags:
       pveum user token add root@pam ludus --privsep 0 -o json → capture secret
       (if exists: prompt reuse-existing-secret | delete+recreate)
     else: prompt / read --token-id --token-secret
     Validate: curl -sk -H "Authorization: PVEAPIToken=…" https://localhost:8006/api2/json/version

2. Gather config (interactive unless --no-prompt; flags override)
     proxmox_endpoints  (default: every node IP from GET /cluster/status → :8006)
     LXC VMID           (default: GET /cluster/nextid)
     LXC storage        (default: first storage with content=rootdir)
     eth0: dhcp | static IP/CIDR + gw
     wireguard_endpoint (default: eth0 static IP, else prompt)
     vm_storage_pool, iso_storage_pool, license_key

3. SDN bootstrap (curl + token, GET-before-POST idempotent)
     POST /cluster/sdn/zones      {zone:ludus, type:simple|vxlan, peers:[…]}
     POST /cluster/sdn/vnets      {vnet:ludusnat, zone:ludus, vlanaware:1}
     POST /cluster/sdn/vnets/ludusnat/subnets {subnet:192.0.2.0/24, gateway:.254, snat:1}
     PUT  /cluster/sdn
     Poll GET /cluster/sdn → state==ok (30s)

4. Fetch template
     --template-file <p>  → cp to /var/lib/vz/template/cache/  (air-gap path)
     else: curl R2 https://<bucket>/ludus-lxc/<ver>/ludus-<ver>-debian13-amd64.tar.zst
           verify sha256 against …/<ver>/checksums.txt

5. Create container
     pct create $VMID local:vztmpl/ludus-<ver>-debian13-amd64.tar.zst \
       --hostname ludus --unprivileged 1 --features nesting=1,keyctl=1 \
       --cores 4 --memory 4096 --swap 512 --rootfs $STORAGE:20 \
       --net0 name=eth0,bridge=vmbr0,ip=$ETH0_CFG,firewall=0 \
       --net1 name=eth1,bridge=ludusnat,ip=192.0.2.253/24 \
       --onboot 1 --startup order=99
     Append to /etc/pve/lxc/$VMID.conf:
       lxc.cgroup2.devices.allow: c 10:200 rwm
       lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file

6. Configure
     Render config.yml (heredoc)
     pct start $VMID
     pct push $VMID config.yml /opt/ludus/config.yml --perms 0600
     --import-db <tar>: pct push + extract to /opt/ludus/db and /etc/wireguard
     pct exec $VMID -- systemctl restart ludus
     Poll: pct exec $VMID -- test -f /opt/ludus/install/.bootstrap-complete  (5m)

7. Output: LXC IP, API URL, WG endpoint; on failure tail install.log via pct exec.
```

### 8.2 Flags

`--version`, `--template-file`, `--token-id`, `--token-secret`, `--no-prompt`, `--vmid`, `--storage`, `--ip`, `--gw`, `--endpoints`, `--import-db`, `--wg-endpoint`, `--license`.

---

## 9. LXC Image (`ludus-server/lxc/`)

Built with **DAB** (Debian Appliance Builder) on Debian 13 base. Output: `ludus-<version>-debian13-amd64.tar.zst`.

### 9.1 Layout

```
ludus-server/lxc/
├── dab.conf            # Suite: trixie, Architecture: amd64, Name: ludus
├── Makefile            # dab init/bootstrap/install/finalize
├── build.sh            # CI entrypoint; injects $LUDUS_VERSION
└── files/
    ├── ludus.service
    ├── ludus-admin.service
    ├── dnsmasq-ludus.conf      # placeholder; bootstrap rewrites
    └── 99-ludus-sysctl.conf    # net.ipv4.ip_forward=1
```

### 9.2 Baked-in contents (air-gap)

**apt packages:** `python3 python3-pip python3-venv ansible-core wireguard-tools dnsmasq nftables iproute2 openssh-client sshpass curl jq ca-certificates git gpg dbus systemd-resolved`

**Installed by `build.sh` post-bootstrap:**
- `/opt/ludus/ludus-server` — binary from `build all` artifact
- `/opt/ludus/ansible/`, `/opt/ludus/packer/` — copied from source tree
- `/opt/ludus/venv/` — `proxmoxer requests netaddr pywinrm dnspython jmespath` (pinned versions)
- `/opt/ludus/collections/` — `ansible-galaxy collection install -r requirements.yml -p …` (vendored)
- `/usr/local/bin/packer` + `/opt/ludus/resources/packer/plugins/{proxmox,ansible}` (pinned)
- `ludus` system user (uid 1001); `/opt/ludus` chowned
- Units enabled: `ludus.service`, `ludus-admin.service`; `wg-quick@wg0` + `dnsmasq` enabled (no-op until bootstrap writes configs)

**Air-gap guarantee:** container reaches `.bootstrap-complete` with zero outbound internet given only `config.yml`. Verified by CI job `test-lxc-airgap`.

---

## 10. DB Import & Reconciliation

Triggered when `/opt/ludus/db/` is non-empty at first boot.

```
reconcileImportedState():
  for each Range r in DB:
      EnsureVNet("ludus", "r"+r.Number, vxlan_tag_base+r.Number, vlanaware=true)
      append route 10.{r.Number}.0.0/16 via 192.0.2.{100+r.Number}
  for each User u in DB:
      if GET /access/users/{u.ProxmoxUsername} → 200: skip
      else: WARN "user %s in DB but missing in Proxmox; recreate with `ludus user add --import`"
  ApplySDN()
  if /etc/wireguard/server-private-key absent:
      WARN "WG server key missing; existing client configs will break.
            Copy old /etc/wireguard/ or run `ludus user wg regen --all`"
```

**Documented procedure:** stop old Ludus → `tar czf ludus-backup.tar.gz /opt/ludus/db /opt/ludus/config.yml /etc/wireguard` → on new host `install.sh --import-db ludus-backup.tar.gz`.

---

## 11. Range-Management Ansible Parameterization

Range playbooks stay; only Proxmox-touching parts change.

| File | Change |
|---|---|
| `range-management/proxmox.py` | No edit. `ansible.go` injects `PROXMOX_URL` = `pveclient.ActiveEndpoint()`, `PROXMOX_TOKEN_ID/SECRET`. |
| `tasks/firewall/set-firewall-rules.yml` | `https://127.0.0.1:8006` → `{{ proxmox_url }}`; `PVEAPIToken={{ proxmox_token_id }}={{ proxmox_token_secret }}` |
| `tasks/router/add-vlan-to-router.yml` | Same substitution |
| `tasks/proxmox/configure-ip-and-hostname-linux.yml` | Same substitution |
| `tasks/proxmox/snapshot-management.yml` | Same substitution |
| `tasks/proxmox/set-vm-state.yml` | Same substitution |
| `user-management/vmbr-management.yml` | **Deleted** (SDN-only) |
| All `community.proxmox.*` module tasks | `api_host: "{{ proxmox_api_host }}"`, `api_token_id/secret` from extra-vars |

**Extra-vars injection (`ludus-api/ansible.go`):** `proxmox_url`, `proxmox_api_host`, `proxmox_token_id`, `proxmox_token_secret`, `ludus_nat_ip`, `ludus_nat_gateway`. Secret passed via `--extra-vars @/tmp/ludus-vars-<rand>.json` (0600, deleted post-run) — never on argv.

**Firewall deny-list update:** rules blocking range→host traffic now block every IP in `proxmox_endpoints` + `ludus_nat_ip` + `ludus_nat_gateway`.

---

## 12. CI (`.gitlab-ci.yml`)

| Job | Stage | Needs | Does |
|---|---|---|---|
| `build-lxc-template` | `build` | `build all` | Installs `dab`, runs `ludus-server/lxc/build.sh`. Artifact: `*.tar.zst` + `sha256`. Triggers: tags, `[build-lxc]` commit msg, changes to `ludus-server/lxc/**`. |
| `test-lxc-airgap` | `test` | `build-lxc-template` | `pct create` on isolated bridge (no gateway). Assert `.bootstrap-complete`, `ludus.service` active, nftables egress drop counter == 0. |
| `test-install-sh` | `test` | `build-lxc-template` | `install.sh --no-prompt --template-file <artifact>` on fresh PVE CI node. Assert API responds on :8080. |
| `upload-lxc-r2` | `upload` | `build-lxc-template` | `rclone copy *.tar.zst r2:ludus-lxc/$CI_COMMIT_TAG/`; update `latest.txt`. All tags. |
| `release` (amend) | `release-binaries` | — | Add asset link → R2 URL for the LXC template. |

Existing range-deploy test matrix unchanged; now exercises the LXC-hosted server end-to-end. `go test ./...` already runs in `build all`.

---

## 13. Testing Summary

| Layer | Target | Method |
|---|---|---|
| Unit (Go) | `pveclient/` ≥80% | `httptest`, `RoundTripper` mock, table-driven |
| Unit (Go) | `bootstrap.go` | `PVEClient` interface mock, `t.TempDir()`, golden files |
| Unit (Go) | config shim/validation | Viper in-memory |
| Unit (Go) | `reconcileImportedState` | Seeded PocketBase + mock client |
| Integration | Air-gap bootstrap | `test-lxc-airgap` CI job |
| Integration | Installer | `test-install-sh` CI job |
| E2E | Range deploy | Existing CI matrix |

---

## 14. Deletion List

| Path | Reason |
|---|---|
| `ludus-server/ansible/proxmox-install/stage-{1,2,3}.yml` | Go bootstrap + DAB |
| `ludus-server/ansible/proxmox-install/existing-proxmox.yml` | Go bootstrap |
| `ludus-server/ansible/proxmox-install/main.yml` + supporting `tasks/` | Go bootstrap |
| `ludus-server/ansible/user-management/vmbr-management.yml` | SDN-only |
| `ludus-api/network.go`: `manageVmbrInterfaceStandalone`, `runNetworkCommand` | SDN-only |
| `ludus-server/checks.go`: `checkDebian12or13`, `checkForVirtualizationSupport` | N/A in LXC |
| `ludus-server/main.go` `/etc/pve/…/*.pem` paths | `tls_cert_file`/`tls_key_file` |
| `ludus-server/install.go` ansible-install invocation | `bootstrap()` |
| `install.sh` legacy server-install branch | §8 flow |
| Config fields: `proxmox_interface`, `proxmox_local_ip`, `proxmox_gateway`, `proxmox_netmask`, `cluster_mode`, `proxmox_url` (shimmed), `proxmox_public_ip` (shimmed) | No longer meaningful |

**Kept:** all `range-management/`, remaining `user-management/`, `packer/`, `proxmox.py`, Telmate snapshot client (replace with go-proxmox in a follow-up once verified).

---

## 15. Host-Coupling Replacement Map

| Before | After |
|---|---|
| `pveum passwd` | Removed (create-only via `POST /access/users`; reset → warning) |
| `pveum user token add/del` | `pveclient.CreateToken/DeleteToken` |
| `pvesm status` | `pveclient.StorageStatus` |
| `pveperf` | `pveclient.NodeStatus` (drops fsync bench; noted in API response) |
| `pveversion` | `pveclient.Version` |
| `ls /etc/pve/nodes` | `pveclient.ClusterNodeCount` |
| `pvesh get /nodes/{n}/network` | `pveclient.Raw().Node(n).Network()` |
| `/etc/pve/nodes/{n}/pveproxy-ssl.*` | self-signed `/opt/ludus/tls/` |
| `/etc/network/interfaces` edits | SDN VNets via API |
| `curl 127.0.0.1:8006` (ansible) | `{{ proxmox_url }}` extra-var |

---

## 16. Open Follow-ups (out of scope for this spec)

- Replace Telmate snapshot client with go-proxmox once snapshot coverage verified in fork.
- arm64 LXC template.
- Optional `systemd-creds` storage for `proxmox_token_secret` (deferred; plaintext config chosen).
