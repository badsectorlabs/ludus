#!/usr/bin/env python3
"""Reversible Proxmox network cutover, using existing root cluster SSH trust."""
import base64
import ipaddress
import inspect
import json
import pathlib
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import time

SERVICES = ('ludus', 'ludus-admin', 'wg-quick@wg0', 'dnsmasq')
NETWORK = pathlib.Path('/etc/network')
FRAGMENT = 'interfaces.d/ludus-lxc-gateway'
ROUTE_HOOKS = ('if-up.d/sdn-routes', 'if-up.d/ludus-routes')


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs)


def api(path, *args):
    return json.loads(run('pvesh', 'get', path, *args, '--output-format', 'json'))


def call(node, owner, action, payload=None):
    request = json.dumps({'action': action, 'payload': payload or {}})
    if node['name'] == owner:
        return node_action(action, payload or {})
    # Proxmox provisions /root/.ssh/config, known_hosts and authorized_keys.
    # Never bypass host-key verification or fall back to password prompting.
    command = ['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10',
               '-o', 'StrictHostKeyChecking=yes', '-o', 'HostKeyAlias='+node['name']]
    known_hosts = pathlib.Path('/etc/pve/nodes', node['name'], 'ssh_known_hosts')
    if known_hosts.is_file():
        command += ['-o', 'UserKnownHostsFile='+str(known_hosts), '-o', 'GlobalKnownHostsFile=/dev/null']
    command += ['root@'+node['ip'], 'python3 -c '+shlex.quote(pathlib.Path(__file__).read_text())+' --node']
    return json.loads(run(*command, input=request))


def snapshot(allow_migration=False):
    for command in ('ip', 'ifreload', 'iptables-save', 'iptables-restore', 'systemctl', 'pvesh'):
        if not shutil.which(command):
            raise RuntimeError('Missing per-node prerequisite: '+command)
    if subprocess.run(['pgrep', '-f', '(^|/)(ansible-playbook|packer)( |$)'], stdout=subprocess.DEVNULL).returncode == 0:
        raise RuntimeError('A deployment or template build is running')
    paths = [NETWORK/'interfaces', *sorted((NETWORK/'interfaces.d').glob('*')), *(NETWORK/name for name in ROUTE_HOOKS)]
    files = {}
    for path in paths:
        if path.is_symlink():
            raise RuntimeError('Cannot safely snapshot symlinked network configuration: '+str(path))
        if path.is_file():
            files[str(path.relative_to(NETWORK))] = {
                'data': base64.b64encode(path.read_bytes()).decode(),
                'mode': path.stat().st_mode & 0o777,
            }
    if not allow_migration and (FRAGMENT in files or 'interfaces.d/ludus-migration' in files):
        raise RuntimeError('A previous migration network fragment exists')
    services = {}
    for name in SERVICES:
        state = subprocess.run(['systemctl', 'show', name, '--property=LoadState', '--value'], capture_output=True, text=True, check=True).stdout.strip()
        if state == 'not-found':
            continue
        services[name] = {
            'active': subprocess.run(['systemctl', 'is-active', '--quiet', name]).returncode == 0,
            'enablement': subprocess.run(['systemctl', 'is-enabled', name], capture_output=True, text=True).stdout.strip(),
        }
    return {'name': run('hostname', '-s').strip(), 'files': files, 'services': services,
            'addresses': json.loads(run('ip', '-j', '-4', 'address')),
            'routes': json.loads(run('ip', '-j', '-4', 'route', 'show', 'table', 'all')),
            'iptables': run('iptables-save'),
            'ip_forward': pathlib.Path('/proc/sys/net/ipv4/ip_forward').read_text().strip()}


def nic_bridge(value):
    return next((field[7:] for field in value.split(',') if field.startswith('bridge=')), None)


