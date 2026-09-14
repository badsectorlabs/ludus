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
  python3 - "$MIGRATION_DIR" "${LXC_BRIDGE:-}" "${ETH0_IP:-}" "${ETH0_GW:-}" "$LUDUS_NAT_GATEWAY" <<'PY'
import ipaddress,json,pathlib,re,shutil,subprocess,sys
root=pathlib.Path(sys.argv[1])
def run(*args):
    return subprocess.check_output(args,text=True)
def api(path,*args):
    return json.loads(run('pvesh','get',path,*args,'--output-format','json'))
info=json.loads((root/'info.json').read_text())
nodes=[n for n in api('/cluster/status') if n['type']=='node']
if len(nodes)!=1:
    raise SystemExit('Host network cutover requires a single-node install; do not change a clustered legacy network without a per-node migration plan')
node=nodes[0]['name']
for zone in api('/cluster/sdn/zones'):
    if zone['zone']=='ludus' and zone['type']!='simple':
        raise SystemExit('The existing ludus SDN zone is not simple; a per-node migration plan is required')
    if zone['zone']=='ludus' and (zone.get('ipam') not in (None,'pve') or zone.get('dns') or zone.get('reversedns')):
        raise SystemExit('The existing ludus SDN zone uses external IPAM/DNS; migration cannot safely roll back those services')
targets={'ludusnat', *(f"r{r['number']}" for r in info['ranges'])}
for vnet in api('/cluster/sdn/vnets'):
    if vnet['vnet'] in targets and vnet['zone']!='ludus':
        raise SystemExit(f"SDN target {vnet['vnet']} belongs to another zone; resolve the collision before migrating")
addresses=json.loads(run('ip','-j','-4','address'))
nat=[a['ifname'] for a in addresses for ip in a['addr_info'] if ip.get('local')=='192.0.2.254' and ip.get('prefixlen')==24]
if len(nat)!=1 or nat[0] not in ('vmbr1000','ludusnat'):
    raise SystemExit('Expected the existing Ludus NAT address 192.0.2.254/24 on vmbr1000 or ludusnat')
nat_gateway=sys.argv[5]
if any(a.get('local')==nat_gateway for interface in addresses for a in interface['addr_info']):
    raise SystemExit(f'NAT gateway {nat_gateway} is already assigned on the host')
def gateway_neighbour_exists():
    neighbours=json.loads(run('ip','-j','-4','neighbour','show','to',nat_gateway,'dev',nat[0]))
    return any(n.get('lladdr') and not {'FAILED','INCOMPLETE'}.intersection(n.get('state',[])) for n in neighbours)
if gateway_neighbour_exists():
    raise SystemExit(f'NAT gateway {nat_gateway} already has a neighbour on {nat[0]}')
probe=subprocess.run(['ping','-n','-I',nat[0],'-c','1','-W','2','-w','3',nat_gateway],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5)
if probe.returncode not in (0,1):
    raise SystemExit('Could not check the reserved NAT gateway address')
if probe.returncode==0 or gateway_neighbour_exists():
    raise SystemExit(f'NAT gateway {nat_gateway} is in use on {nat[0]}')
bridge,ip,gateway=sys.argv[2:5]
auto_management=not bridge and not ip and not gateway
if not auto_management and (not bridge or not ip or not gateway or ip=='dhcp'):
    raise SystemExit('Migration needs --bridge, static --ip and --gw together, or none for an automatic private management bridge')
routes=json.loads(run('ip','-j','-4','route','show','table','all'))
if auto_management:
    bridge='ludusmg'
    if any(a['ifname']==bridge for a in addresses) or pathlib.Path('/sys/class/net/'+bridge).exists():
        raise SystemExit('ludusmg already exists; supply an unused management bridge/address explicitly')
    occupied=[ipaddress.ip_network(r['dst'],strict=False) for r in routes if r.get('dst') not in (None,'default')]
    subnet=next((ipaddress.ip_network(f'172.31.{n}.0/30') for n in range(255,239,-1) if not any(ipaddress.ip_network(f'172.31.{n}.0/30').overlaps(x) for x in occupied)),None)
    if subnet is None:
        raise SystemExit('No unused private management subnet; supply --bridge, --ip and --gw')
    gateway=str(subnet[1]); ip=str(subnet[2])+'/30'
