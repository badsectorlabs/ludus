#!/usr/bin/env bash

# /opt/ludus/ci/prepare-cluster.sh
#
# Prepares cluster nodes (VMIDs 1005/1006) for cluster CI tests.
# These VMs are shared across pools and use their own locking via
# /tmp/.ludus-ci-cluster-* tracking files.

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

# Cluster lock: only one pipeline can use cluster nodes at a time
CLUSTER_LOCK="$POOL_ASSIGNMENT_DIR/cluster.lock"
CLUSTER_OWNER="$POOL_ASSIGNMENT_DIR/cluster-owner"

# Wait for cluster to be free (another pipeline may be using it)
echo "Acquiring cluster lock..."
exec {CFD}>>"$CLUSTER_LOCK"
WAIT_START=$(date +%s)
while ! flock -n "$CFD" 2>/dev/null; do
    ELAPSED=$(( $(date +%s) - WAIT_START ))
    if [[ "$ELAPSED" -ge 600 ]]; then
        echo "ERROR: Cluster nodes busy for 600s. Exiting for retry."
        exit "${SYSTEM_FAILURE_EXIT_CODE:-71}"
    fi
    # Check for stale lock
    if [[ -f "$CLUSTER_OWNER" ]]; then
        FILE_AGE=$(( $(date +%s) - $(stat -c %Y "$CLUSTER_OWNER" 2>/dev/null || stat -f %m "$CLUSTER_OWNER" 2>/dev/null) ))
        if [[ "$FILE_AGE" -gt 21600 ]]; then
            echo "Cluster lock is stale (${FILE_AGE}s old). Breaking."
            rm -f "$CLUSTER_OWNER"
            break
        fi
    fi
    echo "Cluster busy, waiting... (${ELAPSED}s)"
    sleep 10
done
echo "$PIPELINE_ID" > "$CLUSTER_OWNER"

NODE1=$CLUSTER_NODE1_VMID
NODE2=$CLUSTER_NODE2_VMID

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "clean-cluster" ]]; then
    # Revert both nodes to clean snapshot
    echo "Reverting cluster nodes to 'clean' snapshot"
    qm rollback "$NODE1" clean --start 1
    qm rollback "$NODE2" clean --start 1
    # Wait for SSH
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
    # Only roll back once per pipeline per snapshot
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
        # Reboot the cluster nodes and wait for SSH
        qm reboot "$NODE1"
        qm reboot "$NODE2"
        for IP in $CLUSTER_NODES; do
            for i in {1..90}; do
                ssh -o ConnectTimeout=3 -F /home/gitlab-runner/.ssh/config root@"$IP" "echo ready" && break || sleep 5
            done
        done
        # Then wait 60 seconds for the cluster to stabilize
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

# Clean up old rollback tracking files
find /tmp/ -name '.ludus-ci-cluster-*' -type f -mtime +2 -exec rm {} +

# Export VM_ID and VM_IP for run.sh
export VM_ID=$NODE1
export VM_IP=$CLUSTER_NODE1_IP