def verify_tap(vmid, key, value):
    """Follow firewall bridge/veth to the intended bridge on the VM's node."""
    # qm status has no JSON output on supported Proxmox releases.
    if run('qm', 'status', str(vmid)).strip() != 'status: running':
        return
    device = pathlib.Path('/sys/class/net', f'tap{vmid}i{key[3:]}')
    bridge = nic_bridge(value)
    master = device/'master'
    if not master.exists():
        raise RuntimeError(f'VM {vmid} {key} tap is detached')
    attached = master.resolve().name
    if attached.startswith('fwbr'):
        uplink = pathlib.Path('/sys/class/net', f'fwpr{vmid}p{key[3:]}', 'master')
        attached = uplink.resolve().name if uplink.exists() else None
    if attached != bridge:
        raise RuntimeError(f'VM {vmid} {key} tap belongs to {attached}, expected {bridge}')


def remove_legacy_hooks(text, nat_bridge, ranges):
    out = []
    interface = None
    for line in text.splitlines(keepends=True):
        match = re.match(r'\s*iface\s+(\S+)(?:\s|$)', line)
        if match:
            interface = match[1]
        if interface == nat_bridge:
            if re.match(r'\s*address\s+192\.0\.2\.254(?:/24)?\s*$', line):
                continue
            if re.match(r'\s*netmask\s+255\.255\.255\.0\s*$', line):
                continue
            if match:
                line = re.sub(r'\binet static\b', 'inet manual', line)
        if any(re.search(r'\bip route (?:add|del|replace) 10\.'+str(r['number'])+r'\.0\.0/16\b', line) for r in ranges):
            continue
        out.append(line)
    return ''.join(out)


def remove_legacy_route_blocks(text, ranges):
    for r in ranges:
        marker = '# LUDUS MANAGED BLOCK FOR RANGE '+str(r['number'])
        text = re.sub(r'^'+re.escape(marker)+r' BEGIN\r?\n.*?^'+re.escape(marker)+r' END(?:\r?\n|$)', '', text, flags=re.M | re.S)
    return text


def probe_address(interface, address):
    """Probe the wire, not stale neighbor entries left by a rolled-back owner."""
    import socket
    import struct
    mac = bytes.fromhex(pathlib.Path('/sys/class/net', interface, 'address').read_text().strip().replace(':', ''))
    target = socket.inet_aton(address)
    header = struct.pack('!HHBB', 1, 0x0800, 6, 4)
    packet = b'\xff'*6 + mac + b'\x08\x06' + header + b'\x00\x01'
    # An ARP probe works even on cluster peers without an IPv4 NAT address.
    packet += mac + b'\x00'*4 + b'\x00'*6 + target
    with socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(0x0806)) as probe:
        probe.bind((interface, 0))
        for _ in range(3):
            probe.send(packet)
            deadline = time.monotonic()+1
            while time.monotonic() < deadline:
                probe.settimeout(max(0.001, deadline-time.monotonic()))
                try:
                    reply = probe.recv(2048)
                except socket.timeout:
                    break
                if (len(reply) >= 42 and reply[12:20] == b'\x08\x06'+header
                        and reply[20:22] in (b'\x00\x01', b'\x00\x02')
                        and reply[28:32] == target):
                    raise RuntimeError('Reserved NAT gateway is already in use')


