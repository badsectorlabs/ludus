# Host-to-LXC and plugin upgrade test matrix

Review target: `plugin-updates` after merging `origin/main` (2.3.4), September 25, 2026.
This is a release qualification plan, **not a report of successful migrations**.
Source review and local checks are complete; the VM scenarios below remain unexecuted.

## Release recommendation

Do not qualify upgrades using `/api/health` alone. Require the same users to authenticate
with their existing credentials at the same endpoint and operate the same deployed VMs
through their existing WireGuard profiles. Test ordinary users, not only ROOT.

Three source-confirmed gaps need fixes and regression coverage before qualification:

1. **B1 — P0: supplied Enterprise plugin is not installed in the runtime format.**
   `install.sh` pushes `--enterprise-plugin` to `ludus-enterprise.so` in both plugin
   directories with mode `0644`. `importMigrationState` checks for `*.so`, while
   `ludus-api/license.go` looks for `ludus-enterprise.plugin` and `startManagedPlugin`
   requires an executable RPC process. Supplying a valid RPC binary does not put it
   at the path the loader uses. Online download could mask this; an offline licensed
   migration must exercise the supplied artifact. Cover with PLUGIN-01 and PLUGIN-02.
2. **B2 — P0: final transfer ignores custom `data_directory`.**
   `migrationCollect` snapshots the configured directory, and import normalizes its
   destination to `/opt/ludus/db`. However, `migration_sync_final_state` archives
   `opt/ludus/db` on the old host unconditionally. A missing default directory fails;
   a stale one can replace the correctly staged database with the wrong data. Cover
   both variants, with an acknowledged write after staging, in DATA-02 and FAIL-03.
3. **B3 — P1: customized global Ansible defaults are omitted.**
   `migrationTrees` excludes `ansible`, and the export does not separately capture
   `ansible/server-config.yml`. Router template/name annotation preserves router
   identity, but not the other global defaults used by future deployments. Preserve
   supported configuration explicitly, or reject it with an actionable preflight
   message; do not copy legacy executables/playbooks wholesale. Cover with DATA-05.

These findings follow directly from the code; they have **not** been reproduced by
running an installer on a test VM. No fixes to these migration gaps are included in
the main merge.

Two intentional behavior changes also need explicit acceptance:

- From merged `main`, absent `sso_require_existing_user` now means `true`. Existing
  matching SSO users should still log in, but unknown identities no longer enroll
  automatically. An explicit `false` must survive migration. Do not silently undo
  the new security default in the name of compatibility.
- A non-exposed admin API becomes localhost-only **inside the container**. Existing
  host-local administration using `127.0.0.1:8081` must move to `pct exec` or a supported
  public API command. This is documented, but is not a transparent change for scripts.

## Evidence and current coverage

The merge passed all Go tests, then all Go tests with `-race`, across `ludus-api`,
`ludus-server`, `ludus-client`, and `dynamic-inventory` on macOS arm64. The Linux
amd64 dynamic-inventory binary was built first for the server's embed requirement.
Seven Python installer-status tests passed; three build-isolation tests were skipped
because they require Linux. Shell syntax and touched CI/Ansible YAML parsing passed.
These checks do not exercise Proxmox, systemd, hotplug, WireGuard, or an actual IdP.

Relevant existing coverage:

- `ludus-server/migration_test.go`: live SQLite snapshot, inventory independent of
  the legacy VM cache, router identity, archive rejection, configuration merge,
  TLS selection, WireGuard validation, environment parsing, and private Git credentials.
  It does not exercise a populated installation through the whole shell cutover.
- `bootstrap_test.go`, `reconcile_test.go`, `localgen/*_test.go`, and
  `ludus-api/pveclient/*_test.go`: mocked Proxmox provisioning and local generation.
- `ludus-api/sso_test.go`: existing-account policy through PocketBase's OAuth route
  with a fake provider and fake privileged provisioning service. It is not an SSO migration test.
- `ludus-server/config_test.go`: omitted/explicit SSO and default-range settings,
  including asymmetric combinations and reload. The merge adapts it to LXC validation.
- `ludus-api/license_update_test.go`, `plugins_integration_test.go`,
  `plugin_resources_test.go`, `plugin_pocketbase_client_test.go`, and VM-hook tests:
  RPC/version/authorization/lifecycle contracts, but not real old-host plugin migration.
- `password_rotation_test.go` and `sqlite_concurrency_test.go`: credential recovery
  and concurrent database behavior. Retest them across real service restarts.
