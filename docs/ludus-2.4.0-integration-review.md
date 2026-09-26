# Ludus 2.4.0 integration and qualification

Qualification began September 25, 2026. This supersedes the earlier stop-before-merge review after the user authorized fixes, merges, and testing on VMs 2001–2004.

## Status

The branch histories are merged and all five reported integration blockers are fixed. The requested final repeat is complete: all four designated VMs were restored to `pre-testing`, then clean installation, normal standalone upgrade, and two-node cluster upgrade passed using the same `2.4.0+ca5bd167` appliance. Earlier full custom-directory upgrades, injected-failure rollback, reboot, and network-persistence checks also passed. Qualification is limited to the fixtures and boundaries stated below, not every scenario in the broader test matrix.

The follow-on deployment exercise also passed: six original ranges and a new ordinary user's range completed full deployments, with one test-role marker per range and WireGuard reachability to all 26 range VMs. It exposed two additional fresh-deployment defects, fixed in `775733b`, and required recovery of pre-existing cluster NFS/NTFS fixture failures. The initial failures, user-approved repair, and final evidence are recorded below.

## Integrated revisions

- Starting `2-4-0`: `3263236`, Ludus 2.3.5.
- Local `plugin-updates` at `ef32d1e`: merged by `4956798`.
- `origin/ludus-lxc-plugins-rpc-integration` at `c8e86a6`: merged by `ef4df81`. The actual branch name uses **plugins**, plural.
- Remote-only `origin/plugin-updates` at `8036f85`: merged by `07803d9`, retaining its removal of unreleased plugin-schema/deletion compatibility.
- `28e4c6f`: migration state, RPC artifact, global-defaults, early rollback, and clustered-cutover fixes.
- `b795bc9c`: active ARP ownership detection instead of treating stale neighbor-cache entries as live gateway conflicts.
- `b778290c`: include the cluster helper in the isolated CI appliance builder and remove the obsolete ICMP prerequisite.
- `ca5bd167`: retire legacy managed host route-hook blocks at cutover and restore their exact contents/modes on rollback.
- `775733b`: create the per-user role-upload directory and package the established Proxmox Packer fork/version.

The `go.sum` merge conflict retained the existing 2.3.5 `go-proxmox` replacement, `v0.0.0-20260925164902-37c99f03b4d7`, and the added RPC dependency checksums. No remote branch was pushed.

## Integration blocker changes

### 1. Existing Proxmox clusters could not migrate

**Cause:** migration preflight rejected clustered hosts, while fresh-install network setup could replace existing VXLAN configuration and assign the same gateway on multiple peers. A host-only NIC cutover could not safely move workloads owned by another node.

**Changes:**

- `ludus-server/lxc/migrate-cluster.py` now discovers the participating nodes, checks quorum and existing SSH trust, snapshots per-node state, stages networks, performs coordinated cutover, and restores each node on failure. It preserves the existing zone, peers, MTU, VNI tags, VM ownership, disks, and MAC addresses, and moves each NIC/tap/firewall uplink on its actual owning node.
- `install.sh` and `ludus-server/lxc/migrate-host.sh` use migration-specific staging rather than fresh SDN provisioning. The candidate NAT NIC stays disconnected until the old `.254` owner is removed. Only the original Ludus host receives `192.0.2.49`; the LXC takes `.254`.
- `host_managed_network: true` is generated and retained during configuration merge. `ludus-server/bootstrap.go`, `reconcile.go`, `ludus-api/config.go`, `sdn.go`, and `pveclient/host_network.go` use `RequireVNet` / `EnsureVNetPreservingTag` to validate/reuse existing networks without retagging live ranges or installing a gateway on every VXLAN peer. New ranges can still create VNets.

**Evidence:** real two-node upgrade and automatic rollback; twelve distributed workloads, original clients and fifteen VPN targets; new-user/range creation; candidate reboot and both-node network reload. Detailed results appear below.

### 2. Final synchronization ignored a custom database directory

**Cause:** initial export honored `data_directory`, but the final stopped-service copy read `/opt/ludus/db`, risking stale data or failure when that directory was absent.

**Changes:** `ludus-server/migration.go` carries and validates the source `DataDirectory` in the manifest and checks it against archived configuration. `ludus-server/lxc/migrate-host.sh` uses that source for final synchronization. Only the archive/destination layout is normalized to `/opt/ludus/db`; the default path is not a fallback.