def node_action(action, p):
    if action == 'snapshot':
        return snapshot(p.get('allow_migration', False))
    if action == 'probe':
        addresses = json.loads(run('ip', '-j', '-4', 'address'))
        if any(a.get('local') == p['gateway'] for link in addresses for a in link['addr_info']):
            raise RuntimeError('Reserved NAT gateway is assigned on this node')
        bridge = p['bridge']
        if pathlib.Path('/sys/class/net', bridge).exists():
            probe_address(bridge, p['gateway'])
        return True
    if action == 'verify-nics':
        for vm in p['vms']:
            for key, value in vm[p['direction']].items():
                verify_tap(vm['vmid'], key, value)
        return True
    if action == 'cutover':
        for path in [NETWORK/'interfaces', *sorted((NETWORK/'interfaces.d').glob('*'))]:
            if path.is_file() and path.name not in ('sdn', 'ludus-lxc-gateway'):
                path.write_text(remove_legacy_hooks(path.read_text(), p['nat_bridge'], p['ranges']))
        for name in ROUTE_HOOKS:
            path = NETWORK/name
            if path.is_file():
                path.write_text(remove_legacy_route_blocks(path.read_text(), p['ranges']))
        addresses = json.loads(run('ip', '-j', '-4', 'address'))
        for link in addresses:
            for address in link['addr_info']:
                if address.get('local') == '192.0.2.254':
                    run('ip', 'address', 'del', '192.0.2.254/'+str(address['prefixlen']), 'dev', link['ifname'])
        if p['owner']:
            # Linux may delete same-subnet secondary addresses with the old
            # primary address when promote_secondaries is disabled.
            run('ip', 'address', 'replace', p['gateway']+'/24', 'dev', 'ludusnat')
        for r in p['ranges']:
            destination = f"10.{r['number']}.0.0/16"
            # Replace only a route captured as a legacy Ludus route.
            for route in p['saved']['routes']:
                if route.get('dst') == destination and route.get('dev') in (p['nat_bridge'], 'ludusnat'):
                    run('ip', 'route', 'replace', destination, 'via', '192.0.2.254', 'dev', 'ludusnat', 'onlink')
        # Persist routes only on nodes which originally routed these ranges.
        routes = [r for r in p['ranges'] if any(x.get('dst') == f"10.{r['number']}.0.0/16" and x.get('dev') in (p['nat_bridge'], 'ludusnat') for x in p['saved']['routes'])]
        fragment = NETWORK/FRAGMENT
        text = fragment.read_text() if fragment.exists() else 'iface ludusnat inet manual\n'
        for r in routes:
            text += f"\tpost-up ip route replace 10.{r['number']}.0.0/16 via 192.0.2.254 dev ludusnat onlink\n"
        if p['owner']:
            run('ip', 'route', 'replace', '198.51.100.0/24', 'via', '192.0.2.254', 'dev', 'ludusnat')
            text += '\tpost-up ip route replace 198.51.100.0/24 via 192.0.2.254 dev ludusnat\n'
        fragment.write_text(text)
        ensure_include()
        return True
    if action == 'gateway':
        fragment = NETWORK/FRAGMENT
        fragment.parent.mkdir(parents=True, exist_ok=True)
        fragment.write_text('iface ludusnat inet static\n\taddress '+p['gateway']+'/24\n')
        ensure_include()
        run('ip', 'address', 'replace', p['gateway']+'/24', 'dev', 'ludusnat')
        return True
    if action == 'verify-gateway':
        addresses = json.loads(run('ip', '-j', '-4', 'address'))
        assigned = [(n['ifname'], a.get('prefixlen')) for n in addresses for a in n['addr_info'] if a.get('local') == p['gateway']]
        if assigned != ([('ludusnat', 24)] if p['owner'] else []):
            raise RuntimeError('NAT gateway must exist only on the owning host')
        for bridge in p['bridges']:
            if not pathlib.Path('/sys/class/net', bridge, 'bridge').exists():
                raise RuntimeError('SDN bridge not active: '+bridge)
        return True
    if action == 'restore':
        errors = []
        saved = p['saved']
        for name in ('interfaces.d/ludus-migration', FRAGMENT, 'interfaces.d/sdn'):
            path = NETWORK/name
            if name not in saved['files'] and path.exists():
                path.unlink()
        for name, item in saved['files'].items():
            path = NETWORK/name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(base64.b64decode(item['data']))
            path.chmod(item['mode'])
        def restore(*args, **kwargs):
            result = subprocess.run(args, stdout=sys.stderr, **kwargs)
            if result.returncode:
                errors.append(' '.join(args))
        # ifreload does not remove addresses added with ip address replace.
        addresses = json.loads(run('ip', '-j', '-4', 'address'))
        for link in addresses:
            if any(a.get('local') == p['gateway'] for a in link['addr_info']):
                restore('ip', 'address', 'del', p['gateway']+'/24', 'dev', link['ifname'])
        for r in p['ranges']:
            destination = f"10.{r['number']}.0.0/16"
            current = json.loads(run('ip', '-j', '-4', 'route', 'show', destination))
            for route in current:
                if route.get('gateway') == '192.0.2.254' and route.get('dev') == 'ludusnat':
                    restore('ip', 'route', 'del', destination, 'via', '192.0.2.254', 'dev', 'ludusnat')
        current = json.loads(run('ip', '-j', '-4', 'route', 'show', '198.51.100.0/24'))
        if any(r.get('gateway') == '192.0.2.254' and r.get('dev') == 'ludusnat' for r in current):
            restore('ip', 'route', 'del', '198.51.100.0/24', 'via', '192.0.2.254', 'dev', 'ludusnat')
        restore('ifreload', '-a')
        if p.get('management') and pathlib.Path('/sys/class/net', p['management']).exists():
            restore('ip', 'link', 'delete', p['management'])
        for route in saved['routes']:
            if route.get('dst') in {f"10.{r['number']}.0.0/16" for r in p['ranges']} and route.get('gateway') and route.get('dev'):
                args = ['ip', 'route', 'replace', route['dst'], 'via', route['gateway'], 'dev', route['dev']]
                if route.get('table') not in (None, 'main', 254):
                    args += ['table', str(route['table'])]
                restore(*args)
        restore('sysctl', '-w', 'net.ipv4.ip_forward='+saved['ip_forward'])
        restore('iptables-restore', input=saved['iptables'], text=True)
        for service, state in saved['services'].items():
            if state['enablement'] in ('enabled', 'disabled', 'masked'):
                restore('systemctl', {'enabled': 'enable', 'disabled': 'disable', 'masked': 'mask'}[state['enablement']], service)
            restore('systemctl', 'start' if state['active'] else 'stop', service)
        for link in saved['addresses']:
            if any(a.get('local') == '192.0.2.254' for a in link['addr_info']):
                try:
                    announce(link['ifname'], '192.0.2.254')
                except OSError as error:
                    errors.append('Cannot announce restored NAT gateway: '+str(error))
        if errors:
            raise RuntimeError('Node rollback incomplete: '+', '.join(errors))
        return True
    raise RuntimeError('Unknown node action: '+action)


