#!/usr/bin/env bash

# /opt/ludus/ci/cleanup.sh
#
# Custom executor cleanup phase. Called after every job.
# Releases the pool assignment when LUDUS_RELEASE_POOL is set (terminal jobs)
# or LUDUS_DESTROY_VM is set (legacy compatibility).
# For full builds, rolls back the base VM to "clean" and removes pipeline-created snapshots.

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

set -eo pipefail

ASSIGNMENT_FILE="$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}.pool"
FULL_BUILD_VMID_FILE="$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}-full-build-vmid"

if [[ "$CUSTOM_ENV_LUDUS_RELEASE_POOL" == "true" || "$CUSTOM_ENV_LUDUS_DESTROY_VM" == "true" ]]; then
    echo "Releasing pool for pipeline $PIPELINE_ID"

    # If this was a full build, clean up the base VM
    if [[ -f "$FULL_BUILD_VMID_FILE" ]]; then
        FULL_VM=$(cat "$FULL_BUILD_VMID_FILE")
        echo "Full build cleanup: rolling back VM $FULL_VM to 'clean' and removing pipeline snapshots"

        # Delete pipeline-created snapshots (ignore errors if they don't exist)
        for SNAP in clean_install templates_built range_built_admin range_built_user; do
            qm delsnapshot "$FULL_VM" "$SNAP" 2>/dev/null && echo "  Deleted snapshot: $SNAP" || true
        done

        # Roll back to the clean base state
        qm rollback "$FULL_VM" clean --start 2>/dev/null || true
    fi

    # Remove pool assignment
    if [[ -f "$ASSIGNMENT_FILE" ]]; then
        POOL=$(cat "$ASSIGNMENT_FILE")
        echo "Released pool $POOL for pipeline $PIPELINE_ID"
        rm -f "$ASSIGNMENT_FILE"
    fi

    # Remove full build tracking
    rm -f "$FULL_BUILD_VMID_FILE"

    # Clean up rollback tracking files for this pipeline
    rm -f /tmp/.ludus-ci-"${PIPELINE_ID}"-*
fi
