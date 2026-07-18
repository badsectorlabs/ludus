#!/usr/bin/env bash
set -euo pipefail
# Creates the LXC with NO default route on eth0, asserts bootstrap completes
# and zero packets hit the egress drop counter.

VMID=${1:?vmid}
TMPL=${2:?template path}
NODE=$(hostname)
TOKEN_ID=${TOKEN_ID:?}
TOKEN_SECRET=${TOKEN_SECRET:?}
RUN_USER_TEST=${RUN_USER_TEST:-0}
RUN_RANGE_TEST=${RUN_RANGE_TEST:-0}
TEST_USER_ID=${TEST_USER_ID:-UT}
TEST_USER_NAME=${TEST_USER_NAME:-User Test}
TEST_USER_EMAIL=${TEST_USER_EMAIL:-user-test@ludus.internal}
TEST_USER_PASSWORD=${TEST_USER_PASSWORD:-user-test-password}
TEST_PROXMOX_USERNAME=${TEST_PROXMOX_USERNAME:-user-test}
TEST_RANGE_ID=${TEST_RANGE_ID:-UTR}
TEST_RANGE_NAME=${TEST_RANGE_NAME:-User Test Range}
TEST_RANGE_NUMBER=${TEST_RANGE_NUMBER:-42}
LXC_ROOTFS_STORAGE=${LXC_ROOTFS_STORAGE:-local-lvm}
LUDUS_NAT_BRIDGE=${LUDUS_NAT_BRIDGE:-ludusnat}
TMP_PREFIX="/tmp/ludus-lxc-test-${VMID}-"

if [[ "$RUN_RANGE_TEST" == "1" ]]; then
  RUN_USER_TEST=1
fi

cleanup() {
  local rc=$?
  set +e
  pct stop "$VMID" --skiplock 1 2>/dev/null
  pct destroy "$VMID" --force 1 2>/dev/null
  nft delete table inet ludus-airgap-test 2>/dev/null
  ip link del ludus-airgap 2>/dev/null
  rm -f /tmp/cfg.yml
  rm -f "${TMP_PREFIX}"*.json 2>/dev/null || true
  rm -f "/var/lib/vz/template/cache/${TMPL_NAME:-}" 2>/dev/null || true
  exit "$rc"
}
trap cleanup EXIT INT TERM

api_post_in_lxc() {
  local api_key=$1
  local path=$2
  local payload=$3
  local expected_status=$4
  local label=$5
  local status

  status=$(pct exec "$VMID" -- env LUDUS_TEST_API_KEY="$api_key" LUDUS_TEST_PATH="$path" LUDUS_TEST_PAYLOAD="$payload" bash -lc \
    'curl -skS -o /tmp/ludus-api-response.json -w "%{http_code}" \
      -H "X-API-KEY: ${LUDUS_TEST_API_KEY}" \
      -H "Content-Type: application/json" \
      --data-binary "@${LUDUS_TEST_PAYLOAD}" \
      "https://127.0.0.1:8081${LUDUS_TEST_PATH}"')

  if [[ "$status" != "$expected_status" ]]; then
    echo "FAIL: ${label} returned HTTP ${status}, expected ${expected_status}"
    pct exec "$VMID" -- cat /tmp/ludus-api-response.json || true
    exit 1
  fi
}

run_user_provision_test() {
  local root_api_key
  local local_payload
  local container_payload

  echo "Running user provisioning test"
  root_api_key=$(pct exec "$VMID" -- cat /opt/ludus/install/root-api-key)
  local_payload="${TMP_PREFIX}user.json"
  container_payload="/tmp/ludus-user.json"
  cat > "$local_payload" <<EOF
{"userID":"${TEST_USER_ID}","isAdmin":true,"password":"${TEST_USER_PASSWORD}","email":"${TEST_USER_EMAIL}","name":"${TEST_USER_NAME}"}
EOF
  pct push "$VMID" "$local_payload" "$container_payload" --perms 0600
  api_post_in_lxc "$root_api_key" "/api/v2/user" "$container_payload" 201 "user create"

  ADMIN_API_KEY=$(pct exec "$VMID" -- python3 -c 'import json; print(json.load(open("/tmp/ludus-api-response.json"))["apiKey"])')
  pct exec "$VMID" -- test -d "/opt/ludus/users/${TEST_PROXMOX_USERNAME}/.ansible"
  pct exec "$VMID" -- test -f "/etc/wireguard/${TEST_USER_ID}-client-private-key"
  pct exec "$VMID" -- test -f "/opt/ludus/ranges/${TEST_USER_ID}/range-config.yml"
  echo "PASS: user provisioning"
}

