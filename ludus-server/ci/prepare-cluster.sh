#!/usr/bin/env bash

# /opt/ludus/ci/prepare-cluster.sh

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source ${currentDir}/base.sh

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "clean-cluster" ]]; then
    # Revert both nodes to clean snapshot
    echo "Reverting cluster nodes to 'clean' snapshot"
    qm rollback $NODE1_VMID clean --start 1
    qm rollback $NODE2_VMID clean --start 1
    # Wait for SSH
    for IP in $CLUSTER_NODES; do
        for i in {1..90}; do
            ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@$IP "echo ready" && break || sleep 5
        done
    done
elif [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "cluster-from-snapshot" ]]; then
    SNAP="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    if ! qm listsnapshot $NODE1_VMID | grep -q "$SNAP"; then
        echo "Snapshot $SNAP not found, falling back to full build"
        exit $BUILD_FAILURE_EXIT_CODE
    fi
    # Only roll back once per pipeline per snapshot
    if [[ ! -f /tmp/.ludus-ci-cluster-$CUSTOM_ENV_CI_PIPELINE_ID-$SNAP-rolled-back ]]; then
        SNAPTIME=$(pvesh get /nodes/$PROXMOX_NODE/qemu/$NODE1_VMID/snapshot --output-format=json | jq --arg S "$SNAP" '.[] | select(.name==$S) | .snaptime')
        DIFF=$(( $(date +%s) - $SNAPTIME ))
        if [[ "$DIFF" -gt 120 ]]; then
            echo "Rolling back cluster to $SNAP"
            qm rollback $NODE1_VMID "$SNAP" --start
            qm rollback $NODE2_VMID "$SNAP" --start
            for IP in $CLUSTER_NODES; do
                for i in {1..30}; do
                    ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@$IP "echo ready" && break || sleep 5
                done
            done
        else
            echo "$SNAP snapshot is < 2 minutes old. Not rolling back."
        fi
        touch /tmp/.ludus-ci-cluster-$CUSTOM_ENV_CI_PIPELINE_ID-$SNAP-rolled-back
        # Reboot the cluster nodes and wait for SSH
        qm reboot $NODE1_VMID
        qm reboot $NODE2_VMID
        for IP in $CLUSTER_NODES; do
            for i in {1..90}; do
                ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@$IP "echo ready" && break || sleep 5
            done
        done
        # Then wait 60 seconds for the cluster to stabilize
        sleep 60
    fi
elif [[ "$CUSTOM_ENV_LUDUS_INSTALL_STEP" == "take-cluster-snapshot" ]]; then
    SNAP="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    if ! qm listsnapshot $NODE1_VMID | grep -q "$SNAP"; then
        echo "Snapshotting cluster nodes -> $SNAP"
        qm snapshot $NODE1_VMID "$SNAP" --vmstate true
        qm snapshot $NODE2_VMID "$SNAP" --vmstate true
    fi
fi

# Clean up old rollback tracking files
find /tmp/ -name '.ludus-ci-cluster-*' -type f -mtime +2 -exec rm {} +