else:
    subnet=ipaddress.ip_interface(ip).network
    if subnet.overlaps(ipaddress.ip_network('192.0.2.0/24')) or subnet.overlaps(ipaddress.ip_network('198.51.100.0/24')):
        raise SystemExit('Management network must not overlap Ludus NAT or WireGuard networks')
    if any(a['ifname']==bridge and bridge==nat[0] for a in addresses):
        raise SystemExit('Management must not use the old Ludus NAT bridge')
uplink=next((r['dev'] for r in routes if r.get('dst')=='default' and 'dev' in r),None)
if not uplink:
    raise SystemExit('The host needs an IPv4 default route')
for path in ('/etc/systemd/system/ludus-lxc-forwarding.service','/usr/local/lib/ludus/migrate-host.sh'):
    if pathlib.Path(path).exists():
        raise SystemExit('A previous LXC migration forwarding installation exists; resolve it before migrating')
for name in ('interfaces','interfaces.d'):
    src=pathlib.Path('/etc/network')/name
    if src.is_dir(): shutil.copytree(src,root/name)
    elif src.exists(): shutil.copy2(src,root/name)
shutil.copytree('/etc/pve/sdn',root/'sdn',dirs_exist_ok=True)
sysctl_path=pathlib.Path('/etc/sysctl.d/99-ludus-ip-forward.conf')
if sysctl_path.exists(): shutil.copy2(sysctl_path,root/sysctl_path.name)
(root/'iptables.rules').write_text(run('iptables-save'))
services={}
for service in ('ludus','ludus-admin','wg-quick@wg0','dnsmasq'):
    services[service]={'active':subprocess.run(['systemctl','is-active','--quiet',service]).returncode==0,'enabled':subprocess.run(['systemctl','is-enabled','--quiet',service]).returncode==0}
bridges={nat[0]:'ludusnat', **{f"vmbr{1000+r['number']}":f"r{r['number']}" for r in info['ranges']}}
vms=[]
for vm in api('/cluster/resources','--type','vm'):
    if vm['type']!='qemu': continue
    config=api(f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config")
    old={}; new={}
    for key,value in config.items():
        if not re.fullmatch(r'net\d+',key): continue
        fields=value.split(',')
        replaced=[('bridge='+bridges[f[7:]]) if f.startswith('bridge=') and f[7:] in bridges else f for f in fields]
        if 'bridge='+nat[0] in fields:
            # The legacy NAT bridge used native VLAN 1. The NAT VNet is untagged.
            if any(f.startswith('trunks=') or (f.startswith('tag=') and f!='tag=1') for f in fields):
                raise SystemExit(f"VM {vm['vmid']} {key} uses non-native NAT VLANs; resolve them before migrating")
            replaced=[f for f in replaced if f!='tag=1']
        if replaced!=fields:
            old[key]=value; new[key]=','.join(replaced)
    if new: vms.append({'node':vm['node'],'vmid':vm['vmid'],'old':old,'new':new})
config={'node':node,'nat_bridge':nat[0],'bridge':bridge,'ip':ip,'gateway':gateway,'auto_management':auto_management,'subnet':str(subnet),'uplink':uplink,'services':services,'vms':vms,'ranges':info['ranges'],'port':info['port'],'admin_port':info['admin_port'],'expose_admin_port':info['expose_admin_port'],'wireguard_port':info['wireguard_port'],'ip_forward':pathlib.Path('/proc/sys/net/ipv4/ip_forward').read_text().strip()}
config['host_addresses']=[a['local'] for interface in addresses for a in interface.get('addr_info',[])]
if not auto_management:
    config['route_localnet']=pathlib.Path('/proc/sys/net/ipv4/conf',bridge,'route_localnet').read_text().strip()
(root/'network.json').write_text(json.dumps(config,indent=2))
PY
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

migration_cutover() {
  printf '%s\n' "$VMID" >"$MIGRATION_DIR/vmid"
  if pgrep -f '(^|/)(ansible-playbook|packer)( |$)' >/dev/null; then
    echo "A deployment or build started during preflight; retry after it finishes" >&2
    return 1
  fi
  migration_check_sdn_pending
  python3 - "$MIGRATION_DIR" <<'PY'
import pathlib,sys
root=pathlib.Path(sys.argv[1])
saved={p.name:p.read_bytes() for p in (root/'sdn').glob('*.cfg')}
current={p.name:p.read_bytes() for p in pathlib.Path('/etc/pve/sdn').glob('*.cfg')}
if saved!=current:
    raise SystemExit('SDN configuration changed during preflight; retry migration in a maintenance window')
PY
  systemctl stop ludus ludus-admin
  "$MIGRATION_DIR/ludus-server" --migration-info >"$MIGRATION_DIR/cutover-info.json"
  python3 - "$MIGRATION_DIR" <<'PY'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1])