def ensure_include():
    path = NETWORK/'interfaces'
    text = path.read_text()
    if not re.search(r'^\s*(?:source\s+/etc/network/interfaces\.d/\*|source-directory\s+/etc/network/interfaces\.d/?)\s*$', text, re.M):
        path.write_text(text+'\nsource /etc/network/interfaces.d/*\n')


def cluster_nodes():
    status = api('/cluster/status')
    nodes = [n for n in status if n['type'] == 'node']
    clusters = [n for n in status if n['type'] == 'cluster']
    if not nodes or any(not n.get('online') for n in nodes):
        raise RuntimeError('All cluster nodes must be online before migration')
    if len(nodes) > 1 and (not clusters or not clusters[0].get('quorate')):
        raise RuntimeError('Cluster must have quorum before migration')
    return nodes


def prepare(root, bridge, address, gateway, nat_gateway):
    info = json.loads((root/'info.json').read_text())
    nodes = cluster_nodes()
    owner = run('hostname', '-s').strip()
    if owner not in {n['name'] for n in nodes}:
        raise RuntimeError('Cannot identify the owning Proxmox node')
    snapshots = {}
    for node in nodes:
        saved = call(node, owner, 'snapshot')
        if saved['name'] != node['name']:
            raise RuntimeError('Cluster SSH connected to the wrong node')
        if node['name'] != owner and any(saved['services'].get(s, {}).get('active') for s in ('ludus', 'ludus-admin', 'wg-quick@wg0')):
            raise RuntimeError('Ludus/WireGuard is active on another node; resolve split ownership before migrating')
        snapshots[node['name']] = saved
    local = snapshots[owner]
    nat = [(node, a['ifname']) for node, saved in snapshots.items() for a in saved['addresses'] for ip in a['addr_info'] if ip.get('local') == '192.0.2.254']
    if len(nat) != 1 or nat[0][0] != owner or nat[0][1] not in ('vmbr1000', 'ludusnat'):
        raise RuntimeError('The legacy NAT address must belong only to the Ludus owning host on vmbr1000 or ludusnat')
    nat_bridge = nat[0][1]
    for node in nodes:
        call(node, owner, 'probe', {'bridge': nat_bridge, 'gateway': nat_gateway})
    vnets = {v['vnet']: v for v in api('/cluster/sdn/vnets')}
    zone_name = vnets.get('ludusnat', {}).get('zone', 'ludus')
    zones = {z['zone']: z for z in api('/cluster/sdn/zones')}
    zone = zones.get(zone_name)
    if len(nodes) > 1 and (not zone or zone['type'] != 'vxlan'):
        raise RuntimeError('Cluster migration requires an existing working VXLAN zone')
    if zone:
        if zone['type'] not in ('simple', 'vxlan') or zone.get('ipam') not in (None, 'pve') or zone.get('dns') or zone.get('reversedns'):
            raise RuntimeError('The Ludus SDN zone uses unsupported routing or external IPAM/DNS')
        members = set(re.split(r'[,;\s]+', zone.get('nodes', ''))) - {''}
        if members and not {n['name'] for n in nodes}.issubset(members):
            raise RuntimeError('The Ludus zone must be enabled on every cluster node')
        if zone['type'] == 'vxlan':
            peers = set(re.split(r'[,;\s]+', zone.get('peers', ''))) - {''}
            if not {n['ip'] for n in nodes}.issubset(peers):
                raise RuntimeError('VXLAN peers must include every cluster node address')
    targets = {'ludusnat', *(f"r{r['number']}" for r in info['ranges'])}
    for name in targets:
        if name in vnets and vnets[name]['zone'] != zone_name:
            raise RuntimeError('SDN target belongs to another zone: '+name)
        if len(nodes) > 1 and name not in vnets:
            raise RuntimeError('Cluster migration requires existing VXLAN VNet: '+name)
    subnets = api('/cluster/sdn/vnets/ludusnat/subnets') if 'ludusnat' in vnets else []
    for subnet in subnets:
        if not subnet['subnet'].endswith('-192.0.2.0-24') or subnet.get('dhcp-range') or subnet.get('dhcp-dns-server'):
            raise RuntimeError('Unsupported NAT subnet or SDN DHCP; resolve before migration')
    auto = not bridge and not address and not gateway
    routes = local['routes']
    if auto:
        bridge = 'ludusmg'
        if any(a['ifname'] == bridge for a in local['addresses']):
            raise RuntimeError('ludusmg already exists')
        occupied = [ipaddress.ip_network(r['dst'], strict=False) for r in routes if r.get('dst') not in (None, 'default')]
        occupied += [ipaddress.ip_interface(a['local']+'/'+str(a['prefixlen'])).network for n in local['addresses'] for a in n['addr_info']]
        subnet = next((ipaddress.ip_network(f'172.31.{n}.0/30') for n in range(255, 239, -1) if not any(ipaddress.ip_network(f'172.31.{n}.0/30').overlaps(x) for x in occupied)), None)
        if subnet is None:
            raise RuntimeError('No unused management subnet; supply --bridge, --ip and --gw')
        gateway, address = str(subnet[1]), str(subnet[2])+'/30'
    else:
        if not bridge or not address or not gateway or address == 'dhcp':
            raise RuntimeError('Supply --bridge, static --ip and --gw together, or none')
        subnet = ipaddress.ip_interface(address).network
        if bridge == nat_bridge or any(subnet.overlaps(ipaddress.ip_network(x)) for x in ('192.0.2.0/24', '198.51.100.0/24')):
            raise RuntimeError('Management network must not overlap the Ludus networks')
    uplink = next((r['dev'] for r in routes if r.get('dst') == 'default' and 'dev' in r), None)
    if not uplink:
        raise RuntimeError('Owning host requires an IPv4 default route')
    for path in ('/etc/systemd/system/ludus-lxc-forwarding.service', '/usr/local/lib/ludus/migrate-host.sh'):
        if pathlib.Path(path).exists():
            raise RuntimeError('A previous migration forwarding installation exists')
    bridges = {'vmbr1000': 'ludusnat', **{f"vmbr{1000+r['number']}": f"r{r['number']}" for r in info['ranges']}}
    vms = []
    for vm in api('/cluster/resources', '--type', 'vm'):
        config = api(f"/nodes/{vm['node']}/{vm['type']}/{vm['vmid']}/config")
        old, new = {}, {}
        for key, value in config.items():
            if not re.fullmatch(r'net\d+', key) or nic_bridge(value) not in targets | bridges.keys():
                continue
            if vm['type'] != 'qemu':
                raise RuntimeError('Existing containers on Ludus bridges require a separate migration plan')
            fields = value.split(',')
            replaced = [('bridge='+bridges[f[7:]]) if f.startswith('bridge=') and f[7:] in bridges else f for f in fields]
            if nic_bridge(value) in (nat_bridge, 'vmbr1000', 'ludusnat'):
                if any(f.startswith('trunks=') or (f.startswith('tag=') and f != 'tag=1') for f in fields):
                    raise RuntimeError(f"VM {vm['vmid']} uses non-native NAT VLANs")
                replaced = [f for f in replaced if f != 'tag=1']
            old[key], new[key] = value, ','.join(replaced)
        if old:
            if config.get('lock'):
                raise RuntimeError(f"VM {vm['vmid']} is locked")
            vms.append({'node': vm['node'], 'vmid': vm['vmid'], 'old': old, 'new': new})
    c = {'node': owner, 'nodes': nodes, 'snapshots': snapshots, 'nat_bridge': nat_bridge, 'nat_gateway': nat_gateway,
         'zone': zone_name, 'zone_type': zone['type'] if zone else 'simple', 'vnets': vnets, 'subnets': subnets,
         'bridge': bridge, 'ip': address, 'gateway': gateway, 'auto_management': auto, 'subnet': str(subnet),
         'uplink': uplink, 'services': local['services'], 'vms': vms, 'ranges': info['ranges'],
         'ip_forward': local['ip_forward'], 'host_addresses': [a['local'] for n in local['addresses'] for a in n['addr_info']]}
    c.update({key: info[key] for key in ('port', 'admin_port', 'expose_admin_port', 'wireguard_port')})
    if not auto:
        c['route_localnet'] = pathlib.Path('/proc/sys/net/ipv4/conf', bridge, 'route_localnet').read_text().strip()
    for node in nodes:
        call(node, owner, 'verify-nics', {'vms': [vm for vm in vms if vm['node'] == node['name']], 'direction': 'old'})
    shutil.copytree('/etc/pve/sdn', root/'sdn', dirs_exist_ok=True)
    sysctl = pathlib.Path('/etc/sysctl.d/99-ludus-ip-forward.conf')
    if sysctl.exists():
        shutil.copy2(sysctl, root/sysctl.name)
    (root/'network.json').write_text(json.dumps(c, indent=2))


