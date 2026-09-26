#!/usr/bin/env bash
set -euo pipefail

# Run the LXC bootstrap/provisioning test inside a nested Proxmox node.
# By default this uses the CI cluster runtime VMID from base.sh/README (1005).

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LXC_TEMPLATE=${LXC_TEMPLATE:-${1:-}}
TEST_VMID=${TEST_VMID:-9320}
NESTED_PVE_VMID=${NESTED_PVE_VMID:-1005}
NESTED_PVE_HOST=${NESTED_PVE_HOST:-}
NESTED_PVE_USER=${NESTED_PVE_USER:-gitlab-runner}
NESTED_PVE_SSH_CONFIG=${NESTED_PVE_SSH_CONFIG:-/home/gitlab-runner/.ssh/config}
REMOTE_DIR=${REMOTE_DIR:-/tmp/ludus-lxc-test}
TOKEN_ID=${TOKEN_ID:-}
TOKEN_SECRET=${TOKEN_SECRET:-}
LXC_ROOTFS_STORAGE=${LXC_ROOTFS_STORAGE:-local-lvm}
LUDUS_NAT_BRIDGE=${LUDUS_NAT_BRIDGE:-ludusnat}
NESTED_PVE_IP_REGEX=${NESTED_PVE_IP_REGEX:-^203\\.0\\.113\\.}

if [[ -z "$LXC_TEMPLATE" || ! -f "$LXC_TEMPLATE" ]]; then
  echo "Usage: LXC_TEMPLATE=/path/to/ludus-<version>-debian13-amd64.tar.zst $0"
  echo "       or: $0 /path/to/ludus-<version>-debian13-amd64.tar.zst"
  exit 1
fi

discover_nested_host() {
  local vmid=$1
  local ip

  command -v qm >/dev/null 2>&1 || return 1
  command -v jq >/dev/null 2>&1 || return 1
  for _ in $(seq 1 30); do
    ip=$(qm guest cmd "$vmid" network-get-interfaces 2>/dev/null \
      | jq -r 'if type == "array" then . else (.result // .data.result // []) end | .[]? | ."ip-addresses"[]? | ."ip-address"? // empty' \
      | grep -E "$NESTED_PVE_IP_REGEX" \
      | head -n1 || true)
    if [[ -n "$ip" ]]; then
      echo "$ip"
      return 0
    fi
    sleep 5
  done
  return 1
}

if [[ -z "$NESTED_PVE_HOST" ]]; then
  NESTED_PVE_HOST=$(discover_nested_host "$NESTED_PVE_VMID" || true)
fi

if [[ -z "$NESTED_PVE_HOST" ]]; then
  echo "Unable to discover nested Proxmox host VMID ${NESTED_PVE_VMID}."
  echo "Run ludus-server/ci/ci-vm-setup.yml first or set NESTED_PVE_HOST."
  exit 1
fi

SSH_OPTS=(-o BatchMode=yes -o StrictHostKeyChecking=no)
if [[ -f "$NESTED_PVE_SSH_CONFIG" ]]; then
  SSH_OPTS=(-F "$NESTED_PVE_SSH_CONFIG")
fi
SSH_TARGET="${NESTED_PVE_USER}@${NESTED_PVE_HOST}"
TEMPLATE_NAME=$(basename "$LXC_TEMPLATE")

echo "Using nested Proxmox ${SSH_TARGET}"
ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "mkdir -p '$REMOTE_DIR'"
scp "${SSH_OPTS[@]}" "$SCRIPT_DIR/test-lxc-airgap.sh" "$LXC_TEMPLATE" "$SSH_TARGET:$REMOTE_DIR/"

quote() {
  printf "%q" "$1"
}

ssh "${SSH_OPTS[@]}" "$SSH_TARGET" \
  "REMOTE_DIR=$(quote "$REMOTE_DIR") TEST_VMID=$(quote "$TEST_VMID") TEMPLATE_NAME=$(quote "$TEMPLATE_NAME") TOKEN_ID=$(quote "$TOKEN_ID") TOKEN_SECRET=$(quote "$TOKEN_SECRET") LXC_ROOTFS_STORAGE=$(quote "$LXC_ROOTFS_STORAGE") LUDUS_NAT_BRIDGE=$(quote "$LUDUS_NAT_BRIDGE") bash -s" <<'REMOTE'
set -euo pipefail

run_root() {
  if [[ "$(id -u)" == "0" ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

run_root pveversion >/dev/null

generated_token=0
if [[ -z "${TOKEN_ID}" || -z "${TOKEN_SECRET}" ]]; then
  token_name="ludus-lxc-test"
  run_root pveum user token remove root@pam "$token_name" >/dev/null 2>&1 || true
  token_json=$(run_root pveum user token add root@pam "$token_name" --privsep 0 --output-format json)
  TOKEN_ID=$(echo "$token_json" | python3 -c 'import sys,json; print(json.load(sys.stdin)["full-tokenid"])')
  TOKEN_SECRET=$(echo "$token_json" | python3 -c 'import sys,json; print(json.load(sys.stdin)["value"])')
  generated_token=1
fi

cleanup_token() {
  if [[ "$generated_token" == "1" ]]; then
    run_root pveum user token remove root@pam ludus-lxc-test >/dev/null 2>&1 || true
  fi
}
trap cleanup_token EXIT

run_root chmod +x "${REMOTE_DIR}/test-lxc-airgap.sh"
run_root env \
  TOKEN_ID="$TOKEN_ID" \
  TOKEN_SECRET="$TOKEN_SECRET" \
  LXC_ROOTFS_STORAGE="$LXC_ROOTFS_STORAGE" \
  LUDUS_NAT_BRIDGE="$LUDUS_NAT_BRIDGE" \
  RUN_USER_TEST=1 \
  RUN_RANGE_TEST=1 \
  "${REMOTE_DIR}/test-lxc-airgap.sh" "$TEST_VMID" "${REMOTE_DIR}/${TEMPLATE_NAME}"
REMOTE