**Evidence:** two full upgrades from `/srv/ludus qualification/db`, once with a stale default directory and once with no default directory. A transaction committed after staging appeared in the final LXC, while the unused copy remained unchanged.

### 3. Supplied Enterprise artifacts used the obsolete plugin format

**Cause:** the installer placed the supplied artifact at legacy `.so` paths with non-executable permissions, while the integrated runtime expects executable RPC `.plugin` processes.

**Changes:** `install.sh` installs `ludus-enterprise.plugin` with mode `0755` in both public/admin plugin directories, owned by the respective service users. `ludus-server/migration.go` requires regular executable files and validates actual RPC metadata: Enterprise identity and matching non-empty versions. `ludus-api/plugins.go` exposes the existing probe as `ReadPluginMetadata`; licensing and integration-test callers were migrated without an obsolete alias.

**Evidence:** Linux regressions exercise real protocol subprocesses and reject legacy `.so`, wrong identity, mismatched versions, missing execute permission, symlinks, and incompatible RPC processes. A commercial Enterprise artifact/license was unavailable; commercial activation is not claimed.

### 4. Customized global Ansible defaults were lost

**Cause:** migration excluded the global defaults file, and embedded playbook replacement could overwrite it with packaged defaults.

**Changes:** `ludus-server/migration.go` separately exports, allowlists, validates, and transactionally imports `ansible/server-config.yml`. The shell helper includes it in source fingerprinting. `ludus-server/update.go` preserves its bytes and permissions when replacing embedded Ansible files and rejects a symlink before mutation. Existing router template/name settings are retained; obsolete host playbooks are not imported.

**Evidence:** exact customized-file SHA-256 preservation in both custom-directory upgrades and the cluster upgrade; regression coverage for malformed defaults, archive boundaries, router defaults, payload replacement, and permission preservation.

### 5. Early failures could leave a competing candidate running

**Cause:** rollback learned the candidate VMID too late, so a failure after creation/start but before cutover could leave it running or enabled at boot.

**Changes:** `install.sh` saves the migration-owned VMID and `container-created` marker immediately after `pct create`, before first boot/import. `ludus-server/lxc/migrate-host.sh` stops that candidate and disables autostart before restoring competing host services. Failed candidate disks remain available for diagnosis; unrelated containers are not selected.

**Evidence:** real import and bootstrap-phase injected failures, restored original API/VPN/workloads, and an actual host reboot proving candidate 9999 stayed stopped with `onboot: 0`.

### Additional corrections discovered during qualification

- **Peer API connectivity during staging:** `migration_forwarding stage` in `migrate-host.sh` installs private management-subnet outbound NAT before bootstrap. It does not redirect original API/WireGuard endpoints. The missing NAT was reproduced as a real cluster bootstrap failure; automatic rollback and corrected peer connectivity were exercised.
- **False gateway conflicts after rollback:** `migrate-cluster.py` now actively solicits ARP instead of trusting cached neighbors or ICMP. Real namespace checks cover an IPv4-less peer and an ICMP-blocking live owner; the actual stale-cache cluster retry subsequently passed.
- **Isolated CI packaging omitted the new helper:** `ludus-server/ci/build-lxc-isolated.sh` now stages `migrate-cluster.py`; the isolation fixture was updated. The omission failed before the change, passed afterward, and the actual isolated builder produced the final appliance.
- **Network reload resurrected old routes:** `migrate-cluster.py` snapshots both named host hooks, `/etc/network/if-up.d/sdn-routes` and `ludus-routes`, and retires only complete Ludus-managed blocks for migrated ranges. Unrelated content and modes remain intact; rollback restores the exact originals. Live reload and real isolated snapshot/cutover/restore checks cover this behavior.

## Test environment and baseline

Only the designated nested VMs were modified. `kind-rook` was used as the SSH jump host and for authorized snapshot/VM control; no outer-host installation, networking, storage, or unrelated VM was changed.

