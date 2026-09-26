#!/usr/bin/env bash
# Sourced by the installer from the verified appliance, never from the old host.

migration_value() {
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])).get(sys.argv[2], ""); print(str(v).lower() if isinstance(v,bool) else v)' "$MIGRATION_DIR/info.json" "$1"
}

migration_check_sdn_pending() {
  # Proxmox compares parsed SDN sections with sdn/.running-config, not *.new files.
  # Older releases omit fabrics from has_pending_changes(), so include them here.
  perl -MPVE::Network::SDN - <<'PERL'
use strict;
use warnings;
die "SDN configuration is locked by another operation; finish it before migrating\n"
    if -e '/etc/pve/sdn/.lock';
PVE::Cluster::cfs_update(1);
my $running = PVE::Network::SDN::running_config();
my $configs = {
    zones => PVE::Network::SDN::Zones::config(),
    vnets => PVE::Network::SDN::Vnets::config(),
    subnets => PVE::Network::SDN::Subnets::config(),
    controllers => PVE::Network::SDN::Controllers::config(),
};
for my $extra (['fabrics', 'Fabrics'], ['route-maps', 'RouteMaps'], ['prefix-lists', 'PrefixLists']) {
    my ($type, $module) = @$extra;
    my $config = ('PVE::Network::SDN::' . $module)->can('config');
    $configs->{$type} = { ids => $config->()->to_sections() } if $config;
}
for my $type (keys %$configs) {
    my $pending = PVE::Network::SDN::pending_config($running, $configs->{$type}, $type);
    for my $object (values %{ $pending->{ids} }) {
        die "SDN has pending changes; apply or revert them before migrating\n"
            if exists($object->{pending}) || exists($object->{state});
    }
}
PERL
}

migration_prepare() {
  [[ -f /opt/ludus/config.yml && ! -f /opt/ludus/install/.bootstrap-complete ]] || {
    echo "--migrate-host requires an existing host-installed Ludus instance" >&2; return 1;
  }
  [[ -z "${IMPORT_DB:-}" ]] || { echo "Do not combine --migrate-host and --import-db" >&2; return 1; }
  for cmd in pvesh pvesm qm pct ip ping perl iptables iptables-save iptables-restore conntrack systemctl flock; do
    command -v "$cmd" >/dev/null || { echo "Migration requires $cmd" >&2; return 1; }
  done
  exec 9>/run/lock/ludus-lxc-migration.lock
  flock -n 9 || { echo "Another host migration is running" >&2; return 1; }
  migration_check_sdn_pending
  "$MIGRATION_DIR/ludus-server" --migration-info >"$MIGRATION_DIR/info.json"
  if [[ $(migration_value requires_plugin) == true && -z ${ENTERPRISE_PLUGIN:-} ]]; then
    echo "This install requires a matching --enterprise-plugin; the old Go plugin cannot be reused" >&2
    return 1
  fi
  if pgrep -f '(^|/)(ansible-playbook|packer)( |$)' >/dev/null; then
    echo "Wait for all range deployments and template builds to finish before migrating" >&2; return 1
  fi
  WG_EP=${WG_EP:-$(migration_value wireguard_endpoint)}
  WG_PORT=$(migration_value wireguard_port)
  LUDUS_API_PORT=$(migration_value port)
  LUDUS_ADMIN_PORT=$(migration_value admin_port)
  VM_STORAGE=${VM_STORAGE:-$(migration_value proxmox_vm_storage_pool)}
  VM_STORAGE_FORMAT=${VM_STORAGE_FORMAT:-$(migration_value proxmox_vm_storage_format)}
  ISO_STORAGE=${ISO_STORAGE:-$(migration_value proxmox_iso_storage_pool)}
  LUDUS_NAT_IP=192.0.2.254
  # .1-.3 are reserved services, .50-.100 DHCP, and .101+ range routers.
  LUDUS_NAT_GATEWAY=192.0.2.49
  # Preserve the address used for DNS, DHCP, router management and default routes
  # by existing guests. Only the Proxmox-side SDN gateway moves.
  python3 "$MIGRATION_DIR/migrate-cluster.py" prepare "$MIGRATION_DIR" "${LXC_BRIDGE:-}" "${ETH0_IP:-}" "${ETH0_GW:-}" "$LUDUS_NAT_GATEWAY"
  MIGRATION_SDN_ZONE=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["zone"])' "$MIGRATION_DIR/network.json")
  LXC_BRIDGE=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["bridge"])' "$MIGRATION_DIR/network.json")
  ETH0_IP=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["ip"])' "$MIGRATION_DIR/network.json")
  ETH0_GW=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["gateway"])' "$MIGRATION_DIR/network.json")
  MIGRATION_COMMITTED=0
  trap 'migration_exit $?' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  python3 - "$MIGRATION_DIR/network.json" <<'PY'
