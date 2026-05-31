#!/usr/bin/env bash
set -euo pipefail
# Creates the LXC with NO default route on eth0, asserts bootstrap completes
# and zero packets hit the egress drop counter.

VMID=${1:?vmid}
TMPL=${2:?template path}
NODE=$(hostname)
TOKEN_ID=${TOKEN_ID:?}
TOKEN_SECRET=${TOKEN_SECRET:?}

cleanup() {
  local rc=$?
  set +e
  pct stop "$VMID" --skiplock 1 2>/dev/null
  pct destroy "$VMID" --force 1 2>/dev/null
  nft delete table inet ludus-airgap-test 2>/dev/null
  ip link del ludus-airgap 2>/dev/null
  rm -f /tmp/cfg.yml
  rm -f "/var/lib/vz/template/cache/${TMPL_NAME}" 2>/dev/null || true
  exit "$rc"
}
trap cleanup EXIT INT TERM

# Isolated bridge with no uplink
if ! ip link show ludus-airgap &>/dev/null; then
  ip link add ludus-airgap type bridge
  ip addr add 172.31.255.1/24 dev ludus-airgap
  ip link set ludus-airgap up
fi
# Egress drop counter on the bridge
nft add table inet ludus-airgap-test 2>/dev/null || true
nft flush table inet ludus-airgap-test
nft add chain inet ludus-airgap-test fwd '{ type filter hook forward priority 0; }'
nft add rule inet ludus-airgap-test fwd iifname "ludus-airgap" counter drop

cp "$TMPL" /var/lib/vz/template/cache/
TMPL_NAME=$(basename "$TMPL")
pct create "$VMID" "local:vztmpl/${TMPL_NAME}" \
  --hostname ludus-test --unprivileged 1 --features nesting=1,keyctl=1 \
  --cores 2 --memory 2048 --rootfs local-lvm:10 \
  --net0 "name=eth0,bridge=ludus-airgap,ip=172.31.255.10/24" \
  --net1 "name=eth1,bridge=ludusnat,ip=192.0.2.253/24"
cat >> "/etc/pve/lxc/${VMID}.conf" <<EOF
lxc.cgroup2.devices.allow: c 10:200 rwm
lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file
EOF

# pveproxy binds :::8006, so the container reaches the API at the bridge IP.
# This keeps the air-gap test honest: only the directly-connected /24 is reachable.
PVE_API_IP=172.31.255.1

cat > /tmp/cfg.yml <<EOF
proxmox_endpoints: ["https://${PVE_API_IP}:8006"]
proxmox_token_id: ${TOKEN_ID}
proxmox_token_secret: ${TOKEN_SECRET}
proxmox_node: ${NODE}
proxmox_invalid_cert: true
sdn_zone: ludus
ludus_nat_interface: ludusnat
license_key: community
database_encryption_key: $(head -c 24 /dev/urandom | base64 | head -c 32)
EOF
pct start "$VMID"; sleep 5
pct push "$VMID" /tmp/cfg.yml /opt/ludus/config.yml --perms 0600
pct exec "$VMID" -- systemctl restart ludus

for _ in $(seq 1 60); do
  pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete && break
  sleep 5
done
pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete \
  || { pct exec "$VMID" -- tail -100 /opt/ludus/install/install.log; exit 1; }

pct exec "$VMID" -- systemctl is-active ludus

DROPPED=$(nft -j list chain inet ludus-airgap-test fwd \
  | python3 -c "import sys,json; d=json.load(sys.stdin); print(sum(e.get('counter',{}).get('packets',0) for r in d['nftables'] if 'rule' in r for e in r['rule'].get('expr',[]) if 'counter' in e and any('drop' in str(x) for x in r['rule'].get('expr',[]))))")
echo "Egress drop counter: ${DROPPED}"
[[ "$DROPPED" -eq 0 ]] || { echo "FAIL: container attempted internet egress"; exit 1; }

echo "PASS: air-gap bootstrap"
