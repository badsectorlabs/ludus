#!/usr/bin/env python3
"""Explicit, resumable provisioning of the independent LXC CI generation."""
import argparse
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time

HERE = Path(__file__).resolve().parent
OWNER = "ludus-ci:lxc-2.4"
CLUSTER_NAT_GATEWAY = "192.0.2.254"
# This safety boundary is deliberately independent of editable profile metadata.
ALLOWED = frozenset(range(2400, 2408)) | {2409, 2412}


def run(*args, capture=False, check=True, **kwargs):
    result = subprocess.run([str(a) for a in args], text=True, capture_output=capture,
                            check=check, **kwargs)
    return result.stdout.strip() if capture else result


def profile(name):
    clean_env = {key: value for key, value in os.environ.items()
                 if not key.startswith("CI_SEED_") and key not in {"CLUSTER_NODE1_VMID", "CLUSTER_NODE2_VMID", "BUILD_VMID"}}
    output = run("bash", "-ec", 'source "$1"; env -0', "bash", HERE / "ci-profile.sh",
                 env={**clean_env, "CUSTOM_ENV_LUDUS_CI_PROFILE": name,
                      "LUDUS_CI_PROFILE": name}, capture=True)
    return dict(value.split("=", 1) for value in output.split("\0") if "=" in value)


def fail(message):
    raise RuntimeError(message)


def config(vmid, node):
    values = json.loads(run("pvesh", "get", f"/nodes/{node}/qemu/{vmid}/config",
                            "--output-format", "json", capture=True))
    return {key: str(value) for key, value in values.items()}