before=json.loads((root/'info.json').read_text())
after=json.loads((root/'cutover-info.json').read_text())
for key in ('ranges','vms','port','admin_port','expose_admin_port','wireguard_port','wireguard_endpoint'):
    if before[key]!=after[key]:
        raise SystemExit('Installation changed during preflight; retry migration in a maintenance window')
PY
  "$MIGRATION_DIR/ludus-server" --export-state "$MIGRATION_DIR/state.tar.gz"
  IMPORT_DB="$MIGRATION_DIR/state.tar.gz"
  systemctl stop wg-quick@wg0 dnsmasq
  touch "$MIGRATION_DIR/cutover"
  python3 - "$MIGRATION_DIR/network.json" <<'PY'
import json,pathlib,re,subprocess,sys
c=json.load(open(sys.argv[1]))
# Keep bridge definitions until all NICs have moved. Remove only the old NAT
# address and Ludus range route hooks; never touch the management uplink.
paths=[pathlib.Path('/etc/network/interfaces'),*pathlib.Path('/etc/network/interfaces.d').glob('*')]
for path in paths:
    if not path.is_file() or path.name=='sdn': continue
    lines=path.read_text().splitlines(keepends=True); out=[]; interface=None
    for line in lines:
        m=re.match(r'\s*iface\s+(\S+)\s+inet\s+',line)
        if m: interface=m[1]
        if interface==c['nat_bridge']:
            if re.match(r'\s*address\s+192\.0\.2\.254(?:/24)?\s*$',line): continue
            if re.match(r'\s*netmask\s+255\.255\.255\.0\s*$',line): continue
            if m: line=re.sub(r'\binet static\b','inet manual',line)
        if any(re.search(r'\bip route (?:add|del|replace) 10\.'+str(r['number'])+r'\.0\.0/16\b',line) for r in c['ranges']): continue
        out.append(line)
    path.write_text(''.join(out))