run_range_create_test() {
  local local_payload
  local container_payload
  local gateway

  echo "Running range creation test"
  local_payload="${TMP_PREFIX}range.json"
  container_payload="/tmp/ludus-range.json"
  cat > "$local_payload" <<EOF
{"rangeID":"${TEST_RANGE_ID}","name":"${TEST_RANGE_NAME}","rangeNumber":${TEST_RANGE_NUMBER},"userID":["${TEST_USER_ID}"]}
EOF
  pct push "$VMID" "$local_payload" "$container_payload" --perms 0600
  api_post_in_lxc "$ADMIN_API_KEY" "/api/v2/ranges/create" "$container_payload" 201 "range create"

  gateway="192.0.2.$((100 + TEST_RANGE_NUMBER))"
  pct exec "$VMID" -- grep -q "10.${TEST_RANGE_NUMBER}.0.0/16 via ${gateway}" /etc/network/if-up.d/ludus-routes
  pct exec "$VMID" -- bash -lc "ip route show 10.${TEST_RANGE_NUMBER}.0.0/16 | grep -q 'via ${gateway}'"
  echo "PASS: range creation"
}

# Isolated bridge with no uplink
if ! ip link show ludus-airgap &>/dev/null; then
  ip link add ludus-airgap type bridge
  ip addr add 172.31.255.1/24 dev ludus-airgap
  ip link set ludus-airgap up
fi
# Egress drop counter on the bridge
nft add table inet ludus-airgap-test 2>/dev/null || true
nft flush table inet ludus-airgap-test
nft add chain inet ludus-airgap-test ludus_forward '{ type filter hook forward priority 0; }'
nft add rule inet ludus-airgap-test ludus_forward iifname "ludus-airgap" counter drop

cp "$TMPL" /var/lib/vz/template/cache/
TMPL_NAME=$(basename "$TMPL")
pct create "$VMID" "local:vztmpl/${TMPL_NAME}" \
  --hostname ludus-test --unprivileged 1 --features nesting=1,keyctl=1 \
  --cores 2 --memory 2048 --rootfs "${LXC_ROOTFS_STORAGE}:10" \
  --net0 "name=eth0,bridge=ludus-airgap,ip=172.31.255.10/24" \
  --net1 "name=eth1,bridge=${LUDUS_NAT_BRIDGE},ip=192.0.2.253/24"
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
ludus_nat_interface: ${LUDUS_NAT_BRIDGE}
license_key: community
database_encryption_key: $(head -c 24 /dev/urandom | base64 | head -c 32)
EOF
pct start "$VMID"; sleep 5
pct push "$VMID" /tmp/cfg.yml /opt/ludus/config.yml --perms 0600
pct exec "$VMID" -- systemctl restart ludus-admin ludus

for _ in $(seq 1 60); do
  pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete && break
  sleep 5
done
pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete \
  || { pct exec "$VMID" -- tail -100 /opt/ludus/install/install.log; exit 1; }

pct exec "$VMID" -- systemctl is-active ludus
pct exec "$VMID" -- systemctl is-active ludus-admin

ADMIN_API_KEY=""
if [[ "$RUN_USER_TEST" == "1" ]]; then
  run_user_provision_test
fi
if [[ "$RUN_RANGE_TEST" == "1" ]]; then
  run_range_create_test
fi

DROPPED=$(nft -j list chain inet ludus-airgap-test ludus_forward \
  | python3 -c "import sys,json; d=json.load(sys.stdin); print(sum(e.get('counter',{}).get('packets',0) for r in d['nftables'] if 'rule' in r for e in r['rule'].get('expr',[]) if 'counter' in e and any('drop' in str(x) for x in r['rule'].get('expr',[]))))")
echo "Egress drop counter: ${DROPPED}"
[[ "$DROPPED" -eq 0 ]] || { echo "FAIL: container attempted internet egress"; exit 1; }

echo "PASS: air-gap bootstrap"