import json,pathlib,subprocess,sys
c=json.load(open(sys.argv[1]))
if c['auto_management']:
    path=pathlib.Path('/etc/network/interfaces.d/ludus-migration')
    if path.exists(): raise SystemExit('A previous migration management configuration exists')
    path.parent.mkdir(parents=True,exist_ok=True)
    path.write_text(f"auto {c['bridge']}\niface {c['bridge']} inet static\n\taddress {c['gateway']}/30\n\tbridge-ports none\n\tbridge-stp off\n\tbridge-fd 0\n")
    interfaces=pathlib.Path('/etc/network/interfaces')
    text=interfaces.read_text()
    if 'source /etc/network/interfaces.d/*' not in text:
        interfaces.write_text(text+'\nsource /etc/network/interfaces.d/*\n')
    subprocess.run(['ifup',c['bridge']],check=True)
PY
}

migration_begin_sdn_stage() {
  install -d -m 0700 "$MIGRATION_DIR/staged-sdn"
  touch "$MIGRATION_DIR/sdn-changed"
}

migration_finish_sdn_stage() {
  local path
  rm -f "$MIGRATION_DIR/staged-sdn/"*.cfg
  for path in /etc/pve/sdn/*.cfg; do
    [[ -f "$path" ]] && cp "$path" "$MIGRATION_DIR/staged-sdn/"
  done
  touch "$MIGRATION_DIR/sdn-staged"
}

migration_stage_networks() {
  migration_begin_sdn_stage
  python3 "$MIGRATION_DIR/migrate-cluster.py" stage "$MIGRATION_DIR"
  migration_finish_sdn_stage
  # The private management subnet must reach every Proxmox node during
  # bootstrap, before any original API or WireGuard endpoint is redirected.
  migration_forwarding stage
}

migration_write_candidate_routes() {
  python3 - "$MIGRATION_DIR/network.json" "$MIGRATION_DIR/ludus-routes" <<'PY'
import json,pathlib,sys
c=json.load(open(sys.argv[1])); lines=['#!/bin/sh']
for r in c['ranges']:
    n=int(r['number'])
    lines += [
        f'# LUDUS MANAGED BLOCK FOR RANGE {n} BEGIN',
        'if [ "$IFACE" = "eth1" ]; then',
        f'\tip route replace 10.{n}.0.0/16 via 192.0.2.{100+n}',
        'fi',
        f'# LUDUS MANAGED BLOCK FOR RANGE {n} END',
    ]
pathlib.Path(sys.argv[2]).write_text('\n'.join(lines)+'\n')
PY
  pct push "$VMID" "$MIGRATION_DIR/ludus-routes" /etc/network/if-up.d/ludus-routes --perms 0755 --user 0 --group 0
  pct exec "$VMID" -- env IFACE=eth1 /etc/network/if-up.d/ludus-routes
}

migration_static_fingerprint() {
  python3 - <<'PY'
import hashlib,pathlib
roots=[pathlib.Path('/opt/ludus')/name for name in ('ranges','users','templates','resources','blueprints','sources','tls')]
roots += [pathlib.Path('/opt/ludus')/name for name in ('license.lic','cert.pem','key.pem','config.yml','install/root-api-key','ansible/server-config.yml')]
roots += [pathlib.Path('/home/ludus')/name for name in ('.ssh','.gitconfig','.git-credentials')]
h=hashlib.sha256()
for root in roots:
    if not root.exists(): continue
    paths=[root] if root.is_file() else sorted(root.rglob('*'))
    for path in paths:
        st=path.lstat()
        h.update(str(path).encode()+b'\0'+str(st.st_mode&0o777).encode()+b'\0'+str(st.st_size).encode()+b'\0')
        if path.is_file():
            with path.open('rb') as source:
                for block in iter(lambda:source.read(1024*1024),b''): h.update(block)
print(h.hexdigest())
PY
}

migration_stage_candidate_state() {
  local before after
  before=$(migration_static_fingerprint)
  rm -f "$MIGRATION_DIR/state.tar.gz"
  "$MIGRATION_DIR/ludus-server" --export-state-live "$MIGRATION_DIR/state.tar.gz"
  after=$(migration_static_fingerprint)
  [[ $before == "$after" ]] || {
    echo "Ludus files changed while the LXC state was staged; retry after the active command finishes" >&2
    return 1
  }
  printf '%s\n' "$after" >"$MIGRATION_DIR/static-fingerprint"
  pct exec "$VMID" -- systemctl stop ludus-admin ludus
  pct exec "$VMID" -- mv /opt/ludus/install/.bootstrap-complete /run/ludus-staged-bootstrap-complete
  pct push "$VMID" "$MIGRATION_DIR/state.tar.gz" /tmp/ludus-import.tar.gz
  pct exec "$VMID" -- /opt/ludus/ludus-server --import-state /tmp/ludus-import.tar.gz
  pct exec "$VMID" -- rm /tmp/ludus-import.tar.gz
  pct exec "$VMID" -- mv /run/ludus-staged-bootstrap-complete /opt/ludus/install/.bootstrap-complete
  migration_write_candidate_routes
}

migration_sync_final_state() {
  local current
  current=$(migration_static_fingerprint)
  [[ -f "$MIGRATION_DIR/static-fingerprint" ]] && [[ $current == "$(cat "$MIGRATION_DIR/static-fingerprint")" ]] || {
    echo "Ludus files changed after the LXC state was staged; retry migration" >&2
    return 1
  }
  python3 "$MIGRATION_DIR/migrate-cluster.py" final-state "$MIGRATION_DIR"
  pct push "$VMID" "$MIGRATION_DIR/final-state.tar.gz" /tmp/ludus-final-state.tar.gz
  pct exec "$VMID" -- systemctl stop wg-quick@wg0
  pct exec "$VMID" -- bash -euo pipefail -c '
    stage=$(mktemp -d /opt/ludus/.final-state.XXXXXX)
    cleanup() { rm -rf "$stage" /tmp/ludus-final-state.tar.gz; }
    trap cleanup EXIT
    tar -xzf /tmp/ludus-final-state.tar.gz -C "$stage"
    chown -R ludus:ludus "$stage/opt/ludus/db"
    chown -R root:root "$stage/etc/wireguard"
    if test -f "$stage/var/lib/misc/dnsmasq.leases"; then
      chown dnsmasq:nogroup "$stage/var/lib/misc/dnsmasq.leases"
    fi
    previous=$(mktemp -d /opt/ludus/.previous-db.XXXXXX)
    rmdir "$previous"
    mv /opt/ludus/db "$previous"
    mv "$stage/opt/ludus/db" /opt/ludus/db
    rm -rf "$previous"
    rm -rf /etc/wireguard
    mv "$stage/etc/wireguard" /etc/wireguard
    if test -f "$stage/var/lib/misc/dnsmasq.leases"; then
      install -m 0644 -o dnsmasq -g nogroup "$stage/var/lib/misc/dnsmasq.leases" /var/lib/misc/dnsmasq.leases
    fi
  '
  pct exec "$VMID" -- systemctl start wg-quick@wg0
}

migration_cutover() {
  if pgrep -f '(^|/)(ansible-playbook|packer)( |$)' >/dev/null; then
    echo "A deployment or build started during preflight; retry after it finishes" >&2
    return 1
  fi
  migration_check_sdn_pending
  python3 - "$MIGRATION_DIR" <<'PY'
import pathlib,sys
root=pathlib.Path(sys.argv[1])
saved_root=root/'staged-sdn' if (root/'sdn-staged').exists() else root/'sdn'
saved={p.name:p.read_bytes() for p in saved_root.glob('*.cfg')}
current={p.name:p.read_bytes() for p in pathlib.Path('/etc/pve/sdn').glob('*.cfg')}
if saved!=current:
    raise SystemExit('SDN configuration changed during preflight; retry migration in a maintenance window')
PY
  # The staged API contains only temporary first-boot state. Stop it before
  # redirecting clients so every queued request reaches the imported database.
  pct exec "$VMID" -- systemctl stop ludus-admin ludus
  migration_api_bridge start
  systemctl stop ludus ludus-admin
  "$MIGRATION_DIR/ludus-server" --migration-info >"$MIGRATION_DIR/cutover-info.json"
  python3 - "$MIGRATION_DIR" <<'PY'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1])
before=json.loads((root/'info.json').read_text())
after=json.loads((root/'cutover-info.json').read_text())
# The legacy vms table is a transient cache; even a read-only range poll can
# delete/repopulate it. Proxmox inventory includes stable identities and NICs.
for key in ('ranges','inventory','port','admin_port','expose_admin_port','wireguard_port','wireguard_endpoint','data_directory'):
    if before[key]!=after[key]:
        raise SystemExit('Installation changed during preflight; retry migration in a maintenance window')
PY
  migration_sync_final_state
  migration_forwarding wireguard
  migration_reset_wireguard_sessions
  systemctl stop wg-quick@wg0 dnsmasq
  touch "$MIGRATION_DIR/cutover"
  python3 "$MIGRATION_DIR/migrate-cluster.py" cutover "$MIGRATION_DIR"
  python3 "$MIGRATION_DIR/migrate-cluster.py" activate "$MIGRATION_DIR" "$VMID"
}

# Hold ordinary client TCP connections open while durable state moves into the
# candidate LXC. TLS remains end-to-end: this process only relays bytes and the
# migrated server continues presenting the original certificate. New connections
# are redirected to ready temporary listeners before the old API stops. Once
# DNAT is active, new connections bypass the bridge and existing relays drain.
migration_api_bridge_rules() {
  local action=$1
  [[ -f "$MIGRATION_DIR/api-bridge.json" ]] || return 0
  python3 - "$MIGRATION_DIR/api-bridge.json" "$action" <<'PY'
import json,subprocess,sys
mappings=json.load(open(sys.argv[1])); start=sys.argv[2]=='start'
rules=[]
for mapping in mappings:
    source=str(mapping['source']); listener=str(mapping['listener'])
    rules += [
        ['nat','PREROUTING','-m','addrtype','--dst-type','LOCAL','-p','tcp','--dport',source,'-j','REDIRECT','--to-ports',listener],
        ['nat','OUTPUT','-m','addrtype','--dst-type','LOCAL','-p','tcp','--dport',source,'-j','REDIRECT','--to-ports',listener],
        ['filter','INPUT','-p','tcp','--dport',listener,'-j','ACCEPT'],
    ]
for table,chain,*args in rules:
    base=['iptables','-w','-t',table]
    exists=subprocess.run(base+['-C',chain]+args,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0
    if start and not exists: subprocess.run(base+['-I',chain,'1']+args,check=True)
    if not start and exists: subprocess.run(base+['-D',chain]+args,check=True)
PY
}

migration_api_bridge() {
  local action=$1 pid=""
  [[ -f "$MIGRATION_DIR/api-bridge.pid" ]] && read -r pid <"$MIGRATION_DIR/api-bridge.pid"
  case "$action" in
    start)
      [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null || return 0
      rm -f "$MIGRATION_DIR/api-bridge.json"
      python3 - "$MIGRATION_DIR/network.json" "$MIGRATION_DIR/api-bridge.json" >"$MIGRATION_DIR/api-bridge.log" 2>&1 <<'PY' &
import asyncio,json,pathlib,signal,sys

c=json.load(open(sys.argv[1]))
target=c['ip'].split('/')[0]
ports=[int(c['port'])]
if c['expose_admin_port']:
    ports.append(int(c['admin_port']))
ready=pathlib.Path(sys.argv[2])

async def pipe(reader,writer):
    try:
        while data := await reader.read(65536):
            writer.write(data)
            await writer.drain()
    except (ConnectionError,OSError):
        pass
    finally:
        try:
            writer.write_eof()
            await writer.drain()
        except (ConnectionError,OSError):
            pass

async def relay(reader,writer,port):
    upstream_writer=None
    try:
        while upstream_writer is None:
            try:
                upstream_reader,upstream_writer=await asyncio.wait_for(
                    asyncio.open_connection(target,port),1)
            except (TimeoutError,ConnectionError,OSError):
                await asyncio.sleep(.1)
        await asyncio.gather(
            pipe(reader,upstream_writer),
            pipe(upstream_reader,writer),
        )
    finally:
        writer.close()
        if upstream_writer is not None:
            upstream_writer.close()

async def main():
    loop=asyncio.get_running_loop()
    stopping=asyncio.Event()
    active=set()
    servers=[]
    def launch(reader,writer,port):
        task=asyncio.create_task(relay(reader,writer,port))
        active.add(task)
        task.add_done_callback(active.discard)
    for sig in (signal.SIGTERM,signal.SIGINT):
        loop.add_signal_handler(sig,stopping.set)
    mappings=[]
    for port in ports:
        server=await asyncio.start_server(
            lambda reader,writer,port=port: launch(reader,writer,port),
            '0.0.0.0',0)
        servers.append(server)
        mappings.append({'source':port,'listener':server.sockets[0].getsockname()[1]})
    temporary=ready.with_suffix('.tmp')
    temporary.write_text(json.dumps(mappings))
    temporary.replace(ready)
    await stopping.wait()
    for server in servers:
        server.close()
    await asyncio.gather(*(server.wait_closed() for server in servers))
    if active:
        await asyncio.gather(*active,return_exceptions=True)

asyncio.run(main())
PY
      pid=$!
      printf '%s\n' "$pid" >"$MIGRATION_DIR/api-bridge.pid"
      for _ in $(seq 1 50); do
        [[ -f "$MIGRATION_DIR/api-bridge.json" ]] && break
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.1
      done
      if ! kill -0 "$pid" 2>/dev/null || [[ ! -f "$MIGRATION_DIR/api-bridge.json" ]]; then
        cat "$MIGRATION_DIR/api-bridge.log" >&2
        echo "Unable to preserve the existing API listener during migration" >&2
        return 1
      fi
      migration_api_bridge_rules start
      ;;
    drain)
      migration_api_bridge_rules stop
      [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null || kill -TERM "$pid"
      ;;
    stop)
      migration_api_bridge_rules stop
      if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        kill -TERM "$pid" 2>/dev/null || true
        for _ in $(seq 1 50); do
          kill -0 "$pid" 2>/dev/null || break
          sleep 0.1
        done
        kill -KILL "$pid" 2>/dev/null || true
      fi
      rm -f "$MIGRATION_DIR/api-bridge.pid" "$MIGRATION_DIR/api-bridge.json"
      ;;
    *) echo "Unknown API bridge action: $action" >&2; return 1 ;;
  esac
}

migration_move_nics() {
  python3 "$MIGRATION_DIR/migrate-cluster.py" move-nics "$MIGRATION_DIR" "$VMID"
}

migration_forwarding() {
  python3 - "$MIGRATION_DIR/network.json" "$1" <<'PY'
import json,subprocess,sys
c=json.load(open(sys.argv[1])); mode=sys.argv[2]; start=mode!='stop'; ip=c['ip'].split('/')[0]
rules=[['-t','raw','PREROUTING','-i',c['bridge'],'-s','127.0.0.0/8','-j','DROP'],['-t','nat','POSTROUTING','-s',ip+'/32','-o',c['uplink'],'-j','MASQUERADE'],['filter','FORWARD','-s',ip,'-j','ACCEPT'],['filter','FORWARD','-d',ip,'-m','conntrack','--ctstate','RELATED,ESTABLISHED','-j','ACCEPT'],['filter','INPUT','-s',ip,'-p','tcp','--dport','8006','-j','ACCEPT']]
if mode!='stage':
    rules += [['-t','nat','POSTROUTING','-s','192.0.2.0/24','-o',c['uplink'],'-j','MASQUERADE'],['filter','FORWARD','-i','ludusnat','-o',c['uplink'],'-j','ACCEPT'],['filter','FORWARD','-i',c['uplink'],'-o','ludusnat','-m','conntrack','--ctstate','RELATED,ESTABLISHED','-j','ACCEPT']]
    endpoints=[('udp',c['wireguard_port'])]
    if mode!='wireguard':
        endpoints.insert(0,('tcp',c['port']))
        if c['expose_admin_port']: endpoints.append(('tcp',c['admin_port']))
    for proto,port in endpoints:
        rules += [['-t','nat','PREROUTING','-m','addrtype','--dst-type','LOCAL','-p',proto,'--dport',str(port),'-j','DNAT','--to-destination',f'{ip}:{port}'],['-t','nat','OUTPUT','-m','addrtype','--dst-type','LOCAL','-p',proto,'--dport',str(port),'-j','DNAT','--to-destination',f'{ip}:{port}'],['-t','nat','POSTROUTING','-d',ip,'-p',proto,'--dport',str(port),'-j','MASQUERADE'],['filter','FORWARD','-d',ip,'-p',proto,'--dport',str(port),'-j','ACCEPT']]
for rule in rules:
    table,chain,args=(rule[1],rule[2],rule[3:]) if rule[0]=='-t' else (rule[0],rule[1],rule[2:])
    base=['iptables','-w','-t',table]
    exists=subprocess.run(base+['-C',chain]+args,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0
    if start and not exists: subprocess.run(base+['-I',chain,'1']+args,check=True)
    if not start and exists: subprocess.run(base+['-D',chain]+args,check=True)
if start:
    subprocess.run(['sysctl','-w','net.ipv4.ip_forward=1'],check=True,stdout=subprocess.DEVNULL)
    if mode!='stage':
        subprocess.run(['sysctl','-w',f"net.ipv4.conf.{c['bridge']}.route_localnet=1"],check=True,stdout=subprocess.DEVNULL)
PY
}

migration_reset_wireguard_sessions() {
  # Keepalives can retain pre-cutover NAT mappings indefinitely. Invalidate only
  # sessions to the original local WireGuard endpoints, never the whole table.
  python3 - "$MIGRATION_DIR/network.json" <<'PY'
import json,os,subprocess,sys
c=json.load(open(sys.argv[1]))
for address in sorted(set(c['host_addresses'])):
    result=subprocess.run(['conntrack','-D','-p','udp','--orig-dst',address,'--dport',str(c['wireguard_port'])],capture_output=True,text=True,env={**os.environ,'LC_ALL':'C'})
    if result.returncode and not (result.returncode==1 and '0 flow entries have been deleted' in result.stderr):
        raise SystemExit('Cannot reset previous WireGuard connection: '+result.stderr.strip())
PY
}

migration_commit() {
  install -d -m 0755 /usr/local/lib/ludus
  install -m 0700 "$MIGRATION_DIR/migrate-host.sh" /usr/local/lib/ludus/migrate-host.sh
  cat >/etc/systemd/system/ludus-lxc-forwarding.service <<EOF
[Unit]
Description=Preserve the host-installed Ludus API and WireGuard endpoints
Wants=network-online.target
After=network-online.target
Before=pve-guests.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/bash /usr/local/lib/ludus/migrate-host.sh --forward-start $MIGRATION_DIR
ExecStop=/bin/bash /usr/local/lib/ludus/migrate-host.sh --forward-stop $MIGRATION_DIR
[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now ludus-lxc-forwarding.service
  systemctl disable ludus ludus-admin wg-quick@wg0 dnsmasq
  touch "$MIGRATION_DIR/complete"
  MIGRATION_COMMITTED=1
  printf '[+] Migration committed. Original data and rollback state: %s\n' "$MIGRATION_DIR"
}

migration_rollback() {
  [[ -f "$MIGRATION_DIR/network.json" ]] || return 0
  echo "Restoring the host installation from $MIGRATION_DIR" >&2
  local restore_result=0
  migration_api_bridge stop || restore_result=1
  if [[ -f "$MIGRATION_DIR/container-created" ]]; then
    local id status
    if [[ ! -f "$MIGRATION_DIR/vmid" ]]; then
      echo "Rollback cannot identify the migration-owned container" >&2
      return 1
    fi
    read -r id <"$MIGRATION_DIR/vmid"
    [[ $id =~ ^[0-9]+$ ]] || { echo "Invalid migration-owned VMID" >&2; return 1; }
    # An already stopped candidate is safe; every other stop error is fatal.
    status=$(pct status "$id") || return 1
    if [[ $status != "status: stopped" ]]; then
      pct stop "$id" || { echo "Cannot stop candidate $id; refusing to reactivate duplicate host services" >&2; return 1; }
    fi
    pct set "$id" --onboot 0 || { echo "Cannot disable candidate $id autostart; rollback incomplete" >&2; return 1; }
    [[ $(pct status "$id") == "status: stopped" ]] || { echo "Candidate $id remains active; rollback incomplete" >&2; return 1; }
  fi
  if [[ -f /etc/systemd/system/ludus-lxc-forwarding.service ]]; then
    systemctl disable --now ludus-lxc-forwarding.service || restore_result=1
  fi
  python3 "$MIGRATION_DIR/migrate-cluster.py" rollback "$MIGRATION_DIR" || restore_result=$?
  migration_reset_wireguard_sessions || restore_result=1
  return "$restore_result"
}

migration_exit() {
  local result=$1
  trap - EXIT INT TERM
  if [[ ${MIGRATION_COMMITTED:-0} != 1 ]]; then
    set +e
    migration_rollback
    [[ $? == 0 ]] || echo "[!] Automatic rollback incomplete; use the saved network state before retrying" >&2
    [[ $result != 0 ]] || result=1
  fi
  exit "$result"
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  set -euo pipefail
  [[ $# == 2 && -d $2 ]] || { echo "Usage: $0 --rollback|--forward-start|--forward-stop MIGRATION_DIRECTORY" >&2; exit 1; }
  MIGRATION_DIR=$2
  case $1 in
    --rollback) migration_rollback ;;
    --forward-start) migration_forwarding start ;;
    --forward-stop) migration_forwarding stop ;;
    *) exit 1 ;;
  esac
fi