- CI `test-lxc-airgap`: isolated bootstrap, user creation, source catalog, and empty
  range creation. It does not prove preservation of a deployed legacy range.
- CI `test-install-sh`: installer plus API health on `clean_install`. Auto-detection
  may choose migration depending on that snapshot; the job does not assert preservation
  of existing SSO identities, client credentials, VM state, or failure recovery.

Primary implementation references: `install.sh`, `ludus-server/migration.go`,
`ludus-server/lxc/migrate-host.sh`, `ludus-server/bootstrap.go`,
`ludus-server/reconcile.go`, `ludus-api/database.go`, `ludus-api/plugins.go`,
`ludus-api/license.go`, and `ludus-api/plugin_resources.go`.
Read [DEVELOPERS.md](../DEVELOPERS.md) and the
[migration contract](docs/infrastructure-operations/migrate-to-lxc.md) before execution.

## Test-host safety and available fixtures

**Never install, build, upgrade, change networking, restart services, or restore
snapshots on `kind-rook` itself.** It is the outer hypervisor, not the migration target.
Use its scoped test-pool API for checkout/release and SSH only as the jump host.
Do not run repository CI helpers on the outer host; several create tokens, change
storage, or configure bridges on whichever machine executes them.

Read-only observations during this review:

- VM **1014** was available and runs Ludus **2.3.2**, Proxmox **9.2.18**, with both
  legacy API services active and no LXC guests. Its CIA range has four running VMs:
  a Debian 13 router, Windows Server DC, Windows 11 client, and Kali. The database
  has one successful range, one never-deployed range, three admin users in `pam`,
  four cached VMs, and **zero external-auth links**. It is not the required mixed-user
  SSO fixture. Provider configuration was not established by this inspection.
- VMs **1015/1016** were also available. Their snapshot descriptions report the same
  2.3.2/default-range baseline; their guest data was not inspected.
- VM **1017** is named `bare-proxmox` but its listed snapshot is `ludus-v2-3-2`.
  Do not assume it is bare or that it has a clean-install baseline.
- VM **1018** has both `proxmox-clean` and `ludus-v2-3-2` snapshots. Checkout does
  not restore a snapshot: the current state must be checked before using either.
- No VM was reserved, built on, upgraded, or restored. Availability is point-in-time;
  rerun `./testing.sh list` before execution. Do not touch VMs reserved by other worktrees.

### Required fixture families

- **F0 — Clean:** bare single-node Proxmox, named clean snapshot, no Ludus marker,
  users, tokens, or NAT/SDN objects. Prepare separate PVE 8 and PVE 9 variants.
- **F1 — Populated 2.3.2:** start from a verified deployed-range snapshot such as
  1014's `ludus-v2-3-2`. Add a non-admin owner and a different non-admin with shared
  access; preserve the existing admin range. This covers a direct pre-2.3.4 upgrade.
- **F2 — Populated 2.3.4 + SSO:** prepare from F1 on a reserved test VM. Use a test-only
  IdP/realm with an already-linked SSO admin, already-linked SSO non-admin, existing
  local account awaiting first SSO link, unknown identity, and mismatched-email identity.
  Include password users, ROOT, direct/group sharing, quotas, and a user with no default
  range. Set both policy flags explicitly in variants. Snapshot only after verifying
  every account and deployed range works before migration.
- **F3 — Licensed:** F2 plus a test license, actual legacy Enterprise plugin, exercised
  Enterprise settings (quotas/range VPN/KMS or other installed features), and matching
  target RPC artifacts. Add a separate Anti-Sandbox/provider-owned VM fixture if that
  installation type is supported. Never consume production license seats for testing.
- **F4 — Customized:** F1/F2 with a dedicated database directory outside `/opt/ludus`,
  custom TLS/CA, custom ports, systemd secret files, global defaults, private sources,
  local roles/collections, custom templates, non-default storage, and multiple ranges.
  Give different users/ranges distinct sentinel values so swaps are detectable.
- **F5 — Existing LXC:** a successful F2/F3 migration with post-cutover writes, plus a
  fresh LXC install. Needed for ordinary updates, plugin replacement, reboot, and retry.
- **FX — Unsupported/broken:** clones or restored baselines varied one fault at a time:
  pre-PocketBase DB, multiple Proxmox nodes, incompatible SDN, address collision,
  unsupported credential reference, corrupt artifact, or missing state. These must
  fail safely, not silently downgrade functionality.