def stage(c):
    if not any(z['zone'] == c['zone'] for z in api('/cluster/sdn/zones')):
        run('pvesh', 'create', '/cluster/sdn/zones', '--zone', c['zone'], '--type', 'simple', '--ipam', 'pve')
    for name in ['ludusnat', *(f"r{r['number']}" for r in c['ranges'])]:
        if name not in c['vnets']:
            run('pvesh', 'create', '/cluster/sdn/vnets', '--vnet', name, '--zone', c['zone'], '--vlanaware', '1' if name != 'ludusnat' else '0')
        elif name != 'ludusnat' and not c['vnets'][name].get('vlanaware'):
            run('pvesh', 'set', '/cluster/sdn/vnets/'+name, '--vlanaware', '1')
    # A gateway in a VXLAN subnet is generated on every node. Keep IPAM/subnet
    # identity, but let the single owning host provide routing and SNAT instead.
    for subnet in c['subnets']:
        args = ['pvesh', 'set', '/cluster/sdn/vnets/ludusnat/subnets/'+subnet['subnet'], '--snat', '0']
        if subnet.get('gateway'):
            args += ['--delete', 'gateway']
        run(*args)
    run('pvesh', 'set', '/cluster/sdn')
    deadline = time.monotonic()+60
    while True:
        try:
            for node in c['nodes']:
                call(node, c['node'], 'verify-gateway', {'gateway': c['nat_gateway'], 'owner': False, 'bridges': ['ludusnat', *(f"r{r['number']}" for r in c['ranges'])]})
            break
        except (RuntimeError, subprocess.CalledProcessError):
            if time.monotonic() >= deadline:
                raise
            time.sleep(1)
    owner = next(n for n in c['nodes'] if n['name'] == c['node'])
    call(owner, c['node'], 'gateway', {'gateway': c['nat_gateway']})
    for node in c['nodes']:
        call(node, c['node'], 'verify-gateway', {'gateway': c['nat_gateway'], 'owner': node['name'] == c['node'], 'bridges': ['ludusnat']})