- **2001**, `203.0.113.201`: clean Proxmox 9.2.20; also used to build Linux artifacts and run external API/WireGuard probes.
- **2002**, `203.0.113.202`: standalone Ludus 2.3.5, three successful ranges and twelve running workload VMs.
- **2003/2004**, `203.0.113.203` / `.204`: quorate two-node Proxmox 9.2.20 cluster, Ludus 2.3.5 on node 1, twelve running workloads distributed across both nodes, shared `ludus-nfs` storage.
- Cluster baseline: VXLAN zone `ludus`, MTU 1450, original two peers, NAT VNI 16777215, and range VNIs 1/2/3.
- All four `pre-testing` snapshots include RAM. VMs 2002/2003 each have T01 as admin and T02/T03 as ordinary users. Licenses are Community.

Before upgrades, all six existing API keys and passwords authenticated at the original endpoints. TLS certificate fingerprints, range identities, VM IDs/names/IPs/power states, storage, disks, MACs, and node ownership were recorded. Non-admin cross-range requests returned 403. All six unchanged WireGuard profiles completed a handshake and reached the VPN gateway and each of their four workloads: thirty reachability targets in total.

## Completed first-cycle runtime checks

### Clean installation — VM 2001

- Initial appliance installation succeeded in **27.74 seconds**; both API services, WireGuard, dnsmasq, and HTTPS health were active.
- The real 2.4.0 CLI created admin QA and non-admin QU. Both password/API-key authentication paths, default-range ownership, range status, template/source listing, and non-admin cross-range denial passed.
- Both newly generated WireGuard profiles reached the VPN gateway.
- The first development build omitted the optional embedded frontend and returned 404 at `/ui`. The private GUI source was then built using the release pipeline's static-export approach and embedded. This was a build-input correction, not a server workaround.
- The supported in-container `--update --no-dep-update` path installed `2.4.0+28e4c6f` successfully. A real browser rendered login, authenticated QA, and displayed the expected Community license gate. Full paid UI functionality was not enabled or bypassed.

### Standalone migration with a stale default directory — VM 2002

- Configured source: `/srv/ludus qualification/db`; `/opt/ludus/db` deliberately retained stale data. Both source service sandboxes were explicitly allowed to write the custom directory, and the source baseline was reverified before migration.
- Full upgrade succeeded in **209.72 seconds**.
- After the real initial import completed, while both original API services remained active, a SQLite transaction changed T03's name from `Test T03` to `Qualification committed after staging`. The final LXC contained the new value; the stale default directory retained the old value.
- Customized global defaults survived payload refresh with exact SHA-256 `a7c7ab137f55231314d6fe343c38f8028e2fc3cd897064a71928b4ec8af22069`.
- Original router defaults, three users, API keys, passwords, TLS fingerprint, successful ranges, twelve running workloads, disk/MAC identities, storage, ports, and non-admin isolation were preserved. All fifteen unchanged-profile VPN targets passed.

### Standalone migration with no default directory — VM 2002

- After an additional authorized restore of VM 2002, repeated a **full upgrade** with the same custom source and `/opt/ludus/db` entirely absent. The unused default database was retained outside that path for comparison.
- Full upgrade succeeded in **204.88 seconds** on `2.4.0+28e4c6f` media.
- The post-staging source transaction reached the final normalized database; the unused copy did not acquire it.
- Exact customized-defaults SHA-256 `7cc4abfe9df8c7465edcba5db718839a7a8942abc020827c4c5a4f3d7c6e75c3` was preserved. The different hash reflects this fixture's different comment, not lost defaults.
- Original authentication, TLS, all twelve workload identities, storage/disks/MACs, router defaults, isolation, and all fifteen VPN targets passed again.

### Fault recovery and reboot

- Injected state-import failure: installer exited **42** after **102.69 seconds**. Recovery metadata identified candidate 9999; it was stopped with `onboot: 0`; original API, TLS, users, ranges, and VPN recovered.
- Injected bootstrap-phase command failure immediately after candidate start: installer exited **43** after **71.42 seconds**, with the same safe candidate/service recovery. This was a bootstrap-phase failure, not a simulated five-minute timeout.
- Actually rebooted VM 2002 after rollback. The boot ID changed and the failed candidate remained stopped with autostart disabled. Original workloads had `onboot: 0`; those settings were retained, and exactly the twelve baseline-running workload VMIDs were explicitly restarted. API and all fifteen VPN targets passed afterward.
- Retry deliberately refused to overwrite an existing migration-created API token. Only the verified failed candidate and its qualification-created token were removed before retry.
- An in-flight WireGuard probe lost connectivity during cutover; every completed post-upgrade probe passed with unchanged profiles. **No zero-downtime VPN claim is made.**

