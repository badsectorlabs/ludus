#!/usr/bin/env bash

# /opt/ludus/ci/prepare.sh
#
# Custom executor prepare phase. Runs once per job before the script.
#
# - Claim/release jobs (build type contains "claim" or "release") do no VM
#   work here.
# - Cluster jobs delegate to prepare-cluster.sh.
# - All other jobs read POOL from the environment (set by GitLab from the
#   claim-pool job's dotenv artifact), then resolve the target VM and
#   handle snapshot rollback.

set -eo pipefail

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

# --- Claim/release jobs need no VM prep ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"claim"* || "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"release"* ]]; then
    echo "Claim/release job ($CUSTOM_ENV_LUDUS_BUILD_TYPE) — no VM prep needed"
    exit 0
fi

# --- Cluster handling (dedicated shared VMs) ---
if [[ -n "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"cluster"* ]]; then
    source "${currentDir}/prepare-cluster.sh"
    exit 0
fi

# --- POOL is exported by GitLab from the claim-pool dotenv artifact ---
# GitLab forwards artifact dotenv variables as CUSTOM_ENV_<NAME> in the
# custom executor; fall back to a plain POOL env if set.
export POOL="${CUSTOM_ENV_POOL:-${POOL:-}}"
if [[ -z "$POOL" ]]; then
    echo "ERROR: POOL is not set. The claim-pool job must run before this job and pass POOL via dotenv." >&2
    exit "${BUILD_FAILURE_EXIT_CODE:-1}"
fi

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

# --- Rollback for snapshot-based tests ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "from-snapshot" ]]; then
    TRACKING_FILE="/tmp/.ludus-ci-${PIPELINE_ID}-${CUSTOM_ENV_LUDUS_SNAPSHOT_NAME}-rolled-back"

    if [[ ! -f "$TRACKING_FILE" ]]; then
        echo "Rolling back VM $VM_ID to 'clean' snapshot"
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
        echo "VM $VM_ID already rolled back for pipeline $PIPELINE_ID / snapshot $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME. Skipping rollback."
    fi
fi

# --- Rollback for build VM (any-built) ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "any-built" ]]; then
    # Build VM doesn't need rollback - it supports concurrent builds.
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
find "$POOL_ASSIGNMENT_DIR" -mindepth 1 -maxdepth 1 -mtime +2 -exec rm -rf {} + 2>/dev/null || true