Identity and deployed-range tests must run together on F2/F3, not just as separate
empty-install tests. Existing PVE `@pam` users must remain `@pam`; the `@pve` default
for newly created users must not rewrite old principals or invalidate their tokens.

### Safe execution workflow

1. Select an available VM and confirm the exact baseline exists, storage supports
   snapshots, and its current state matches the intended source fixture. Have the
   baseline restored through the documented test-VM workflow before the first run.
2. Reserve it with `./testing.sh checkout VMID --baseline SNAPSHOT`. An explicit
   baseline is mandatory for this matrix: ordinary release updates to the latest
   public release and would erase the distinction between source-version fixtures.
3. Build using `./dev.sh -B -s -v CANDIDATE`. **Do not use the ordinary `dev.sh -s`
   update flow on the legacy host.** Build-only must not alter services, API keys,
   environment files, or the source database. Package the candidate on the reserved
   nested VM according to DEVELOPERS.md; this is not done by `-B` alone.
4. Run that branch's installer **inside the reserved nested VM**. For the explicit
   path, use `bash ~/ludus-dev/install.sh --no-prompt --migrate-host --template-file
   ~/ludus-dev/ludus-CANDIDATE-debian13-amd64.tar.zst --skip-verification` as one command.
   The bypass is for local development media only. Separate signed-media cases
   must omit it. Also test auto-detection without `--migrate-host`.
5. Observe the old external endpoint from a client outside the nested VM throughout
   cutover. A tunnel directly into the LXC is useful for diagnosis, but cannot prove
   original-endpoint forwarding or unchanged WireGuard profiles.
6. For post-install inspection use `./testing.sh tunnel start --ludus-host 192.0.2.254`
   after migration, or `192.0.2.253` after a fresh installation. Confirm `status`;
   host SSH/Proxmox forwards still target the nested VM. Use existing fixture
   credentials; build-only does not create a DEV account.
7. Save sanitized evidence before cleanup. Stop the tunnel, then use
   `./testing.sh release VMID` to restore the explicit baseline. Retain checkout
   ownership on failure; do not manually mark a failed VM available. Release does
   not replace the test of the migration's own rollback mechanism.

IdP configuration, license services, and failure-injection clients must be disposable
test resources. The existing Keycloak VM on the outer host is not permission to alter
a shared realm. No upgrade or fault injection is part of this review's completed work.

## Common assertions and evidence

Capture a baseline before each attempt and compare immediately after success/rollback,
after restarting the LXC, and after rebooting the **nested Proxmox VM**:

- **Identity manifest:** stable PocketBase record/user IDs, email-to-provider links,
  admin flags, user numbers, default-range IDs, groups, direct grants, quotas, PVE
  principals/token IDs, and ACLs. Count equality alone is insufficient. Keep secret
  comparisons private; never log plaintext keys, passwords, OAuth secrets, or DB dumps.
- **Credential proof:** use pre-upgrade password, API key, OAuth identity, and WireGuard
  profile without regenerating them. Compare TLS/public WireGuard fingerprints;
  exercise encrypted credentials against Proxmox, not just local decryption.
- **Range manifest:** range IDs/numbers, owners/grants, testing state, schedules,
  VMIDs/names/UUIDs, disks and snapshots, MACs, VLANs/trunks, router identity, template
  IDs, power state, and guest uptime. Allow only documented bridge/NAT rewrites.
- **Data manifest:** canonical records/relationships and file hashes/modes for user
  files, templates, sources, blueprints, uploaded files, and supported configuration.
  DB migrations/logs/caches may legitimately change; do not demand byte-identical
  SQLite files or compare the volatile VM cache as the sole VM authority.
- **Network proof:** external API, established and new WG peers, range DNS and DHCP,
  intra-range traffic, allowed shared access, denied cross-range access, and intended
  egress. Check both persistence and live tap attachments, not just Proxmox config.
- **Durability:** every successful mutation acknowledged before service shutdown
  remains present. An ambiguous timeout must not cause duplicate users, ranges,
  or plugin jobs on retry. Track a monotonic operation ID in the test client.
- **Availability:** record staging time, API outage/maximum latency, WG packet-loss
  interval, and recovery time at 1-second resolution. Proposed initial limits are
  30 seconds of API unavailability and 90 seconds of WG interruption, with no manual
  reconnect or guest reboot; confirm these product budgets before calling them an SLA.
  Never hide failures using unbounded retries or an infinite client timeout.