### Cluster failures that exercised automatic recovery

- The first cluster attempt failed bootstrap after **426.53 seconds** because the private management subnet could reach the owning node but not the peer API. Automatic rollback restored both nodes' network files, SDN and VM configurations, addresses, routes, VM identity/ownership/running states, and quorum. Candidate 9999 was stopped with autostart disabled. Original API/authentication/TLS/isolation and all fifteen VPN targets passed after rollback.
- During that staging/rollback attempt, 120/120 sampled original-endpoint health requests returned HTTP 200. This is scoped evidence, not a general uninterrupted-cutover guarantee.
- A subsequent attempt stopped safely during preflight after **27.62 seconds** because the old collision check mistook a stale peer neighbor entry for a live `.49` owner. No candidate or token was created. The active-ARP correction was exercised in real isolated network namespaces and on both actual cluster nodes.

### Successful clustered upgrade — VMs 2003/2004

- The corrected cluster migration succeeded in **240.80 seconds** using `2.4.0+b795bc9c` media, SHA-256 `6cc07b0ed46cebd3f28f2c76565914264357fbd3942c14d898c5edd4b7a315d8`.
- All twelve workloads retained their VM IDs, disks, MACs, owning nodes, power states, and storage. NIC taps/firewall uplinks were checked on their actual owning nodes. The zone, MTU, peers, NAT/range VNIs, owner-only `.49`, candidate-only `.254`, and peer without an API instance were preserved.
- All original passwords/API keys, exact TLS fingerprint, range identities/states, non-admin 403 responses, and fifteen unchanged-profile VPN targets passed.
- Global defaults retained exact SHA-256 `bb25dace1e6625be8fce7246bfa3f22d7b2df21e0cbc4ee4f8e81ee701216e2a`; `host_managed_network: true` survived candidate reboot.
- The real new CLI created ordinary user CQ and its empty default range 4. VNet `r4` became active on both nodes with VNI 4, without changing existing tags or workloads.
- Candidate reboot and both-node network reload exposed the legacy route-hook override. The correction was applied using the new helper, then reload, complete inventory/API acceptance, and all fifteen VPN targets passed again. A separate real mount/network-namespace exercise preserved unrelated hook content/range 70 and restored both hook files' exact bytes and mode `0751`. The final rebuilt-media repeat below subsequently proved automatic installer integration.
- Cutover health sampling returned HTTP 200 for **116/120** requests; four consecutive requests timed out after five seconds each. Approximately twenty seconds of interruption was observed. **This is not a zero-downtime migration claim.**

## Final snapshot repeat

After the first-cycle tests, all four VMs were stopped before any restore. Each was restored once to its RAM-inclusive `pre-testing` snapshot, and guest-agent/SSH access recovered. The clean node again had no Ludus host configuration or candidate container.

Every final scenario used the same isolated-CI-built appliance:

- Code revision: `ca5bd167`; observed server/client version `2.4.0+ca5bd167`.
- Artifact: `ludus-2.4.0-debian13-amd64.tar.zst`.
- SHA-256: `5ee67a522642511f8e595281044a88c351d864b7e05c368c2b39508330487309`.
- The saved local copy and all three transferred copies passed checksum verification.

The final clean installation succeeded in **26.52 seconds**. Runtime reported `2.4.0+ca5bd167`; both API services, WireGuard, and dnsmasq were active. The real CLI created QA/QU; password/API-key authentication, default-range ownership, non-admin isolation, template/source listing, and both VPN gateway probes passed. `/ui` returned HTTP 200; a real browser authenticated the newly created QA user and displayed the active Community license gate.

The final **standalone upgrade succeeded in 204.73 seconds**, from `2.3.5+3263236a` using its normal `/opt/ludus/db` source. No custom-path, name-change, or fault fixture was applied during this final run:

- Original 2.3.5 authentication, TLS, twelve running workloads, range/isolation checks, and fifteen VPN targets passed before migration.
- All original users/names, API keys/passwords, exact TLS fingerprint, range IDs/states, VM identities/IPs/disks/MACs, storage, configured ports, and non-admin 403 behavior were preserved.
- Both API services and WireGuard were healthy on `2.4.0+ca5bd167`; legacy host services were disabled/inactive and forwarding was active.
- Host `ifreload -a` retained range/VPN routes through `.254`. Candidate `systemctl restart networking` retained its exact routes; an actual `pct reboot 9999` changed systemd userspace start identity and recovered healthy services. Full original API/VPN checks passed after each action.
- Global defaults retained exact SHA-256 `bb25dace1e6625be8fce7246bfa3f22d7b2df21e0cbc4ee4f8e81ee701216e2a`; all fifteen unchanged-profile VPN targets passed after reboot.
- Harness correction: the candidate has ifupdown 0.8.44, not the host's ifupdown2. An attempted candidate `ifreload` exited 127 without changing networking; its service has no reload action. The supported networking-service restart above was then exercised. No source or package change was needed.

The final **cluster upgrade succeeded in 240.31 seconds**, from `2.3.5+3263236a` to client/server `2.4.0+ca5bd167`:

- The restored source passed original-client/API/TLS/password/key/isolation and fifteen VPN checks before migration, with both nodes quorate and all twelve workloads running.
- Original VM IDs, names, disks, MACs, node ownership, power states, storage definitions, VXLAN peers/MTU, and existing VNIs were retained. Eighteen actual NIC bridge-port PVID bindings were verified on the owning nodes.
- Only the owner had `.49`; only the candidate had `.254`. Peer connections to API ports 8080/8081 were refused, and its Ludus service was inactive.
- Packaged route-hook retirement happened automatically. Candidate reboot and `ifreload -a` on both hosts retained owner routes through `.254`, unchanged peer routes, and the host-managed configuration. **No manual hook/network fixes or source changes were applied during this final run.**
- All original API/password/key/TLS/range/isolation checks and fifteen unchanged-profile VPN targets passed after all changes. Global defaults retained SHA-256 `bb25dace1e6625be8fce7246bfa3f22d7b2df21e0cbc4ee4f8e81ee701216e2a`.
- The final CLI created ordinary user CQ and empty range 4; `r4`/VNI 4 was active on both nodes without altering the original networks or workloads.
- Final cutover sampling recorded **175/180 HTTP 200** responses and five consecutive approximately five-second timeouts. Post-cutover acceptance passed; uninterrupted service is not claimed.

### Commands used

Run from `/root/qualify` on the named nested node after staging the final archive and installer. These are local development-media commands, not release-signature qualification:

```bash
sha256sum -c ludus-2.4.0-debian13-amd64.tar.zst.sha256

# VM 2001: clean installation
bash install.sh --no-prompt --version 2.4.0 \
  --template-file /root/qualify/ludus-2.4.0-debian13-amd64.tar.zst \
  --skip-verification --vmid 9999 --storage local-lvm \
  --hostname ludus240 --nameserver 203.0.113.254 \
  --bridge vmbr0 --ip 203.0.113.211/24 --gw 203.0.113.254 \
  --vm-storage local-lvm --iso-storage local --wg-endpoint 203.0.113.211

# VM 2002: standalone upgrade; VM 2003: cluster upgrade.
# Do not run a separate installer on peer VM 2004.
bash install.sh --no-prompt --version 2.4.0 \
  --template-file /root/qualify/ludus-2.4.0-debian13-amd64.tar.zst \
  --skip-verification --vmid 9999 --storage local-lvm \
  --hostname ludus240 --nameserver 203.0.113.254 --migrate-host
```

## Build and regression verification

- Linux amd64 Go tests passed across `ludus-server`, `ludus-api`, `ludus-client`, and `dynamic-inventory`, including an `embedwebui` build. The Linux embedded inventory executable was built first.
- Eight cluster migration helper tests passed, including active-ARP collision boundaries and route-hook preservation/retirement/restore behavior.
- Seven installer-status tests and three Linux/root build-isolation tests passed.
- Actual isolated-builder staging reproduced the omitted-helper failure before the fix and succeeded afterward, with workspace cleanup preserved. The real isolated CI builder then produced the final `ca5bd167` appliance, with no source-tree rootfs left behind.
- An isolated Linux network-namespace smoke exercised management-only staging, final endpoint forwarding, and removal: staging provided candidate egress without redirecting old API/WireGuard ports; final forwarding did not expose admin port 8081; unrelated firewall state survived stop.
- Shell syntax checks passed for the installer and migration helper.
- Private GUI revision: `12cfcd32ffd658f7e425913a166fefb4a4cd56b0`; static export built with Bun 1.4.2 / Next.js 15.5.19. The browser exercised actual deployed assets, not a mock frontend.

