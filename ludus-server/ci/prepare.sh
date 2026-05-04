#!/usr/bin/env bash

# /opt/ludus/ci/prepare.sh
#
# Custom executor prepare phase. Claims a pool, selects the target VM,
# handles snapshot rollback and take-snapshot operations.

set -eo pipefail

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

# --- Cluster handling (dedicated shared VMs with their own locking) ---
if [[ -n "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"cluster"* ]]; then
    source "${currentDir}/base.sh"
    source "${currentDir}/prepare-cluster.sh"
    exit 0
fi

# --- Claim a pool (sets POOL, sources base.sh) ---
source "${currentDir}/claim-pool.sh"

# --- Resolve the target VM (sets VM_ID, VM_IP) ---
resolve_vm

echo "Pipeline $PIPELINE_ID | Pool $POOL | VM $VM_ID ($VM_IP) | Type: $CUSTOM_ENV_LUDUS_BUILD_TYPE | Snapshot: $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"

# --- Handle take-snapshot (full build milestone) ---
if [[ "$CUSTOM_ENV_LUDUS_INSTALL_STEP" == "take-snapshot" && -n "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" ]]; then
    if ! qm listsnapshot "$VM_ID" | grep -q "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"; then
        echo "Snapshotting VM ($VM_ID) -> $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
        qm snapshot "$VM_ID" "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" --vmstate true
        if [[ $? -ne 0 ]]; then
            echo "ERROR: Failed to snapshot VM $VM_ID"
            exit "${BUILD_FAILURE_EXIT_CODE:-1}"
        fi
    else
        echo "Snapshot $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME already exists on VM $VM_ID"
    fi
fi

# --- Rollback logic for quick tests (from-snapshot, not part of a full build) ---
FULL_BUILD_VMID_FILE="$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}-full-build-vmid"

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "from-snapshot" && ! -f "$FULL_BUILD_VMID_FILE" ]]; then
    TRACKING_FILE="/tmp/.ludus-ci-${PIPELINE_ID}-${CUSTOM_ENV_LUDUS_SNAPSHOT_NAME}-rolled-back"

    if [[ ! -f "$TRACKING_FILE" ]]; then
        # First job in this chain for this snapshot type - rollback to "clean"
        echo "Rolling back VM $VM_ID to 'clean' snapshot"
        qm rollback "$VM_ID" clean --start
        if [[ $? -ne 0 ]]; then
            echo "ERROR: Failed to rollback VM $VM_ID to 'clean' snapshot"
            exit "${BUILD_FAILURE_EXIT_CODE:-1}"
        fi

        # Wait for SSH to come up after rollback
        echo "Waiting for SSH on $VM_IP..."
        for i in {1..60}; do
            if ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" "echo ready" 2>/dev/null; then
                echo "SSH is up on $VM_IP"
                break
            fi
            if [[ $i -eq 60 ]]; then
                echo "ERROR: SSH did not come up on $VM_IP after 300 seconds"
                exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            fi
            sleep 5
        done

        # Track that we've rolled back this VM for this pipeline/snapshot
        touch "$TRACKING_FILE"
    else
        echo "VM $VM_ID already rolled back for pipeline $PIPELINE_ID / snapshot $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME. Skipping rollback."
    fi
fi

# --- Rollback for build VM (any-built) ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "any-built" ]]; then
    # Build VM doesn't need rollback - it supports concurrent builds.
    # Just ensure it's running and SSH is up.
    echo "Ensuring build VM $VM_ID is accessible..."
    for i in {1..10}; do
        if ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" "echo ready" 2>/dev/null; then
            echo "Build VM $VM_ID is accessible at $VM_IP"
            break
        fi
        if [[ $i -eq 10 ]]; then
            echo "ERROR: Build VM $VM_ID not accessible at $VM_IP"
            exit "${BUILD_FAILURE_EXIT_CODE:-1}"
        fi
        sleep 5
    done
fi

# --- Rollback for full build (first stage only) ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "full" ]]; then
    TRACKING_FILE="/tmp/.ludus-ci-${PIPELINE_ID}-full-build-rolled-back"

    if [[ ! -f "$TRACKING_FILE" ]]; then
        echo "Full build: Rolling back VM $VM_ID to 'clean' snapshot"
        qm rollback "$VM_ID" clean --start
        if [[ $? -ne 0 ]]; then
            echo "ERROR: Failed to rollback VM $VM_ID to 'clean' snapshot"
            exit "${BUILD_FAILURE_EXIT_CODE:-1}"
        fi

        echo "Waiting for SSH on $VM_IP..."
        for i in {1..60}; do
            if ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" "echo ready" 2>/dev/null; then
                echo "SSH is up on $VM_IP"
                break
            fi
            if [[ $i -eq 60 ]]; then
                echo "ERROR: SSH did not come up on $VM_IP after 300 seconds"
                exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            fi
            sleep 5
        done

        touch "$TRACKING_FILE"
    else
        echo "Full build VM $VM_ID already rolled back for pipeline $PIPELINE_ID. Continuing."
    fi
fi

# --- Clean up old tracking files (over 2 days old) ---
find /tmp/ -name '.ludus-ci-*' -type f -mtime +2 -exec rm {} + 2>/dev/null || true
find "$POOL_ASSIGNMENT_DIR" -name '*.pool' -type f -mtime +2 -exec rm {} + 2>/dev/null || true
find "$POOL_ASSIGNMENT_DIR" -name '*-full-build-vmid' -type f -mtime +2 -exec rm {} + 2>/dev/null || true
find "$POOL_ASSIGNMENT_DIR" -name '*.claiming' -type f -mtime +2 -exec rm {} + 2>/dev/null || true