- **Isolation:** no unrelated VM, storage volume, Proxmox user, firewall policy, DNS
  service, or outer-host setting changes. Expect disabled old Ludus services after
  success, restored prior enabled/active states after rollback, and only one active
  authority for API/database/WireGuard writes.

Per-run evidence must include candidate/base/plugin artifact versions and hashes,
fixture snapshot, PVE version, selected flags, redacted commands, assertion results,
timestamps, migration directory, relevant journals, and observed recovery behavior.
Store sensitive artifacts separately with restricted permissions. A failure remains
a failure even when a snapshot restore makes the environment usable again.

## Scenario catalog

**P0** blocks release; **P1** must pass for a claimed supported configuration or have
an explicit release exclusion; **P2** is extended compatibility/scale coverage.
Each semicolon-separated variant below is a distinct execution unless it can safely
share a single migrated fixture. All cases inherit the common assertions above.

### PATH — Installation and upgrade entry points

- **PATH-01 · P0 · F0:** Fresh install on PVE 8 and PVE 9, signed template, default and
  explicit VMID/storage/network settings. Create a real user and deploy a small range.
  Expect a usable LXC, no host Ludus services, one bootstrap, and a working client path.
- **PATH-02 · P0 · F2:** Latest 2.3.4 populated host → candidate using automatic detection
  and no `--migrate-host`. Expect exactly one migration, preserved endpoints, and all
  AUTH/RANGE assertions. This is the primary normal-user upgrade path.
- **PATH-03 · P0 · F1:** Direct 2.3.2 → candidate, plus a separately prepared 2.3.3
  fixture. Do not upgrade the fixture with development tooling first. Expect schema
  migration once, existing login intact, and the new SSO enrollment default disclosed.
- **PATH-04 · P1 · older supported 2.x/FX:** Oldest declared supported PocketBase
  release and a pre-2.0/raw SQLite install. Expect success for the declared boundary,
  and early actionable rejection for raw SQLite requiring the intermediate upgrade.
  Establish the supported 2.x lower bound before release; it is not proven here.
- **PATH-05 · P0 · F2:** Root invocation; non-root sudo; interactive accept; interactive
  decline; unattended no-TTY. Expect correct arguments/version/media forwarded through
  sudo, no hidden prompt in unattended mode, and no server mutation on decline.
- **PATH-06 · P0 · F2/F5:** Repeat after success; retry after failed preflight; retry
  after complete rollback with the retained stopped candidate. Expect no duplicate
  VM/token/forwarder, no overwrite of an occupied VMID, and clear cleanup instructions.
- **PATH-07 · P0 · F5:** Ordinary supported update **inside** the LXC, both migrated and
  fresh variants. Reuse the same users/ranges, then make a new deployment. Expect
  state, custom CA, permissions, and plugin functionality preserved across both services.
- **PATH-08 · P0 · F1/F5:** Run the new server's `--update` on a legacy host, then rerun
  the outer installer on a current LXC installation. Legacy binary update must refuse
  before stopping services. The installer currently reports “already installed” rather
  than updating the LXC; verify and document that distinction and stale-marker handling.
- **PATH-09 · P0 · F3:** Offline migration using locally verified artifacts and a working
  offline test license, with Internet blocked. Expect no release lookup/package download,
  retained IdP access on the test network, and functional licensed features, not just health.
- **PATH-10 · P1 · F4/F0:** Explicit export/import onto another nested host with stopped
  source services. Verify state round trip and refusal to import over a bootstrapped
  appliance. Do not count this as same-host migration: VM disks, PVE users, routing,
  and external endpoints require their own relocation plan.

### AUTH — Existing users, SSO, and authorization

- **AUTH-01 · P0 · F2:** Existing local admin and ordinary users log in with passwords
  and use old API keys; ROOT performs an admin operation through the supported endpoint.
  Expect identical identities/roles, no password reset, and no need to recreate accounts.
- **AUTH-02 · P0 · F2/F3:** Already-linked SSO admin and non-admin log in via the original
  URL and reach their already-deployed ranges. Verify provider IDs, callback URL, DB
  record IDs, groups, quotas, API keys, and WG profiles remain associated with the same user.
- **AUTH-03 · P0 · F2:** Existing local email links to SSO for the first time after migration;
  repeat login and use a second configured provider. Expect the intended account linkage,
  no duplicate provisioning, and no elevation or accidental reassignment of a deployed range.