With the Linux inventory executable and static GUI already supplied as build inputs, the test/build invocations were:

```bash
python3 -B ludus-server/lxc/test_migrate_cluster.py
go test -tags embedwebui ./ludus-server/... ./ludus-api/... \
  ./ludus-client/... ./dynamic-inventory/...
LUDUS_VERSION=2.4.0 bash ludus-server/ci/build-lxc-isolated.sh
```

## Initial qualification evidence and fixture state

Private evidence is grouped under the directory identified below:

- `final-artifact.json`, `final-reset-operations.json`, and `final-reset-access.json`: code/media provenance and all-four stop/restore barrier.
- `final-clean-2001-installer.json`, `final-clean-runtime-proof.json`, and `final-clean-fresh-*.json`: clean installer, actual services/version, client/auth/VPN, and browser result.
- `standalone-qualification-summary.json`, `missing-default/`, and `final-standalone/`: both custom-source cases, fault/reboot recovery, and final normal-path upgrade/reload/reboot acceptance.
- `final-cluster-2003-installer.json`, `final-cluster-verification.json`, `final-cluster-vlan-verification.json`, `final-cluster-candidate-complete.json`, and `final-cluster-runtime-range.json`: coordinated cutover, actual per-node NIC placement, reboot/reload persistence, and new range.
- `final-after-cluster-api-2003.json`, `final-after-cluster-vpn-2003.json`, and `final-cluster-cutover-health.json`: original-client acceptance and measured interruption.

At completion of the initial repeat, candidate 9999 was running on each of VMs 2001, 2002, and 2003; VM 2004 remained the cluster peer without a Ludus API instance. All twenty-four original workload VMs remained running. Fresh QA/QU users and the cluster's CQ/empty range 4 qualification fixture remained. No final reset was performed after the successful repeat.

That phase's temporary qualification SSH key was removed from all four guests and its local private key deleted. Temporary probe harnesses were removed; browser tabs and the UI tunnel were closed. Private evidence/artifacts and the migration recovery directories required by runtime forwarding were retained.

## Additional full-deployment verification — September 26

The follow-on request exercises deployments, not just API health or existing workloads. A benign local role, `ludus_post_upgrade_probe`, writes `/etc/ludus-post-upgrade-qualification` as root with mode `0644`, containing a unique qualification marker and `inventory_hostname`. It was uploaded through each owner's real CLI/API and attached to exactly one non-router Linux VM per range. Existing roles and other configuration were preserved.

Every run used unrestricted `ludus range deploy`: **no `--tags`, `--only-roles`, `--limit`, or manual bypasses of Windows tasks**. Evidence includes terminal job state, actual deployment logs, guest-agent reads of marker contents, and original VM configuration comparisons. WireGuard checks used the original profiles in isolated network namespaces, verified recent handshakes, pinged each gateway/VM, and connected to SSH port 22 or Windows RDP port 3389.

### Existing standalone ranges — VM 2002

- T01, T02, and T03 all completed full deployments with **SUCCESS** on the migrated `2.4.0+ca5bd167` server.
- Exact role markers appeared only on T01 Kali **VM 116**, T02 Kali **VM 114**, and T03 Kali **VM 115**. Markers were absent on all twelve guests before deployment and on the other nine afterward.
- All twelve complete Proxmox VM configurations and running states matched the baseline; original names, IDs, IPs, disks, UUIDs, and NICs were retained.
- All three original users authenticated. Before and after: **15/15 ICMP targets and 12/12 VM TCP targets passed**, using SHA-256-identical WireGuard profiles.
- Evidence: `post-deploy/standalone/summary.json`, deployment logs/transitions, before/after VM configurations, marker reads, and VPN reports.

### Existing clustered ranges — VMs 2003/2004