class Provisioner:
    def __init__(self, args):
        self.args = args
        self.inventory = json.loads((HERE / "ci-provision-inventory.json").read_text())
        current, legacy = profile("lxc-2.4"), profile("host-2.3")
        for role, vm in self.inventory.items():
            vm["role"] = role
            vm["vmid"] = int(current[vm["id_var"]])
            vm["source_vmid"] = int(legacy[self.inventory[vm["source"]]["id_var"]])
            if vm["vmid"] not in ALLOWED:
                fail(f"Destination outside immutable allowlist: {vm['vmid']}")
        if len({v["vmid"] for v in self.inventory.values()}) != len(self.inventory):
            fail("Duplicate destination VMIDs")
        self.selected = [self.inventory[r] for r in args.roles.split(",")] if args.roles else list(self.inventory.values())
        self.network = json.loads(Path(args.network).read_text())
        self.prefix = int(self.network["prefix"])
        self.gateway = str(ipaddress.ip_address(self.network["gateway"]))
        self.dns = str(ipaddress.ip_address(self.network["dns"]))
        self.bridge = self.network["bridge"]
        if not re.fullmatch(r"[a-zA-Z0-9_.-]+", self.bridge):
            fail("Invalid outer bridge")
        addresses, macs = set(), set()
        for vm in self.inventory.values():
            net = self.network["vms"][vm["role"]]
            address = ipaddress.ip_interface(f"{net['ip']}/{self.prefix}")
            if address.version != 4 or ipaddress.ip_address(self.gateway) not in address.network:
                fail("VM IPv4 and gateway must share the configured subnet")
            if net["ip"] in addresses or net["mac"].lower() in macs:
                fail("Duplicate management IP or MAC")
            addresses.add(net["ip"])
            macs.add(net["mac"].lower())
            if not re.fullmatch(r"(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}", net["mac"]):
                fail("Invalid MAC")
            if not re.fullmatch(r"[a-zA-Z0-9_.-]+", net["interface"]):
                fail("Invalid management interface")
            vm["net"] = net
        self.state = Path(args.state_dir)
        self.state.mkdir(parents=True, exist_ok=True, mode=0o700)

    def owned(self, vm, mutable=False):
        if vm["vmid"] not in ALLOWED:
            fail("Refusing a protected or unknown VMID")
        cfg = config(vm["vmid"], self.args.node)
        try:
            description = json.loads(cfg.get("description", "{}"))
        except ValueError:
            description = {}
        if cfg.get("name") != vm["name"] or description.get("ci_owner") != OWNER or description.get("ci_role") != vm["role"]:
            fail(f"VM {vm['vmid']} is not owned by this inventory; refusing adoption/replacement")
        if mutable and cfg.get("template") == "1":
            fail(f"VM {vm['vmid']} is frozen; use a disposable CI clone, not in-place mutation")
        return cfg

    def qm(self, vm, action, *args):
        self.owned(vm, mutable=action not in {"template", "set"})
        return run("qm", action, vm["vmid"], *args)

    def guest(self, vm, script, capture=False):
        self.owned(vm, mutable=True)
        response = json.loads(run("qm", "guest", "exec", vm["vmid"], "--timeout", "1800", "--",
                                  "/usr/bin/env", "HOME=/root", "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
                                  "/bin/bash", "-euo", "pipefail", "-c", script, capture=True))
        if not response.get("exited") or response.get("exitcode") != 0:
            fail(f"Guest {vm['vmid']} failed: {response}")
        output = response.get("out-data", "")
        if not capture:
            print(output, end="", flush=True)
        return output.strip()

    def agent_wait(self, vm):
        self.owned(vm, mutable=True)
        for _ in range(180):
            result = run("qm", "guest", "cmd", vm["vmid"], "ping", check=False,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if result.returncode == 0:
                return
            time.sleep(2)
        fail(f"Guest agent timeout on {vm['vmid']}")

    def clone(self, vm):
        resources = json.loads(run("pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json", capture=True))
        if any(int(r["vmid"]) == vm["vmid"] for r in resources):
            self.owned(vm)
            print(f"Owned destination {vm['vmid']} already exists; retaining it")
            return
        source = config(vm["source_vmid"], self.args.node)
        if source.get("template") != "1":
            fail(f"Source {vm['source_vmid']} must already be a template; it will NOT be stopped or changed")
        description = json.dumps({"ci_owner": OWNER, "ci_role": vm["role"]})
        run("qm", "clone", vm["source_vmid"], vm["vmid"], "--full", "1", "--name", vm["name"],
            "--description", description, "--storage", self.args.storage, "--pool", self.args.pool)
        self.owned(vm)
        self.qm(vm, "set", "--protection", "1", "--onboot", "0", "--cpu", "host", "--memory", vm["memory"],
                "--cores", vm["cores"], "--agent", "enabled=1", "--net0", self.nic(vm, isolated=True))

    def nic(self, vm, isolated=False):
        return f"virtio={vm['net']['mac']},bridge={self.bridge},link_down={int(isolated)}"

    def network_vm(self, vm):
        cfg = self.owned(vm, mutable=True)
        if any(re.fullmatch(r"net[1-9][0-9]*", key) for key in cfg):
            fail("Refusing to boot a copy with additional, unisolated outer NICs")
        # Only a destination is shut down, and never force-stopped on timeout.
        status = run("qm", "status", vm["vmid"], capture=True)
        if "running" in status:
            self.qm(vm, "shutdown", "--timeout", "180")
        self.qm(vm, "set", "--net0", self.nic(vm, isolated=True), "--agent", "enabled=1",
                "--onboot", "0", "--protection", "1", "--cpu", "host",
                "--memory", vm["memory"], "--cores", vm["cores"])
        self.qm(vm, "start")
        self.agent_wait(vm)
        net = vm["net"]
        # Preserve bridge ports/options and unrelated internal range networks.
        payload = json.dumps({"interface": net["interface"], "ip": net["ip"], "prefix": self.prefix,
                              "gateway": self.gateway, "dns": self.dns, "hostname": vm.get("hostname")})
        script = "python3 - " + shlex.quote(payload) + " <<'PY'\n" + '''import json,pathlib,re,socket,sys
n=json.loads(sys.argv[1]); p=pathlib.Path('/etc/network/interfaces'); lines=p.read_text().splitlines()
if pathlib.Path('/etc/pve/corosync.conf').exists(): raise RuntimeError('Refusing inherited cluster identity')
hostname=n['hostname'] or socket.gethostname()
hosts=pathlib.Path('/etc/hosts'); host_lines=hosts.read_text().splitlines()
old_addresses=set()
for line in host_lines:
    fields=line.split('#',1)[0].split()
    if len(fields)>1 and socket.gethostname() in fields[1:] and not fields[0].startswith('127.'):
        old_addresses.add(fields[0])
result=[]; active=False; found=False
for line in lines:
    if re.match(r'^iface\\s+'+re.escape(n['interface'])+r'\\s+inet\\s+',line):
        if found: raise RuntimeError('Duplicate management interface stanza')
        found=True; active=True
        result.extend(['iface '+n['interface']+' inet static', '    address '+n['ip']+'/'+str(n['prefix']), '    gateway '+n['gateway'], '    dns-nameservers '+n['dns']]); continue
    if active and re.match(r'^\\s*address\\s+',line):
        old_addresses.add(line.split()[1].split('/')[0])
    if re.match(r'^(iface|auto|allow-)\\s',line): active=False
    if active and re.match(r'^\\s*(address|netmask|gateway|dns-|dhcp-)',line): continue
    result.append(line)
if not found: raise RuntimeError('Management interface stanza absent; refusing guessed network configuration')
p.write_text('\\n'.join(result)+'\\n')
pathlib.Path('/etc/resolv.conf').write_text('nameserver '+n['dns']+'\\n')
pathlib.Path('/etc/hostname').write_text(hostname+'\\n')
host_lines=[line for line in host_lines if socket.gethostname() not in line.split('#',1)[0].split()[1:]]
hosts.write_text('\\n'.join(host_lines)+'\\n'+n['ip']+' '+hostname+'\\n')
# Only this copied host's old management address is rewritten. Keeping an old
# Proxmox endpoint here could otherwise direct migration at a protected source.
configuration=pathlib.Path('/opt/ludus/config.yml')
if configuration.exists():
    text=configuration.read_text()
    for old in old_addresses:
        text=re.sub(r'(?<![0-9.])'+re.escape(old)+r'(?![0-9.])',n['ip'],text)
    configuration.write_text(text)
PY
'''
        self.guest(vm, script)
        self.network_include(vm)
        self.qm(vm, "shutdown", "--timeout", "180")
        self.qm(vm, "set", "--net0", self.nic(vm))
        self.qm(vm, "start")
        self.agent_wait(vm)
        actual = json.loads(self.guest(vm, "ip -j addr", capture=True))
        if not any(a.get("local") == net["ip"] for interface in actual for a in interface.get("addr_info", [])):
            fail(f"Management IP did not apply on {vm['vmid']}")

    def ssh(self, vm, script):
        self.owned(vm, mutable=True)
        return run("ssh", "-F", self.args.ssh_config, f"gitlab-runner@{vm['net']['ip']}",
                   "sudo bash -euo pipefail -c " + shlex.quote(script))

    def copy(self, vm, source, destination):
        self.owned(vm, mutable=True)
        run("scp", "-F", self.args.ssh_config, source, f"gitlab-runner@{vm['net']['ip']}:{destination}")

    def ansible(self, vms, playbook, cluster=False):
        hosts = {}
        for vm in vms:
            self.owned(vm, mutable=True)
            hosts[vm.get("hostname", vm["name"])] = {
                "ansible_host": vm["net"]["ip"], "ansible_user": "gitlab-runner",
                "ansible_ssh_common_args": "-F " + shlex.quote(self.args.ssh_config),
                "ansible_become": True, "ansible_python_interpreter": "/usr/bin/python3",
                "ci_vmid": vm["vmid"], "pve_cluster_addr0": vm["net"]["ip"]}
        group = "ci_pve_cluster" if cluster else "ci_nested"
        inventory = {"all": {"children": {group: {"hosts": hosts}}}}
        if cluster:
            primary = self.inventory["cluster-node1"]["hostname"]
            inventory["all"]["children"]["ci_pve_mds_primary"] = {"hosts": {primary: {}}}
        with tempfile.TemporaryDirectory(prefix="ci-24-ansible-") as tmp:
            path = Path(tmp) / "inventory.json"
            path.write_text(json.dumps(inventory))
            extra = {"ci_cluster_name": "ludus-ci-24", "ci_cluster_network": str(ipaddress.ip_interface(f"{self.gateway}/{self.prefix}").network),
                     "ci_cluster_ceph_osd_disk_size": self.args.osd_size, "ci_cluster_ceph_pg_num": self.args.ceph_pgs}
            if cluster:
                extra["ci_cluster_mds_group"] = "ci_pve_mds_primary"
            run("ansible-playbook", "-i", path, HERE / playbook, "--extra-vars", json.dumps(extra),
                "--skip-tags", "ci-cluster-snapshot")

    def nested(self):
        vms = [v for v in self.selected if v["kind"] in {"base", "cluster"}]
        if not vms:
            fail("nested requires base or cluster roles")
        for vm in vms:
            self.guest(vm, "test ! -e /etc/pve/corosync.conf; test ! -e /opt/ludus/config.yml")
        self.ansible(vms, "ci-nested-setup.yml")

    def network_include(self, vm):
        # SDN creates interfaces.d/sdn. Legacy seeds do not necessarily include
        # that directory, so an API apply alone cannot activate the new VNets.
        self.guest(vm, "python3 - " + shlex.quote(self.args.lxc_bridge) + """ <<'PY'
import pathlib,re,sys
p=pathlib.Path('/etc/network/interfaces')
lines=p.read_text().splitlines()
lines=[line for line in lines if line.strip() != 'source /etc/network/interfaces.d/'+sys.argv[1]]
if not any(re.fullmatch(r'\\s*source(?:-directory)?\\s+/etc/network/interfaces\\.d(?:/\\*|/)?\\s*', line) for line in lines):
    lines.extend(['', 'source /etc/network/interfaces.d/*'])
p.write_text('\\n'.join(lines)+'\\n')
PY
""")

    def lxc_network(self, vm):
        if vm["kind"] == "legacy":
            fail("The legacy migration fixture must retain its legacy network")
        self.network_include(vm)
        bridge = self.args.lxc_bridge
        subnet = ipaddress.ip_interface(self.args.lxc_ip).network
        gateway = self.args.lxc_gateway
        interface = vm["net"]["interface"]
        content = f"""# {OWNER}
auto {bridge}
iface {bridge} inet static
    address {gateway}/{subnet.prefixlen}
    bridge-ports none
    bridge-stp off
    bridge-fd 0
    post-up iptables -t nat -C POSTROUTING -s {subnet} -o {interface} -j MASQUERADE || iptables -t nat -A POSTROUTING -s {subnet} -o {interface} -j MASQUERADE
    post-down iptables -t nat -D POSTROUTING -s {subnet} -o {interface} -j MASQUERADE || true
"""
        path = f"/etc/network/interfaces.d/{bridge}"
        self.guest(vm, f"""command -v ifup iptables
install -d /etc/network/interfaces.d
if test -f {path}; then
    test "$(cat {path})" = {shlex.quote(content.strip())}
else
    if ip link show {bridge} >/dev/null 2>&1; then
        echo 'Refusing to adopt an existing management bridge' >&2
        exit 1
    fi
    printf '%s' {shlex.quote(content)} > {path}
fi
printf 'net.ipv4.ip_forward=1\\n' > /etc/sysctl.d/90-ludus-ci24-forwarding.conf
sysctl -p /etc/sysctl.d/90-ludus-ci24-forwarding.conf
if ! ip -4 addr show dev {bridge} 2>/dev/null | grep -Fq '{gateway}/{subnet.prefixlen}'; then
    ifup {bridge}
fi
ip -4 addr show dev {bridge} | grep -F '{gateway}/{subnet.prefixlen}'
iptables -t nat -C POSTROUTING -s {subnet} -o {interface} -j MASQUERADE
""")

    def migrate(self, vm):
        if vm["kind"] not in {"lxc", "build"}:
            fail(f"Role {vm['role']} must not be migrated")
        self.install_lxc(vm, migrate=True)

    def cluster_install(self):
        if {vm["role"] for vm in self.selected} != {"cluster-node1", "cluster-node2"}:
            fail("cluster-install requires both cluster roles")
        for vm in self.selected:
            self.cluster_health(vm)
        primary = self.inventory["cluster-node1"]
        # VXLAN zones provide L2 only; unlike simple zones, their subnet
        # gateway/SNAT settings do not configure host routing. Own it on node 1.
        self.network_include(primary)
        uplink = primary["net"]["interface"]
        content = f"""# {OWNER}
iface ludusnat inet static
    address {CLUSTER_NAT_GATEWAY}/24
    post-up iptables -t nat -C POSTROUTING -s 192.0.2.0/24 -o {uplink} -j MASQUERADE || iptables -t nat -A POSTROUTING -s 192.0.2.0/24 -o {uplink} -j MASQUERADE
    post-down iptables -t nat -D POSTROUTING -s 192.0.2.0/24 -o {uplink} -j MASQUERADE || true
"""
        path = "/etc/network/interfaces.d/ci24-cluster-nat"
        self.ssh(primary, f"""if test -f {path}; then
    test "$(cat {path})" = {shlex.quote(content.strip())}
else
    printf '%s' {shlex.quote(content)} > {path}
fi
""")
        self.install_lxc(primary, migrate=False)
        for vm in self.selected:
            self.validate(vm)

    def install_lxc(self, vm, migrate):
        for attribute in ("installer", "template", "server", "client", "version"):
            if not getattr(self.args, attribute):
                fail(f"LXC installation requires --{attribute}")
        for attribute in ("installer", "template", "server", "client"):
            if not Path(getattr(self.args, attribute)).is_file():
                fail(f"Missing artifact --{attribute}")
        stage = "/tmp/ludus-ci-24-artifacts"
        self.ssh(vm, f"install -d -o gitlab-runner -g gitlab-runner -m 0700 {stage}")
        media = {attribute: stage + "/" + Path(getattr(self.args, attribute)).name
                 for attribute in ("installer", "template", "server", "client")}
        if len(set(media.values())) != len(media):
            fail("Artifact basenames must be distinct")
        for attribute, destination in media.items():
            self.copy(vm, getattr(self.args, attribute), destination)
        ct = self.args.lxc_vmid
        endpoints = [f"https://{self.args.lxc_gateway}:8006"] if migrate else [
            f"https://{self.inventory[role]['net']['ip']}:8006" for role in ("cluster-node1", "cluster-node2")]
        flags = ["--no-prompt", "--server-only", "--version", self.args.version,
                 "--template-file", media["template"], "--vmid", str(ct), "--storage", self.args.lxc_storage,
                 "--bridge", self.args.lxc_bridge, "--ip", self.args.lxc_ip,
                 "--gw", self.args.lxc_gateway, "--nameserver", self.dns,
                 "--endpoints", " ".join(endpoints)]
        if migrate:
            flags.append("--migrate-host")
        else:
            flags.extend(["--vm-storage", "ceph", "--vm-storage-format", "raw", "--iso-storage", "cephfs"])
        if self.args.skip_verification:
            flags.append("--skip-verification")
        else:
            for option in ("checksum_file", "checksum_signature", "checksum_public_key"):
                path = getattr(self.args, option)
                if not path or not Path(path).is_file():
                    fail("Supply signed checksum inputs or explicitly --skip-verification for local CI artifacts")
                self.copy(vm, path, stage + "/" + Path(path).name)
                flags.extend(["--" + option.replace("_", "-"), stage + "/" + Path(path).name])
        if self.args.enterprise_plugin:
            self.copy(vm, self.args.enterprise_plugin, stage + "/enterprise-plugin")
            flags.extend(["--enterprise-plugin", stage + "/enterprise-plugin"])
        port = 8080 if migrate else 9090
        url = f"https://{ipaddress.ip_interface(self.args.lxc_ip).ip}:{port}"
        resume_check = "systemctl is-active --quiet ludus-lxc-forwarding" if migrate else f"pct exec {ct} -- grep -Eq '^port: *9090$' /opt/ludus/config.yml"
        # Migration preserves legacy hostname fields; normalize them while the
        # new services are stopped. Standalone job clones must reach their own
        # Proxmox host through the private bridge, never the stopped seed's IP.
        proxmox_host = self.args.lxc_gateway if migrate else vm["net"]["ip"]
        normalize = """import json,pathlib,sys,yaml
p=pathlib.Path('/opt/ludus/config.yml')
c=yaml.safe_load(p.read_text())
c['proxmox_endpoints']=json.loads(sys.argv[1])
c['proxmox_hostname']=sys.argv[2]
for key in ('proxmox_url','proxmox_public_ip','proxmox_local_ip'):
    c.pop(key,None)
p.write_text(yaml.safe_dump(c,sort_keys=False))
"""
        normalize_command = shlex.join(["pct", "exec", str(ct), "--", "/opt/ludus/venv/bin/python3",
                                        "-c", normalize, json.dumps(endpoints), proxmox_host])
        self.ssh(vm, f'''if ! pct status {ct} >/dev/null 2>&1; then
    LUDUS_API_PORT={port} LUDUS_NAT_GATEWAY={CLUSTER_NAT_GATEWAY} bash {shlex.quote(media["installer"])} {shlex.join(flags)}
else
    pct exec {ct} -- test -f /opt/ludus/install/.bootstrap-complete
    {resume_check}
fi
pct exec {ct} -- systemctl stop ludus-admin ludus
{normalize_command}
staged=$(pct exec {ct} -- mktemp /tmp/ludus-ci-update.XXXXXX)
trap 'pct exec {ct} -- rm -f "$staged"' EXIT
pct push {ct} {shlex.quote(media["server"])} "$staged" --perms 0700
pct exec {ct} -- env HOME=/root TMPDIR=/tmp PATH=/opt/ludus/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin LANG=C.UTF-8 LC_ALL=C.UTF-8 "$staged" --update
pct push {ct} {shlex.quote(media["client"])} /usr/local/bin/ludus --perms 0755
install -m 0755 {shlex.quote(media["client"])} /usr/local/bin/ludus
pct exec {ct} -- systemctl start ludus-admin ludus
export LUDUS_API_KEY=$(pct exec {ct} -- cat /opt/ludus/install/root-api-key)
for attempt in $(seq 1 120); do
    if curl -fkSs --max-time 10 -H "X-API-KEY: $LUDUS_API_KEY" {shlex.quote(url + "/api/v2/user/all")} | jq -e 'type == "array"' >/dev/null; then exit 0; fi
    sleep 2
done
exit 1
''')
        self.validate(vm)

    def build(self, vm):
        if vm["kind"] != "build":
            fail("build requires the build role")
        self.ssh(vm, """export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y build-essential make git git-lfs curl jq rsync xz-utils zstd debootstrap squashfs-tools libpcap-dev libssl-dev pkg-config python3-venv python3-pip python3-debian ansible-core dab unzip zip rclone ca-certificates openssl
if ! node --version 2>/dev/null | grep -Eq '^v22\\.'; then
    curl -fsSL https://deb.nodesource.com/setup_22.x -o /tmp/ludus-ci24-nodesource.sh
    bash /tmp/ludus-ci24-nodesource.sh
    apt-get install -y nodejs
fi
npm install --global yarn
su - gitlab-runner -c 'curl -fsSL https://bun.sh/install | bash'
ln -sfn /home/gitlab-runner/.bun/bin/bun /usr/local/bin/bun
git lfs install --system
/usr/local/go/bin/go version
""")
        self.build_health(vm)

    def build_health(self, vm):
        self.guest(vm, "/usr/local/go/bin/go version; gcc --version; git lfs version; node --version | grep -E '^v22[.]'; npm --version; yarn --version; sudo -u gitlab-runner /home/gitlab-runner/.bun/bin/bun --version; ansible-playbook --version; rclone version; command -v dab make unzip debootstrap zstd rsync openssl")

    def cluster_health(self, vm):
        self.guest(vm, "pvesh get /cluster/status --output-format json | jq -e '[.[] | select(.type == \"node\" and .online == 1)] | length == 2'; ceph health | grep -qx HEALTH_OK; pvesm status --storage ceph; pvesm status --storage cephfs")

    def lxc_health(self, vm, port):
        if not self.args.version:
            fail("LXC validation requires --version")
        ct = self.args.lxc_vmid
        url = f"https://{ipaddress.ip_interface(self.args.lxc_ip).ip}:{port}"
        if vm["kind"] != "cluster":
            check_platform = """import sys,yaml
c=yaml.safe_load(open('/opt/ludus/config.yml'))
assert c['proxmox_endpoints']==['https://'+sys.argv[1]+':8006'], 'non-cloneable Proxmox endpoints'
assert c['proxmox_hostname']==sys.argv[1], 'non-cloneable Proxmox hostname'
"""
            self.guest(vm, shlex.join(["pct", "exec", str(ct), "--", "/opt/ludus/venv/bin/python3",
                                      "-c", check_platform, self.args.lxc_gateway]))
        self.guest(vm, f'''pct exec {ct} -- test -f /opt/ludus/install/.bootstrap-complete
pct exec {ct} -- systemctl is-active --quiet ludus ludus-admin
export LUDUS_API_KEY=$(pct exec {ct} -- cat /opt/ludus/install/root-api-key)
pct exec {ct} -- /opt/ludus/ludus-server --version | grep -F -- {shlex.quote(self.args.version)}
curl -fkSs --max-time 10 -H "X-API-KEY: $LUDUS_API_KEY" {shlex.quote(url + "/api/v2/user/all")} | jq -e 'type == "array"'
''')

    def cluster(self):
        vms = [v for v in self.selected if v["kind"] == "cluster"]
        if len(vms) != 2:
            fail("cluster requires both cluster-node1 and cluster-node2")
        for vm in vms:
            cfg = self.owned(vm, mutable=True)
            if "virtio1" not in cfg:
                self.qm(vm, "shutdown", "--timeout", "180")
                self.qm(vm, "set", "--virtio1", f"{self.args.storage}:{self.args.osd_size.rstrip('Gg')}")
                self.qm(vm, "start")
                self.agent_wait(vm)
        self.ansible(vms, "ci-cluster-setup.yml", cluster=True)
        for vm in vms:
            self.cluster_health(vm)

    def validate(self, vm):
        cfg = self.owned(vm)
        receipt = self.state / f"{vm['vmid']}.json"
        if cfg.get("template") == "1":
            if not receipt.is_file() or json.loads(receipt.read_text()).get("version") != self.args.version:
                fail(f"Frozen VM {vm['vmid']} lacks a matching readiness receipt")
            print(f"Frozen seed {vm['vmid']} retained with matching readiness receipt")
            return
        self.guest(vm, "systemctl is-active --quiet qemu-guest-agent; test -x /usr/bin/sudo")
        kind = vm["kind"]
        if kind == "base":
            self.guest(vm, "pveversion; pvesh get /version --output-format json; test ! -e /etc/pve/corosync.conf; test ! -e /opt/ludus/config.yml; pvesh get /cluster/resources --type vm --output-format json | jq -e 'all(.[]; .type != \"lxc\")'")
            self.guest(vm, f"ip -4 addr show dev {self.args.lxc_bridge} | grep -F '{self.args.lxc_gateway}/{ipaddress.ip_interface(self.args.lxc_ip).network.prefixlen}'; iptables -t nat -C POSTROUTING -s {ipaddress.ip_interface(self.args.lxc_ip).network} -o {vm['net']['interface']} -j MASQUERADE")
        elif kind == "cluster":
            self.cluster_health(vm)
            primary = self.inventory["cluster-node1"]
            self.lxc_health(primary, 9090)
            self.guest(primary, f"pct exec {self.args.lxc_vmid} -- grep -Eq '^port: *9090$' /opt/ludus/config.yml")
            self.guest(primary, f"ip -4 addr show dev ludusnat | grep -F '{CLUSTER_NAT_GATEWAY}/24'; iptables -t nat -C POSTROUTING -s 192.0.2.0/24 -o {primary['net']['interface']} -j MASQUERADE")
        elif kind == "legacy":
            self.guest(vm, """systemctl is-active --quiet ludus ludus-admin
test ! -e /etc/systemd/system/ludus-lxc-forwarding.service
export LUDUS_API_KEY=$(cat /opt/ludus/install/root-api-key)
curl -fkSs --max-time 10 -H "X-API-KEY: $LUDUS_API_KEY" https://127.0.0.1:8080/api/v2/user/all | jq -e 'type == "array"'
""")
        else:
            self.lxc_health(vm, 8080)
            self.guest(vm, "systemctl is-active --quiet ludus-lxc-forwarding")
            url = shlex.quote(f"https://{ipaddress.ip_interface(self.args.lxc_ip).ip}:8080")
            if vm["role"] in {"templates-built", "range-admin", "range-user", "integration"}:
                self.guest(vm, f"export LUDUS_API_KEY=$(cat /opt/ludus/ci/.apikey-admin); ludus --url {url} templates list --json | jq -e 'length > 0 and all(.[]; .built == true)'")
            if vm["role"] in {"range-admin", "range-user", "integration"}:
                key = "admin" if vm["role"] == "range-admin" else "user"
                self.guest(vm, f"export LUDUS_API_KEY=$(cat /opt/ludus/ci/.apikey-{key}); ludus --url {url} range list --json | jq -e '.rangeState == \"SUCCESS\"'")
            if kind == "build":
                self.build_health(vm)
        data = {"vmid": vm["vmid"], "role": vm["role"], "owner": OWNER, "version": self.args.version,
                "validated_at": int(time.time())}
        temporary = receipt.with_suffix(".tmp")
        temporary.write_text(json.dumps(data) + "\n")
        temporary.replace(receipt)

    def freeze(self, vm):
        cfg = self.owned(vm)
        self.validate(vm)
        if cfg.get("template") == "1":
            return
        if vm["kind"] in {"cluster", "build"}:
            snapshots = json.loads(run("pvesh", "get", f"/nodes/{self.args.node}/qemu/{vm['vmid']}/snapshot", "--output-format", "json", capture=True))
            if any(s["name"] == "clean" for s in snapshots):
                print(f"Existing clean snapshot on {vm['vmid']} retained; never silently replaced")
                return
            self.qm(vm, "snapshot", "clean", "--vmstate", "1")
        else:
            self.qm(vm, "shutdown", "--timeout", "180")
            if "stopped" not in run("qm", "status", vm["vmid"], capture=True):
                fail("Refusing template conversion of a running seed")
            self.qm(vm, "template")
        self.qm(vm, "set", "--protection", "1", "--onboot", "0")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["clone", "network", "nested", "lxc-network", "migrate", "build", "cluster", "cluster-install", "validate", "freeze"])
    parser.add_argument("--roles", help="Comma-separated inventory roles; defaults to all (use explicit roles for specialized phases)")
    parser.add_argument("--network", required=True, help="JSON: bridge,prefix,gateway,dns,vms:{role:{ip,mac,interface}}")
    parser.add_argument("--storage", default="nvme")
    parser.add_argument("--pool", default="CICD")
    parser.add_argument("--node", default=os.uname().nodename.split(".")[0])
    parser.add_argument("--ssh-config", default="/home/gitlab-runner/.ssh/config")
    parser.add_argument("--state-dir", default="/var/lib/ludus-ci/lxc-2.4")
    parser.add_argument("--lxc-vmid", type=int, default=999)
    parser.add_argument("--lxc-storage", default="local")
    parser.add_argument("--lxc-bridge", default="ci24mgmt")
    parser.add_argument("--lxc-ip", default="172.31.24.2/24")
    parser.add_argument("--lxc-gateway", default="172.31.24.1")
    parser.add_argument("--osd-size", default="100G")
    parser.add_argument("--ceph-pgs", type=int, default=32)
    for option in ("installer", "template", "server", "client", "version", "checksum-file", "checksum-signature", "checksum-public-key", "enterprise-plugin"):
        parser.add_argument("--" + option)
    parser.add_argument("--skip-verification", action="store_true", help="Explicitly trust locally built CI artifacts")
    args = parser.parse_args()
    if os.geteuid() != 0:
        fail("Run provisioning as root on the outer Proxmox host")
    if not re.fullmatch(r"[1-9][0-9]*[Gg]", args.osd_size):
        fail("--osd-size must be an integer GiB size, e.g. 100G")
    if not re.fullmatch(r"[a-zA-Z0-9_.-]+", args.lxc_bridge):
        fail("Invalid LXC bridge")
    lxc_address = ipaddress.ip_interface(args.lxc_ip)
    lxc_gateway = ipaddress.ip_address(args.lxc_gateway)
    if lxc_address.version != 4 or lxc_gateway not in lxc_address.network or lxc_address.ip == lxc_gateway:
        fail("LXC IPv4 and distinct gateway must share a subnet")
    if args.lxc_vmid < 100:
        fail("Invalid nested LXC VMID")
    provisioner = Provisioner(args)
    with (provisioner.state / "provision.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.phase in {"nested", "cluster", "cluster-install"}:
            getattr(provisioner, args.phase.replace("-", "_"))()
        else:
            method = getattr(provisioner, "network_vm" if args.phase == "network" else args.phase.replace("-", "_"))
            for vm in provisioner.selected:
                print(f"{args.phase}: {vm['vmid']} {vm['name']}", flush=True)
                method(vm)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        print(f"Provisioning refused/failed: {error}", file=sys.stderr)
        sys.exit(1)