- **AUTH-04 · P0 · F1/F2:** Unknown SSO email with omitted policy, explicit `true`, and
  explicit `false`. Omitted/true must reject with an actionable message and no new PVE
  user or DB row; false must provision exactly once. Preserve explicit opt-out on import.
- **AUTH-05 · P0 · F2:** Linked IdP returns changed/missing email; email case variant;
  disabled IdP user; revoked session; IdP outage. Enforce the documented matching policy
  without fallback-account creation. Recovery must not require relinking valid users.
- **AUTH-06 · P0 · F2:** Reuse an existing browser session, refresh an existing auth token,
  and complete a login begun during cutover. Valid sessions should survive where supported;
  otherwise require a bounded, explained re-login, never loss of the account or privileges.
- **AUTH-07 · P0 · F2:** Existing `@pam` owners operate VMs and open consoles; new users use
  the configured realm (normally `@pve`). Rotate an old PAM user's password and a new PVE
  user's password. Verify actual PVE authentication/token behavior and documented PAM limits.
- **AUTH-08 · P0 · F2:** `create_default_range: false` with manual, SSO, and initial-admin
  creation; repeat with true. False creates no range/pool/config or bogus ACL but still
  permits login/WG/shared access. Existing deployed/default ranges must not be deleted.
- **AUTH-09 · P0 · F2/F3:** Direct owner, directly shared user, group-shared user, unrelated
  user, and admin each attempt read/write/console/snapshot actions. Allowed grants persist;
  denied requests remain denied through both core and plugin routes. Test revocation too.

### DATA — Durable configuration and files

- **DATA-01 · P0 · F2:** Compare user/range/group/source/blueprint/template IDs, memberships,
  PocketBase auth/provider settings, attachments, schedules, and schema migration ledger.
  Assert semantic preservation and successful attachment reads, not only row totals.
- **DATA-02 · P0 · F4 · B2:** Custom `data_directory` with no default DB, then with a
  deliberately stale default DB. Make a distinctive committed write after staging.
  Expect the configured database's latest state in the LXC; never success with stale data.
- **DATA-03 · P0 · F4:** Explicit DB encryption key, legacy implicit key, PocketBase
  `LUDUS_DB_ENCRYPTION_PASSWORD`, and `LUDUS_SECRET_*` via both services' EnvironmentFiles.
  Test spaces/quotes/newlines/percent signs and precedence. OAuth/PVE secrets must decrypt
  and work after reboot; unsupported references must fail before stopping old services.
- **DATA-04 · P0 · F4:** Custom TLS pair, Proxmox proxy certificate, fallback node cert,
  injected private CA, and expiring/mismatched cert. Supported cases retain original
  certificate identity and trust; mismatched pairs fail preflight. Do not mask checks with `-k`.
- **DATA-05 · P1 · F4 · B3:** Distinct global defaults in `ansible/server-config.yml`, partial
  range defaults, and a custom router template/name. Reapply a deployed range and deploy
  a new one; expect the original effective defaults plus intentional version changes,
  no router replacement, and no silent fallback to appliance defaults.
- **DATA-06 · P1 · F4:** Local/private Git sources, SSH known_hosts/keys, HTTP credentials,
  resource uploads, user/global Ansible roles and collections, and custom template files.
  Sync a source and build from it. Expect files/modes/ownership and usable credentials,
  not merely successful archive extraction. Reject unsafe symlinks/credential references.
- **DATA-07 · P0 · F2/F4:** Preserve root API-key file and non-root service access despite
  different host/LXC numeric UIDs. Verify SQLite WAL/storage writes from both services,
  secret-file permissions, and absence of readable secrets in logs, argv, or public artifacts.
- **DATA-08 · P1 · F2/F4:** Non-default API/admin/WG ports, source-sync toggles, quotas,
  reserved range numbers, unknown plugin settings, and custom DNS. Assert effective runtime
  values after import/reload/update; generated platform fields alone may deliberately change.

### NET — Client endpoint and guest network continuity

- **NET-01 · P0 · F2:** Original hostname/IP, original certificate, old CLI, current CLI,
  and browser from outside the nested host. Test new connections and reused connections
  during cutover. No endpoint edits, unexplained TLS warning, or login reset is acceptable.
- **NET-02 · P0 · F2:** Admin port initially private versus explicitly exposed, including
  non-default ports. Private remains unreachable remotely; exposed remains reachable only
  under the intended access policy. Verify `pct exec` administration after the localhost move.