subprocess.run(['ip','address','del','192.0.2.254/24','dev',c['nat_bridge']],check=True)
for r in c['ranges']:
    subprocess.run(['ip','route','del',f"10.{r['number']}.0.0/16"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
PY
}

migration_move_nics() {
  python3 - "$MIGRATION_DIR/network.json" <<'PY'
import json,subprocess,sys
c=json.load(open(sys.argv[1]))
for vm in c['vms']:
    args=['pvesh','set',f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config"]
    for key,value in vm['new'].items(): args += ['--'+key,value]
    subprocess.run(args,check=True,stdout=subprocess.DEVNULL)
    actual=json.loads(subprocess.check_output(['pvesh','get',f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config",'--current','1','--output-format','json']))
    for key,value in vm['new'].items():
        if sorted(actual[key].split(','))!=sorted(value.split(',')):
            raise SystemExit(f"VM {vm['vmid']} {key} did not apply its new bridge; refusing to commit migration")
PY
}

migration_forwarding() {
  python3 - "$MIGRATION_DIR/network.json" "$1" <<'PY'
import json,subprocess,sys
c=json.load(open(sys.argv[1])); start=sys.argv[2]=='start'; ip=c['ip'].split('/')[0]
rules=[['-t','raw','PREROUTING','-i',c['bridge'],'-s','127.0.0.0/8','-j','DROP'],['-t','nat','POSTROUTING','-s',ip+'/32','-o',c['uplink'],'-j','MASQUERADE'],['filter','FORWARD','-s',ip,'-j','ACCEPT'],['filter','FORWARD','-d',ip,'-m','conntrack','--ctstate','RELATED,ESTABLISHED','-j','ACCEPT'],['filter','INPUT','-s',ip,'-p','tcp','--dport','8006','-j','ACCEPT']]
endpoints=[('tcp',c['port']),('udp',c['wireguard_port'])]
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
    subprocess.run(['sysctl','-w','net.ipv4.ip_forward=1',f"net.ipv4.conf.{c['bridge']}.route_localnet=1"],check=True,stdout=subprocess.DEVNULL)
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
  if [[ -f "$MIGRATION_DIR/vmid" ]]; then
    local id
    read -r id <"$MIGRATION_DIR/vmid"
    if [[ -f "$MIGRATION_DIR/container-created" ]]; then
      pct stop "$id" || true
      pct set "$id" --onboot 0 || true
    fi
  fi
  systemctl disable --now ludus-lxc-forwarding.service 2>/dev/null || true
  local restore_result=0
  python3 - "$MIGRATION_DIR" <<'PY' || restore_result=$?
import json,pathlib,shutil,subprocess,sys
root=pathlib.Path(sys.argv[1]); c=json.loads((root/'network.json').read_text()); errors=[]
def run(args):
    result=subprocess.run(args,stdout=subprocess.DEVNULL)
    if result.returncode: errors.append(' '.join(args[:5]))
if (root/'cutover').exists():
    for vm in c['vms']:
        args=['pvesh','set',f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config"]
        for key,value in vm['old'].items(): args+=['--'+key,value]
        run(args)
    # Restore only the section files this migration can change. Reapplying the
    # saved sections regenerates running state, including a reused Ludus zone.
    for name in ('zones.cfg','vnets.cfg','subnets.cfg'):
        path=pathlib.Path('/etc/pve/sdn')/name
        saved=root/'sdn'/name
        if saved.exists(): shutil.copy2(saved,path)
        elif path.exists(): path.unlink()
    # Subnet gateway updates also mutate PVE IPAM outside the section files.
    # Restore only this NAT subnet; leave allocations in other zones untouched.
    ipam_path=pathlib.Path('/etc/pve/sdn/pve-ipam-state.json')
    if ipam_path.exists():
        ipam=json.loads(ipam_path.read_text() or '{}')
        saved_path=root/'sdn'/ipam_path.name
        saved=json.loads(saved_path.read_text() or '{}') if saved_path.exists() else {}
        saved_zone=saved.get('zones',{}).get('ludus',{})
        old_subnet=saved_zone.get('subnets',{}).get('192.0.2.0/24')
        zone=ipam.get('zones',{}).get('ludus')
        if old_subnet is not None:
            ipam.setdefault('zones',{}).setdefault('ludus',{}).setdefault('subnets',{})['192.0.2.0/24']=old_subnet
        elif zone is not None:
            zone.get('subnets',{}).pop('192.0.2.0/24',None)
            if not zone.get('subnets') and not saved_zone: ipam['zones'].pop('ludus')
        ipam_path.write_text(json.dumps(ipam))
    run(['pvesh','set','/cluster/sdn'])
if c['auto_management'] and pathlib.Path('/sys/class/net',c['bridge']).exists():
    run(['ifdown',c['bridge']])
shutil.copy2(root/'interfaces','/etc/network/interfaces')
for name in ('ludus-migration','sdn'):
    path=pathlib.Path('/etc/network/interfaces.d')/name
    if path.exists() and not (root/'interfaces.d'/name).exists(): path.unlink()
if (root/'interfaces.d').exists(): shutil.copytree(root/'interfaces.d','/etc/network/interfaces.d',dirs_exist_ok=True)
run(['ifreload','-a'])
if c['auto_management']:
    if pathlib.Path('/sys/class/net',c['bridge']).exists(): run(['ip','link','delete',c['bridge']])
else:
    run(['sysctl','-w',f"net.ipv4.conf.{c['bridge']}.route_localnet={c['route_localnet']}"])
sysctl_path=pathlib.Path('/etc/sysctl.d/99-ludus-ip-forward.conf')
if (root/sysctl_path.name).exists(): shutil.copy2(root/sysctl_path.name,sysctl_path)
elif sysctl_path.exists(): sysctl_path.unlink()
run(['sysctl','-w',f"net.ipv4.ip_forward={c['ip_forward']}"])
for path in ('/etc/systemd/system/ludus-lxc-forwarding.service','/usr/local/lib/ludus/migrate-host.sh'):
    artifact=pathlib.Path(path)
    if artifact.exists(): artifact.unlink()
run(['systemctl','daemon-reload'])
if (root/'iptables.rules').exists():
    result=subprocess.run(['iptables-restore'],input=(root/'iptables.rules').read_bytes())
    if result.returncode: errors.append('iptables-restore')
for name,state in c['services'].items():
    run(['systemctl','enable' if state['enabled'] else 'disable',name])
    run(['systemctl','start' if state['active'] else 'stop',name])
if errors: raise SystemExit('Rollback needs attention: '+', '.join(errors))
PY
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
