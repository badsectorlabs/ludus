#!/usr/bin/env bash

# /opt/ludus/ci/claim-pool.sh
# Sources base.sh, then claims a pool (A or B) for this pipeline.
# Sets: POOL (exported)
#
# Cluster and build jobs skip pool claiming (they use shared VMs).

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

# Cluster and build jobs don't need a pool
if [[ -n "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"cluster"* ]]; then
    echo "Cluster build type - no pool needed"
    export POOL=""
    return 0 2>/dev/null || exit 0
fi
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "any-built" ]]; then
    echo "Build job - using dedicated build VM, no pool needed"
    export POOL=""
    return 0 2>/dev/null || exit 0
fi

ASSIGNMENT_FILE="$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}.pool"
STALE_THRESHOLD=21600  # 6 hours in seconds
WAIT_TIMEOUT=600       # 10 minutes
WAIT_INTERVAL=10       # seconds between retries

# Check if a pool assignment file is stale
is_stale() {
    local FILE="$1"
    if [[ ! -f "$FILE" ]]; then
        return 0  # Non-existent = stale
    fi
    local FILE_AGE=$(( $(date +%s) - $(stat -c %Y "$FILE" 2>/dev/null || stat -f %m "$FILE" 2>/dev/null) ))
    if [[ "$FILE_AGE" -gt "$STALE_THRESHOLD" ]]; then
        return 0  # Stale
    fi
    return 1  # Not stale
}

# Try to claim a specific pool. Returns 0 on success, 1 on failure.
try_claim_pool() {
    local POOL_NAME="$1"

    # Check if any other pipeline currently owns this pool
    local OWNER_FILE
    OWNER_FILE=$(grep -rl "^${POOL_NAME}$" "$POOL_ASSIGNMENT_DIR"/*.pool 2>/dev/null | head -1)

    if [[ -z "$OWNER_FILE" ]]; then
        # Pool is free - claim it
        echo "$POOL_NAME" > "$ASSIGNMENT_FILE"
        echo "Claimed pool $POOL_NAME for pipeline $PIPELINE_ID"
        export POOL="$POOL_NAME"
        return 0
    fi

    # Pool is owned - check if the owner is stale
    if is_stale "$OWNER_FILE"; then
        local STALE_PIPELINE
        STALE_PIPELINE=$(basename "$OWNER_FILE" .pool)
        echo "Reclaiming stale pool $POOL_NAME from pipeline $STALE_PIPELINE"
        rm -f "$OWNER_FILE"
        rm -f "$POOL_ASSIGNMENT_DIR/${STALE_PIPELINE}-full-build-vmid"
        echo "$POOL_NAME" > "$ASSIGNMENT_FILE"
        export POOL="$POOL_NAME"
        return 0
    fi

    return 1  # Pool is in use and not stale
}

claim_pool() {
    # 1. Check if this pipeline already has a pool assigned
    if [[ -f "$ASSIGNMENT_FILE" ]]; then
        POOL=$(cat "$ASSIGNMENT_FILE")
        export POOL
        echo "Pipeline $PIPELINE_ID already assigned to pool $POOL"
        return 0
    fi

    # 2. Use a per-pipeline flock to prevent races between parallel jobs from the same pipeline
    local PIPELINE_LOCK="$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}.claiming"
    exec {PFD}>>"$PIPELINE_LOCK"
    flock "$PFD"

    # Double-check after acquiring lock (another job may have claimed while we waited)
    if [[ -f "$ASSIGNMENT_FILE" ]]; then
        POOL=$(cat "$ASSIGNMENT_FILE")
        export POOL
        flock -u "$PFD"
        rm -f "$PIPELINE_LOCK"
        echo "Pipeline $PIPELINE_ID already assigned to pool $POOL (after lock)"
        return 0
    fi

    # 3. Try to claim Pool A, then Pool B
    if try_claim_pool "A"; then
        flock -u "$PFD"
        rm -f "$PIPELINE_LOCK"
        return 0
    fi
    if try_claim_pool "B"; then
        flock -u "$PFD"
        rm -f "$PIPELINE_LOCK"
        return 0
    fi

    flock -u "$PFD"
    rm -f "$PIPELINE_LOCK"

    # 4. Both pools occupied - wait with timeout
    echo "Both pools occupied. Waiting up to ${WAIT_TIMEOUT}s for a pool to free up..."
    local WAIT_START
    WAIT_START=$(date +%s)

    while true; do
        local ELAPSED=$(( $(date +%s) - WAIT_START ))
        if [[ "$ELAPSED" -ge "$WAIT_TIMEOUT" ]]; then
            echo "ERROR: No pool available after ${WAIT_TIMEOUT}s. Exiting for GitLab auto-retry."
            exit "${SYSTEM_FAILURE_EXIT_CODE:-71}"
        fi

        sleep "$WAIT_INTERVAL"

        # Re-acquire lock and retry
        exec {PFD}>>"$PIPELINE_LOCK"
        flock "$PFD"

        if [[ -f "$ASSIGNMENT_FILE" ]]; then
            POOL=$(cat "$ASSIGNMENT_FILE")
            export POOL
            flock -u "$PFD"
            rm -f "$PIPELINE_LOCK"
            return 0
        fi

        if try_claim_pool "A"; then
            flock -u "$PFD"
            rm -f "$PIPELINE_LOCK"
            return 0
        fi
        if try_claim_pool "B"; then
            flock -u "$PFD"
            rm -f "$PIPELINE_LOCK"
            return 0
        fi

        flock -u "$PFD"
        echo "Still waiting... (${ELAPSED}s / ${WAIT_TIMEOUT}s)"
    done
}

# Run the claim
claim_pool
