#!/usr/bin/env bash

# /opt/ludus/ci/prepare-cluster.sh
#
# Prepares cluster nodes (VMIDs 1005/1006) for cluster CI tests.
#
# Cluster slot acquisition is now done by the dedicated claim-cluster YAML
# job (which runs claim-cluster.sh and creates the lock dir). This script
# only handles the per-job rollback / take-snapshot / SSH-wait logic.

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

# Populate CLUSTER_NODE{1,2}_IP / CLUSTER_NODES via Proxmox API
discover_cluster_ips

NODE1=$CLUSTER_NODE1_VMID
NODE2=$CLUSTER_NODE2_VMID
CI_CLUSTER_DNS_SERVERS="${CI_CLUSTER_DNS_SERVERS:-$CI_CLONE_DNS_SERVERS}"
CI_CLUSTER_SDN_ZONE="${CI_CLUSTER_SDN_ZONE:-ludus}"

configure_cluster_dns() {
    local IP="$1"
    local HOST_EPOCH

    HOST_EPOCH=$(date -u +%s)

    for i in {1..30}; do
        if ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" \
            "sudo DNS_SERVERS='$CI_CLUSTER_DNS_SERVERS' HOST_EPOCH='$HOST_EPOCH' bash -s" <<'REMOTE'
set -euo pipefail

date -u -s "@${HOST_EPOCH}"
if command -v hwclock >/dev/null 2>&1; then
    hwclock --systohc --utc || true
elif [ -x /sbin/hwclock ]; then
    /sbin/hwclock --systohc --utc || true
fi

for dns_server in $DNS_SERVERS; do
    printf 'nameserver %s\n' "$dns_server"
done > /tmp/resolv.conf.ludus-ci

if [ -d /etc/resolvconf/resolv.conf.d ]; then
    cp /tmp/resolv.conf.ludus-ci /etc/resolvconf/resolv.conf.d/head
    resolvconf -u || true
fi

if ! grep -qE '^nameserver[[:space:]]+' /etc/resolv.conf 2>/dev/null; then
    cp /tmp/resolv.conf.ludus-ci /etc/resolv.conf
fi

if ! command -v chronyd >/dev/null 2>&1 || ! command -v chronyc >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y chrony
fi

if grep -qE '^iface ens18 inet static$' /etc/network/interfaces; then
    if grep -qE '^[[:space:]]*dns-nameservers[[:space:]]+' /etc/network/interfaces; then
        sed -i -E "s/^[[:space:]]*dns-nameservers[[:space:]]+.*/\tdns-nameservers ${DNS_SERVERS}/" /etc/network/interfaces
    else
        sed -i -E "/^[[:space:]]*gateway[[:space:]]+/a\\tdns-nameservers ${DNS_SERVERS}" /etc/network/interfaces
    fi
fi

rm -f /tmp/resolv.conf.ludus-ci
REMOTE
        then
            return 0
        fi
        sleep 5
    done

    echo "ERROR: Failed to configure DNS on cluster node $IP" >&2
    return 1
}

repair_cluster_cephfs() {
    local IP="$1"

    for i in {1..6}; do
        if ssh -o ConnectTimeout=10 -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" \
            "sudo bash -s" <<'REMOTE'
set -euo pipefail

if ! grep -qE '^cephfs:[[:space:]]+cephfs$' /etc/pve/storage.cfg 2>/dev/null; then
    exit 0
fi

if timeout 15 pvesm status --storage cephfs 2>/dev/null | awk '$1 == "cephfs" && $3 == "active" { found=1 } END { exit(found ? 0 : 1) }'; then
    exit 0
fi

timeout 10 umount -f -l /mnt/pve/cephfs 2>/dev/null || true
mkdir -p /mnt/pve/cephfs
systemctl restart pvestatd || true

timeout 30 pvesm status --storage cephfs 2>/dev/null | awk '$1 == "cephfs" && $3 == "active" { found=1 } END { exit(found ? 0 : 1) }'
REMOTE
        then
            return 0
        fi
        sleep 5
    done

    echo "ERROR: Failed to activate cephfs storage on cluster node $IP" >&2
    return 1
}

