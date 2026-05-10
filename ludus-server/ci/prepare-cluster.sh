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

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "clean-cluster" ]]; then
    echo "Reverting cluster nodes to 'clean' snapshot"
    qm rollback "$NODE1" clean --start 1
    qm rollback "$NODE2" clean --start 1
    for IP in $CLUSTER_NODES; do
        for i in {1..90}; do
            ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@"$IP" "echo ready" && break || sleep 5
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
                    ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@"$IP" "echo ready" && break || sleep 5
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
                ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@"$IP" "echo ready" && break || sleep 5
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

# Tidy old rollback tracking files
find /tmp/ -name '.ludus-ci-cluster-*' -type f -mtime +2 -exec rm {} + 2>/dev/null || true

# Export VM_ID and VM_IP for run.sh
export VM_ID=$NODE1
export VM_IP=$CLUSTER_NODE1_IP