- T01 and T02 completed full deployments on their first attempt. T03 initially failed on its existing Windows domain controller's corrupted filesystem; after the explicitly approved repair below, its **full** second deployment completed with **SUCCESS**.
- Exact role markers appeared only on T01 Kali **VM 114** on peer 2004, T02 Kali **VM 115** on owner 2003, and T03 Kali **VM 116** on peer 2004. The other nine guests remained marker-free.
- All twelve VM configurations were byte-identical, including disks, UUIDs, and NICs; ownership and running states were preserved. VXLAN tags 1/2/3/4, NAT VNI 16777215, MTU 1450, quorum, owner-only `.49`, LXC-only `.254`, and the unused CQ configuration were retained.
- Original credentials and unchanged WireGuard profiles passed before and after: **15/15 ICMP targets and 12/12 VM TCP targets**, including peer-owned guests.
- Evidence: `post-deploy/cluster/qualification-final-summary.json`, `role-execution-proof.json`, `markers-final.json`, `vm-details-final.json`, `final-vpn.json`, and `topology-final.json`.

### Pre-existing cluster fixture failures and approved recovery

Before deployment, two peer guests were unreachable because their QEMU I/O was blocked by a stale NFSv4 session. Packet capture showed `NFS4ERR_SEQ_MISORDERED`; the owner NFS server and local storage were healthy. Restarting `nfs-server` on nested owner 2003 cleared the blocked tasks and restored guest agents/IPs without VM resets or disk replacement. All original-client VPN checks then passed.

T03's first full deployment subsequently failed with DISM `0x80070570` while accessing `C:\Windows\Temp` on **VM 111**. NTFS event 55 predated deployment and identified damaged Temp/Defender indexes; read-only CHKDSK reported corrupted/orphaned file records and a dirty volume. This was not bypassed by narrowing the deployment.

The user explicitly approved a safety snapshot/backup followed by native repair:

- Snapshot-mode `vzdump` backup retained on peer 2004 at `/var/lib/vz/dump/vzdump-qemu-111-2026_09_26-08_47_17.vma.zst`.
- Backup passed both `zstd` integrity testing and `vma verify`; SHA-256 `e1ba89056fddaec6abc8252a7998a93b24a2ec3e267dd4ad38aaf190647d8ff9`.
- Scheduled `chkdsk C: /F` and a guest-native `Restart-Computer`; no QEMU reset, replacement, or restore.
- Windows repaired records/indexes, recovered sixteen files to their original directories and one to `found.000`. Subsequent read-only CHKDSK found no problems; C: was healthy/not dirty and AD feature queries succeeded.
- The complete T03 deployment and final VPN/identity/marker checks passed afterward. Initial failure and repair evidence remain in `post-deploy/cluster/T03-*`.

### Fresh-install range — VM 2001

New ordinary user **PD** was created on the fresh appliance; the owner-authenticated user API confirmed `isAdmin: false`. After the corrections below, `debian-13-x64-server-template` was built from its ISO in 263 seconds. PD's unrestricted full range deployment completed with `SUCCESS` in 191.8 seconds, creating:

- **VM 101**, `PD-router-debian13-x64`, `10.3.10.254`, running.
- **VM 102**, `PD-debian13-probe`, `10.3.10.10`, running.

The actual deployment log records the test role changing VM 102. A guest-agent read verified the exact marker `2026-09-26:2001:PD:fresh-full-deployment`, followed by `PD-debian13-probe`, owned by root with mode `0644`. The router had no marker. PD generated a real WireGuard profile through its own CLI/API; a recent handshake, gateway and both VM pings (**3 ICMP targets**), and SSH connections to both VMs (**2 TCP targets**) passed from an isolated network namespace.

Evidence: `post-deploy/fresh-summary.json`, `fresh-template-build-fixed-final.log`, `fresh-PD-full-deploy-final.log`, `fresh-PD-role-execution.log`, `fresh-PD-marker-proof.json`, and `fresh-PD-vpn-proof.json`. Initial failing uploads/builds are retained separately; the final success is not a claim that the original image passed this path unchanged.

### Fresh-deployment corrections

The fresh-install exercise found two additional code/image defects, fixed in **`775733b`**:

1. `ludus-api/api_ansible.go`: local role upload assumed a user's `.ansible/tmp` already existed. The real first upload by new ordinary user PD failed with a missing-directory error. The handler now creates that directory using the existing role-download convention; the same owner upload then succeeded without manually creating it.
2. `ludus-server/lxc/build.sh`: the image packaged HashiCorp Proxmox Packer plugin 1.2.1, despite the existing updater requiring `badsectorlabs/proxmox` 1.2.4. The older plugin omitted the pool during VM creation and received HTTP 403. Packaging now uses the established fork/version and source-qualified download caches, avoiding cross-fork cache reuse. No global VM permissions were granted. See the [upstream pool-scoping fix](https://github.com/hashicorp/packer-plugin-proxmox/releases/tag/v1.2.2) and [fork release](https://github.com/badsectorlabs/packer-plugin-proxmox/releases/tag/v1.2.4).

All four Linux Go module suites passed with `embedwebui`. The actual isolated CI builder produced the corrected appliance, SHA-256 `f72a8ac5f4283eacf6de8246aa6bc5eb1ced1915f2adbec0ab57837378c91039`. Its packaged fork plugin is executable (`0755`); hashing the actual archived binary yielded `18117dff83c386dadac350b1c0a52225edb639e9da3bc0920e004c2344a790a0`, identical to the plugin that completed the fresh template build.

The six original ranges were redeployed on `2.4.0+ca5bd167`. Fresh end-to-end verification used an in-place binary/dependency update identifying itself as `2.4.0+c470094-rolefix`; those working-tree fixes were subsequently committed as `775733b`. The corrected archive was built and inspected, but was not used to reinstall the completed fresh fixture.

Two harness/configuration corrections were also necessary: the local test role needed Galaxy `meta/main.yml`, and the fresh fixture selected `local-lvm` but omitted the documented `--vm-storage-format raw`. After the plugin fix exposed the unsupported `qcow2` setting, only that storage-format setting was corrected. Direct `pct exec` dependency updates also needed the appliance services' virtualenv PATH and UTF-8 locale; the successful command is recorded in [UPDATING.md](../UPDATING.md).

### Follow-on final state and cleanup

- All **24 original range VMs plus PD's two new VMs** remain running. Original VMIDs, IP addresses, Proxmox configurations, cluster ownership, API keys, and WireGuard profiles were retained.
- The seven installed test roles, range configuration additions, and seven marker files remain in place as requested. The other 19 range VMs have no marker. The empty CQ range remains untouched.
- The verified VM 111 safety backup and native repair reports are retained. No snapshot reset, VM replacement, or backup restoration was performed during this follow-on exercise.
- The temporary qualification SSH key was removed and its absence verified on all four nested hosts. Its local private/public files and disposable deployment/probe scripts were removed. The probe's temporary WireGuard interface and network namespace were confirmed absent. Installed roles, profiles, credentials, deployment logs, build artifacts, and evidence remain private outside the repository.

Consolidated evidence: `post-deploy/post-deploy-summary.json`, `access-cleanup-proof.json`, and `local-cleanup-proof.json`, alongside the per-installation evidence listed above.

## Verification boundaries

These fixtures cover Proxmox 9.2.20, 2.3.5 source installs, and Community licensing. A real licensed Enterprise RPC artifact, signed offline license, external SSO provider, and PVE 8 fixture were not available in this qualification. Executable RPC validation was exercised with actual protocol subprocess fixtures, but no commercial Enterprise activation or offline licensed deployment is claimed.

Local development media used `--skip-verification`; transferred appliance SHA-256 values were checked independently. This does not qualify release-signature verification or establish an air-gapped installation result.

Private logs, manifests, original profiles, and credentials are retained outside the repository under `/var/folders/nw/whrcfbgx2dq3hfssvg0j5q8w0000gn/T/ludus-240-qualification-x5t8nohe`. They contain secrets and must not be published. This report intentionally includes no API keys, passwords, WireGuard private keys, or Proxmox token secrets.

See [the migration procedure](docs/infrastructure-operations/migrate-to-lxc.md) for supported topology, endpoint behavior, and recovery. The broader [upgrade test matrix](lxc-upgrade-test-matrix.md) remains a plan for additional fixture families, not a claim that all matrix scenarios were executed.