- **NET-03 · P0 · F2:** Active WG tunnel carrying TCP traffic, idle peer, peer with
  PersistentKeepalive, and a new handshake after cutover. Use unchanged profiles and
  measure recovery. Unrelated conntrack entries must survive the selective WG reset.
- **NET-04 · P0 · F1/F2:** Legacy `vmbr1000` and existing simple-SDN `ludusnat` variants.
  Verify `.254` is unique in the LXC, host gateway is `.49`, existing router routes/DNS
  remain usable, and native NAT tag 1 becomes untagged without disturbing range VLAN tags.
- **NET-05 · P0 · F2:** Multiple live/stopped guests, multiple NICs, distinct MACs, VLANs,
  trunks, firewall flags, and native-tag variations. Compare config and live tap membership,
  then send traffic. No guest reboot, disk change, MAC change, or pending-only NIC edit.
- **NET-06 · P0 · F2:** DNS short/FQDN lookups, AD SRV queries, DHCP renewal, leases,
  range-router DNS rewrites, and existing AdGuard/Blocky router variants. Retain DHCP
  identity and intended resolver behavior before and after range reapply and guest restart.
- **NET-07 · P0 · F2:** Cross-range isolation, direct/group share connectivity, testing-mode
  egress denial, permitted NAT egress, and blocked infrastructure access. Test an explicitly
  allowed and denied destination; successful ping to one gateway is insufficient.
- **NET-08 · P0 · F4:** Automatic private management network and explicit bridge/static
  IP/gateway. Test overlapping routes, occupied `.49`, preexisting `ludusmg`, and exhausted
  automatic /30 candidates. Valid networks work; collisions fail without changing the uplink.
- **NET-09 · P0 · F5:** Restart LXC services, reboot LXC, reboot nested Proxmox, and reload
  nested networking/firewall. Expect one persistent forwarding service, no duplicate NAT
  rules, no legacy listener resurrection, and unchanged external API/WG behavior.

### RANGE — Operations on pre-existing deployments

- **RANGE-01 · P0 · F2:** Two independently owned deployed ranges, one group-shared range,
  and one never-deployed range. Verify VMIDs/disks/snapshots/uptime, configuration and
  owner mappings; include Windows AD and Linux workloads and a custom-named old router.
- **RANGE-02 · P0 · F2:** List/status/credentials/inventory, SSH/RDP/console, power off/on,
  snapshot create/revert, and deployment reapply by the existing ordinary owner. Use a
  disposable workload snapshot, not destructive range recreation, to prove operation parity.
- **RANGE-03 · P0 · F2:** Testing enabled versus disabled, access grants/revocation,
  deny/allow rules, DNS rewrites, auto-shutdown schedules and persisted log history.
  Expect settings and enforcement preserved and no surprise shutdown after migration.
- **RANGE-04 · P0 · F2/F3:** Deploy an additional VM into an existing range, then create
  and deploy a new range after migration. Expect correct PVE token ACLs, SDN membership,
  stored counts, default overrides, and no modification of an unrelated deployed range.
- **RANGE-05 · P1 · F4:** Build and clone existing Debian 12/13 and Windows templates with
  global roles/collections and a partial-defaults config. Include AD join on a routed
  member and custom DNS. These exercise main's merged Packer/defaults/AD/DNS fixes in LXC.
- **RANGE-06 · P1 · F4/FX:** Sparse/reserved range and user numbers, multiple ranges per
  owner, slash-containing supported range IDs, and allocation boundaries. Test the highest
  valid NAT/router address and the next invalid number; reject an impossible topology
  before mutation rather than constructing `192.0.2.(100 + rangeNumber)` outside IPv4.

### PLUGIN — Legacy-to-RPC and subsequent plugin updates

- **PLUGIN-01 · P0 · F3 · B1:** Legacy Enterprise `.so` → supplied matching RPC `.plugin`,
  with correct installation in public and admin services. Verify metadata/version, a real
  licensed operation, entitlements/quotas and logs; a 200 health response is not success.
- **PLUGIN-02 · P0 · F3 · B1:** Offline licensed migration with supplied RPC artifact;
  absent plugin; genuine legacy `.so`; wrong version/name/protocol/architecture; corrupt
  signature; non-executable file. Valid artifact must load; invalid required artifacts
  must prevent commit and preserve old service, not leave a silently degraded installation.
- **PLUGIN-03 · P0 · F3:** Online test license at activation limit, offline license,
  expired license, and license-server outage. Account for a changed machine fingerprint
  when moving into LXC. Valid licensed installs must not lose entitlements or require
  unannounced seat cleanup; invalid licenses must fail closed with a clear recovery path.
