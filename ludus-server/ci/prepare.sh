#!/usr/bin/env bash

# /opt/ludus/ci/prepare.sh
#
# Custom executor prepare phase. Runs once per job before the script.
#
# - Claim/release jobs (build type contains "claim" or "release") do no VM
#   work here.
# - Cluster jobs delegate to prepare-cluster.sh.
# - All non-cluster test jobs resolve or create their per-pipeline CI clone.

set -eo pipefail

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

# --- Claim/release jobs need no VM prep ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"claim"* || "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"release"* ]]; then
    echo "Claim/release job ($CUSTOM_ENV_LUDUS_BUILD_TYPE) - no VM prep needed"
    exit 0
fi

# --- Cluster handling (dedicated shared VMs) ---
if [[ -n "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"cluster"* ]]; then
    source "${currentDir}/prepare-cluster.sh"
    exit 0
fi

# --- Resolve or create the target VM (sets VM_ID, VM_IP) ---
resolve_vm

echo "Pipeline $PIPELINE_ID | VM $VM_ID ($VM_IP) | Type: $CUSTOM_ENV_LUDUS_BUILD_TYPE | Snapshot: $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME | Series: ${VM_SERIES:-n/a}"

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

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "full" || "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "from-snapshot" ]]; then
    wait_for_ci_vm_ssh "$VM_ID" "$VM_IP" || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
fi

if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "from-snapshot" ]]; then
    case "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" in
        "templates_built")
            wait_for_ludus_command "$VM_IP" "/opt/ludus/ci/.apikey-admin" "ludus templates list --json" "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            ;;
        "range_built_admin")
            wait_for_ludus_command "$VM_IP" "/opt/ludus/ci/.apikey-admin" "ludus range list --json" "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            ;;
        "range_built_user"|"integration_ready")
            wait_for_ludus_command "$VM_IP" "/opt/ludus/ci/.apikey-user" "ludus range list --json" "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            ;;
    esac
fi

# --- Clean up old tracking files (over 2 days old) ---
find /tmp/ -name '.ludus-ci-*' -type f -mtime +2 -exec rm {} + 2>/dev/null || true