ensure_cluster_sdn_zone() {
    local PEERS="${CLUSTER_NODES// /,}"

    for i in {1..60}; do
        if ssh -o ConnectTimeout=10 -F /home/gitlab-runner/.ssh/config gitlab-runner@"$CLUSTER_PRIMARY" \
            "sudo SDN_ZONE='$CI_CLUSTER_SDN_ZONE' SDN_PEERS='$PEERS' bash -s" <<'REMOTE'
set -euo pipefail

if ! pvecm status 2>/dev/null | grep -qE '^Quorate:[[:space:]]+Yes$'; then
    echo "Proxmox cluster is not quorate yet"
    exit 1
fi

SDN_NODES=$(pvesh get /cluster/status --output-format=json | jq -r '[.[] | select(.type == "node") | .name] | sort | join(",")')
if [[ -z "$SDN_NODES" ]]; then
    echo "ERROR: no Proxmox cluster nodes found while configuring SDN zone" >&2
    exit 1
fi

if pvesh get /cluster/sdn/zones --output-format=json | jq -e --arg zone "$SDN_ZONE" 'any(.[]; .type == "vxlan" and .zone == $zone)' >/dev/null; then
    pvesh set "/cluster/sdn/zones/$SDN_ZONE" --peers "$SDN_PEERS" --nodes "$SDN_NODES"
else
    pvesh create /cluster/sdn/zones --zone "$SDN_ZONE" --type vxlan --peers "$SDN_PEERS" --nodes "$SDN_NODES"
fi

pvesh set /cluster/sdn
REMOTE
        then
            return 0
        fi
        sleep 5
    done

    echo "ERROR: Failed to configure cluster SDN zone $CI_CLUSTER_SDN_ZONE" >&2
    return 1
}

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "clean-cluster" ]]; then
    echo "Reverting cluster nodes to 'clean' snapshot"
    qm rollback "$NODE1" clean --start 1
    qm rollback "$NODE2" clean --start 1
    for IP in $CLUSTER_NODES; do
        for i in {1..90}; do
            ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" "echo ready" && break || sleep 5
        done
    done
elif [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "cluster-from-snapshot" ]]; then
    SNAP="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    if ! qm listsnapshot "$NODE1" | grep -q "$SNAP"; then
        echo "Snapshot $SNAP not found, falling back to full build"
        exit "${BUILD_FAILURE_EXIT_CODE:-1}"
    fi
    if [[ ! -f "/tmp/.ludus-ci-cluster-${PIPELINE_ID}-${SNAP}-rolled-back" ]]; then
        SNAPTIME=$(pvesh get "/nodes/$PROXMOX_NODE/qemu/$NODE1/snapshot" --output-format=json | jq --arg S "$SNAP" '.[] | select(.name==$S) | .snaptime')
        DIFF=$(( $(date +%s) - SNAPTIME ))
        if [[ "$DIFF" -gt 120 ]]; then
            echo "Rolling back cluster to $SNAP"
            qm rollback "$NODE1" "$SNAP" --start
            qm rollback "$NODE2" "$SNAP" --start
            for IP in $CLUSTER_NODES; do
                for i in {1..30}; do
                    ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" "echo ready" && break || sleep 5
                done
            done
        else
            echo "$SNAP snapshot is < 2 minutes old. Not rolling back."
        fi
        touch "/tmp/.ludus-ci-cluster-${PIPELINE_ID}-${SNAP}-rolled-back"
        qm reboot "$NODE1"
        qm reboot "$NODE2"
        for IP in $CLUSTER_NODES; do
            for i in {1..90}; do
                ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" "echo ready" && break || sleep 5
            done
        done
        sleep 60
    fi
elif [[ "$CUSTOM_ENV_LUDUS_INSTALL_STEP" == "take-cluster-snapshot" ]]; then
    SNAP="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    if ! qm listsnapshot "$NODE1" | grep -q "$SNAP"; then
        echo "Snapshotting cluster nodes -> $SNAP"
        qm snapshot "$NODE1" "$SNAP" --vmstate true
        qm snapshot "$NODE2" "$SNAP" --vmstate true
    fi
fi

for IP in $CLUSTER_NODES; do
    configure_cluster_dns "$IP"
    repair_cluster_cephfs "$IP"
done
ensure_cluster_sdn_zone

# Tidy old rollback tracking files
find /tmp/ -name '.ludus-ci-cluster-*' -type f -mtime +2 -exec rm {} + 2>/dev/null || true

# Export VM_ID and VM_IP for run.sh
export VM_ID=$NODE1
export VM_IP=$CLUSTER_NODE1_IP