def move_nics(c, direction):
    errors = []
    inventory = {v['vmid']: v['node'] for v in api('/cluster/resources', '--type', 'vm')}
    for vm in c['vms']:
        try:
            if inventory.get(vm['vmid']) != vm['node']:
                raise RuntimeError(f"VM {vm['vmid']} moved nodes during migration")
            args = ['pvesh', 'set', f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config"]
            actual = api(f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config", '--current', '1')
            for key, value in vm[direction].items():
                if sorted(actual[key].split(',')) != sorted(value.split(',')):
                    args += ['--'+key, value]
            if len(args) > 3:
                run(*args)
            actual = api(f"/nodes/{vm['node']}/qemu/{vm['vmid']}/config", '--current', '1')
            for key, value in vm[direction].items():
                if sorted(actual[key].split(',')) != sorted(value.split(',')):
                    raise RuntimeError(f"VM {vm['vmid']} {key} configuration did not apply")
            node = next(n for n in c['nodes'] if n['name'] == vm['node'])
            call(node, c['node'], 'verify-nics', {'vms': [vm], 'direction': direction})
        except (RuntimeError, subprocess.CalledProcessError) as e:
            if direction == 'new':
                raise
            errors.append(str(e))
    if errors:
        raise RuntimeError('NIC rollback incomplete: '+', '.join(errors))


def restore_sdn(root):
    for name in ('zones.cfg', 'vnets.cfg', 'subnets.cfg'):
        path = pathlib.Path('/etc/pve/sdn')/name
        saved = root/'sdn'/name
        if saved.exists():
            shutil.copy2(saved, path)
        elif path.exists():
            path.unlink()
    c = json.loads((root/'network.json').read_text())
    ipam_path = pathlib.Path('/etc/pve/sdn/pve-ipam-state.json')
    if ipam_path.exists():
        ipam = json.loads(ipam_path.read_text() or '{}')
        saved_path = root/'sdn'/ipam_path.name
        saved = json.loads(saved_path.read_text() or '{}') if saved_path.exists() else {}
        saved_zone = saved.get('zones', {}).get(c['zone'], {})
        old = saved_zone.get('subnets', {}).get('192.0.2.0/24')
        zone = ipam.get('zones', {}).get(c['zone'])
        if old is not None:
            ipam.setdefault('zones', {}).setdefault(c['zone'], {}).setdefault('subnets', {})['192.0.2.0/24'] = old
        elif zone is not None:
            zone.get('subnets', {}).pop('192.0.2.0/24', None)
            if not zone.get('subnets') and not saved_zone:
                ipam['zones'].pop(c['zone'])
        ipam_path.write_text(json.dumps(ipam))
    run('pvesh', 'set', '/cluster/sdn')


def rollback(root, c):
    errors = []
    def attempt(fn, *args):
        try:
            fn(*args)
        except (RuntimeError, OSError, subprocess.CalledProcessError) as e:
            errors.append(str(e))
    # Restore NICs while both old and staged bridges still exist.
    if (root/'cutover').exists():
        attempt(move_nics, c, 'old')
    if (root/'sdn-changed').exists():
        attempt(restore_sdn, root)
    for node in c['nodes']:
        attempt(call, node, c['node'], 'restore', {'saved': c['snapshots'][node['name']], 'ranges': c['ranges'], 'gateway': c['nat_gateway'], 'management': c['bridge'] if node['name'] == c['node'] and c['auto_management'] else None})
    if not c['auto_management']:
        attempt(run, 'sysctl', '-w', f"net.ipv4.conf.{c['bridge']}.route_localnet={c['route_localnet']}")
    sysctl = pathlib.Path('/etc/sysctl.d/99-ludus-ip-forward.conf')
    if (root/sysctl.name).exists():
        shutil.copy2(root/sysctl.name, sysctl)
    elif sysctl.exists():
        sysctl.unlink()
    for name in ('/etc/systemd/system/ludus-lxc-forwarding.service', '/usr/local/lib/ludus/migrate-host.sh'):
        path = pathlib.Path(name)
        if path.exists():
            path.unlink()
    attempt(run, 'systemctl', 'daemon-reload')
    if errors:
        raise RuntimeError('Rollback needs attention: '+', '.join(errors))


def final_state(root, wireguard=pathlib.Path('/etc/wireguard'), leases=pathlib.Path('/var/lib/misc/dnsmasq.leases')):
    before = json.loads((root/'info.json').read_text())
    after = json.loads((root/'cutover-info.json').read_text())
    source = pathlib.Path(before['data_directory'])
    if before['data_directory'] != after['data_directory'] or not source.is_absolute() or not source.is_dir() or source.resolve() != source:
        raise RuntimeError('Original data directory changed or became unavailable before final synchronization')
    with tarfile.open(root/'final-state.tar.gz', 'w:gz') as archive:
        archive.add(source, arcname='opt/ludus/db')
        archive.add(wireguard, arcname='etc/wireguard')
        if leases.is_file():
            archive.add(leases, arcname='var/lib/misc/dnsmasq.leases')


def announce(interface, address):
    import pathlib
    import socket
    import struct
    mac = bytes.fromhex(pathlib.Path('/sys/class/net', interface, 'address').read_text().strip().replace(':', ''))
    ip = socket.inet_aton(address)
    with socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(0x0806)) as sender:
        sender.bind((interface, 0))
        for operation in (1, 2):
            packet = b'\xff'*6 + mac + b'\x08\x06'
            packet += struct.pack('!HHBBH', 1, 0x0800, 6, 4, operation)
            packet += mac + ip + (b'\x00'*6 if operation == 1 else b'\xff'*6) + ip
            sender.send(packet)


def announce_candidate(vmid):
    run('pct', 'exec', vmid, '--', 'python3', '-c', inspect.getsource(announce)+"\nannounce('eth1', '192.0.2.254')\n")


def main():
    if sys.argv[1] == '--node':
        request = json.load(sys.stdin)
        print(json.dumps(node_action(request['action'], request['payload'])))
        return
    action, directory = sys.argv[1:3]
    root = pathlib.Path(directory)
    if action == 'prepare':
        prepare(root, *sys.argv[3:])
        return
    if action == 'final-state':
        final_state(root)
        return
    c = json.loads((root/'network.json').read_text())
    if action == 'stage':
        stage(c)
        c['staged_files'] = {node['name']: call(node, c['node'], 'snapshot', {'allow_migration': True})['files'] for node in c['nodes']}
        (root/'network.json').write_text(json.dumps(c, indent=2))
    elif action == 'cutover':
        nodes = cluster_nodes()
        if {(n['name'], n['ip']) for n in nodes} != {(n['name'], n['ip']) for n in c['nodes']}:
            raise RuntimeError('Cluster membership changed during migration')
        for node in c['nodes']:
            current = call(node, c['node'], 'snapshot', {'allow_migration': True})
            if current['files'] != c['staged_files'][node['name']]:
                raise RuntimeError('Node network configuration changed during staging: '+node['name'])
        for node in c['nodes']:
            call(node, c['node'], 'cutover', {'saved': c['snapshots'][node['name']], 'nat_bridge': c['nat_bridge'], 'ranges': c['ranges'], 'owner': node['name'] == c['node'], 'gateway': c['nat_gateway']})
    elif action == 'activate':
        vmid = sys.argv[3]
        path = f"/nodes/{c['node']}/lxc/{vmid}/config"
        config = api(path)
        fields = [field for field in config['net1'].split(',') if not field.startswith('link_down=')]
        run('pvesh', 'set', path, '--net1', ','.join(fields))
        run('pct', 'exec', vmid, '--', 'ip', 'link', 'set', 'eth1', 'up')
        announce_candidate(vmid)
    elif action == 'move-nics':
        move_nics(c, 'new')
        announce_candidate(sys.argv[3])
    elif action == 'rollback':
        rollback(root, c)
    else:
        raise RuntimeError('Unknown migration action: '+action)


if __name__ == '__main__':
    main()