- **PLUGIN-04 · P0 · F5:** Upgrade licensed RPC binary with older/equal/newer server and
  compatible protocol versions; test `.local` pin and unavailable update service. Verify
  signed/metadata-validated atomic replacement, restart-required messaging, old process
  behavior until restart, and both services actually loading the selected version afterward.
- **PLUGIN-05 · P0 · F5:** Uploaded resource plugin with persisted config/data/ACLs/jobs:
  replace, restart, disable/enable, and reject an invalid package. The implementation
  intentionally creates a new pending resource ID on replacement; verify consumers and
  jobs rebind correctly, ACL/data survive, and failed replacement leaves the old plugin usable.
- **PLUGIN-06 · P0 · F3/F5:** Provider-owned VM start/stop/status/address hooks, including
  unavailable/crashed/timed-out plugin during reapply and after restart. Selected-provider
  failure must not silently fall back to starting the wrong resource in Proxmox; plugins
  without the optional capability must retain the documented native fallback.
- **PLUGIN-07 · P0 · F5:** Personal/global/system plugin scopes, cross-user/range calls,
  refreshed API tokens, pinned TLS, and overlapping legacy placeholder routes. Verify
  only authorized records/actions are exposed, jobs run once, and plugin crash/uninstall
  cannot leave misleading success, duplicate routes, or orphan privileged children.

### FAIL — Preflight, concurrent activity, interruption, and recovery

- **FAIL-01 · P0 · FX:** Existing VMID, bad storage/content type, insufficient space,
  unavailable token/insufficient ACLs, corrupt/truncated template, checksum/signature
  mismatch, incompatible archive schema/path/symlink, and wrong decryption key. Expect
  nonzero status, actionable cause, and original API/WG/range operation intact.
- **FAIL-02 · P0 · FX:** Multi-node legacy host; incompatible SDN zone/external IPAM;
  pending SDN changes/lock; unsupported NAT VLANs; WireGuard hooks; missing user/range
  files. Test each actual preflight rejection and preservation of unrelated state.
- **FAIL-03 · P0 · F2/F4:** Create a user or mutate static config after staging; separately
  make DB-only updates/API-key rotation, and poll the range cache repeatedly. Static or
  topology changes must abort safely; allowed final DB changes must not be lost, and a
  volatile cache refresh alone must not spuriously abort. Include DATA-02's stale-DB trap.
- **FAIL-04 · P0 · F2:** Active Ansible/Packer before launch, then a worker starting during
  staging, plus concurrent migration attempts. Expect refusal/exclusive lock before
  disruptive work. Long-lived normal API sessions must not be mistaken for deployments.
- **FAIL-05 · P0 · F2:** Inject a controlled failure at each boundary: download/extraction,
  token creation, container creation/boot, staged bootstrap, archive import, final transfer,
  SDN apply, first/middle/last NIC move, WG handoff, API readiness, and forwarding enable.
  See the checkpoint protocol below; every pre-commit failure must restore the baseline service.
- **FAIL-06 · P0 · F2:** Send SIGINT/SIGTERM at staging and cutover boundaries. Check exit
  status, original live services, routes/NICs, keys, and disabled retained candidate. Then
  run the saved rollback helper again to prove repeatability, not just first-pass recovery.
- **FAIL-07 · P0 · F2:** SIGKILL or power interruption of the **nested VM**, including
  immediately before/after commit. EXIT traps do not run in these cases. Require an
  operator-visible recovery route that selects one authoritative DB and restores service
  without guest destruction; do not claim automatic recovery without exercising it.
- **FAIL-08 · P0 · F2:** Rollback itself encounters NIC hotplug failure, unavailable PVE
  API, or iptables restore failure. Expect explicit incomplete-rollback reporting, retained
  recovery metadata and checkout ownership, and no false success/automatic pool release.
- **FAIL-09 · P0 · F5:** Commit succeeds but writing `/etc/ludus-lxc.json` or replacing the
  status helper fails. Expect usable committed service, diagnosis of the finishing error,
  safe rerun/status discovery, and no attempt to migrate the stale host database again.
- **FAIL-10 · P0 · F5:** Make post-commit user/range/plugin writes before requesting
  rollback. A blind host rollback would lose those writes. Require an explicit recovery
  procedure/data reconciliation; restoring a test baseline proves cleanup, not lossless
  product rollback after commit.

### ENV — Platform and scale combinations

- **ENV-01 · P0 · F1/F2:** Run populated migration on both supported PVE majors 8 and 9,
  not merely a fresh install. Include hosts originally installed via Debian and via
  existing Proxmox, since their network/config provenance differs.
- **ENV-02 · P1 · F4:** Directory-backed and LVM-thin container storage, plus ZFS/shared
  VM/ISO storage when supported. Vary qcow2/raw workloads. Expect no VM disk movement,
  correct storage/token permissions, sufficient staging space, and working future clones.
- **ENV-03 · P1 · F2/F4:** Default ports and asymmetric custom ports; private management
  and explicit management; private CA and default certificates; direct endpoint and
  reverse proxy. Use pairwise combinations plus the full customized licensed fixture.
- **ENV-04 · P1 · large F2:** Representative largest supported user/range counts, large
  attachments/archives and many WG peers; measure staging and final-copy pause separately.
  Validate free-space checks and client timeout behavior. Large-file copy time is not
  covered by the small SQLite snapshot unit test.
- **ENV-05 · P2 · F5:** Soak migrated and fresh LXCs through repeated scheduled jobs,
  token refresh, source sync, plugin updates, guest DHCP renewal, and reboot. Monitor
  connection/process leaks, WAL locks, repeated provisioning, and stale authorization.

## Fault-injection checkpoint protocol

Use only a reserved disposable nested VM. Build a controllable test harness or explicit
test artifact; do not race a human typing commands against an arbitrary sleep. Associate
each injection with an observed installer checkpoint and restore the same fixture before
the next run. Keep source changes for instrumentation separate from the release artifact.

For every FAIL-05 boundary run both a returned command error and, where meaningful,
an abrupt interruption. Preserve these independent assertions:

1. **Before service quiesce:** existing authenticated API/WG traffic continues; no
   source data changes; staging failure cannot leave a second live control plane.
2. **After old services stop, before network cutover:** restored host credentials and
   services answer at the original endpoint; final acknowledged writes still exist.
3. **After NAT/SDN or partial NIC changes:** old NIC definitions **and live attachment**,
   routing, gateway, iptables, sysctls, leases and service states return to baseline.
   The management uplink and unrelated SDN objects remain unchanged.
4. **After new API is healthy, before commit:** failure still restores the old host.
   Check whether a queued write reached the candidate before rollback; losing an
   acknowledged write is a release blocker even if health probes pass afterward.
5. **After commit:** automatic rollback is no longer the contract. Verify durable
   forwarding/startup and explicit operator recovery, with no silent rollback of
   new data. The migration directory remains required by the forwarding service.

Repeat failed runs after the documented cleanup, including a retained stopped candidate
with the previous VMID. A successful retry must not rely on manually deleting unrelated
tokens, guests, ACLs, or network state.

## Execution order and release gate

1. **Local/CI gate:** compile and race-test all modules; run Linux-only packaging tests,
   shell/YAML checks, plugin protocol/package tests, and signed artifact checks. Add
   targeted regressions for B1–B3 before expensive upgrade runs.
2. **Primary golden upgrades:** F2/2.3.4 on PVE 9 via normal installer, F1/2.3.2 direct
   on PVE 9, and populated F1/F2 on PVE 8. Each runs all applicable P0 AUTH, DATA,
   NET and RANGE checks, then LXC and nested-host reboot.
3. **Compatibility gates:** F3 licensed offline and online; F4 customized; no-default-range
   SSO fixture; fresh F0; existing F5 update. Cover independent axes pairwise rather
   than multiplying every possible flag, but never separate SSO from deployed ranges.
4. **Recovery gates:** FX preflight matrix plus every FAIL checkpoint on a populated
   fixture. Repeat the final DB-copy, NIC-move and pre-commit-write faults on F3/F4.
5. **Scale/soak:** representative large supported fixture and an overnight F5 run.

Proposed CI additions are separate explicit jobs for populated community migration,
SSO migration, licensed/offline migration, customized state, failure recovery, and
post-migration reboot/update. Keep their fixture versions pinned. Health-only install
jobs should remain smoke tests, not be renamed as upgrade qualification.

Release requires all P0 executions to pass, B1–B3 fixed or the affected configurations
explicitly rejected/documented before mutation, and all supported P1 variants covered.
Every pass needs the saved before/after evidence. Unknown SSO provisioning policy,
private admin-endpoint relocation, unsupported legacy clusters/custom networking, and
post-commit rollback limitations must be visible in release/upgrade guidance.
