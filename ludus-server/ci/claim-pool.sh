#!/usr/bin/env bash

# /opt/ludus/ci/claim-pool.sh
#
# Atomically claims one of the VM pools (A or B) for the current pipeline.
# Writes POOL=<A|B> to pool.env so the gitlab-ci.yml claim-pool job can
# expose it via dotenv to all downstream jobs in the pipeline.
#
# Selection: try A first; fall back to B; otherwise sleep until one is free.
# Hard fail after 1 hour. Stale locks (>6h old) are automatically broken.

set -eo pipefail

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

WAIT_TIMEOUT=3600       # 1 hour
WAIT_INTERVAL=10
STALE_THRESHOLD=21600   # 6 hours

# Atomic per-pool claim using mkdir (POSIX-atomic). Echoes the pool name
# on success.
try_claim() {
    local POOL="$1"
    local LOCK="$POOL_ASSIGNMENT_DIR/pool-${POOL}.lock"

    if mkdir "$LOCK" 2>/dev/null; then
        echo "$PIPELINE_ID" > "$LOCK/owner"
        printf '%s' "$POOL"
        return 0
    fi

    # Lock exists. First, check whether the owning pipeline is in a
    # terminal state via the GitLab API — covers the common case where a
    # user canceled the pipeline (when:always release jobs do NOT run on
    # cancellation, so the lock would otherwise sit until STALE_THRESHOLD).
    local OWNER
    OWNER=$(cat "$LOCK/owner" 2>/dev/null)
    if [[ -n "$OWNER" ]] && is_pipeline_terminal "$OWNER"; then
        echo "Breaking pool ${POOL} lock — owner pipeline ${OWNER} is in a terminal state" >&2
        rm -rf "$LOCK"
        return 1
    fi

    # Time-based fallback for the case where the API is unreachable or
    # the pipeline ID isn't queryable.
    local LOCK_MTIME
    LOCK_MTIME=$(stat -c %Y "$LOCK" 2>/dev/null || stat -f %m "$LOCK" 2>/dev/null || echo 0)
    local AGE=$(( $(date +%s) - LOCK_MTIME ))
    if [[ "$AGE" -gt "$STALE_THRESHOLD" ]]; then
        echo "Breaking stale pool ${POOL} lock (age ${AGE}s)" >&2
        rm -rf "$LOCK"
    fi
    return 1
}

START=$(date +%s)
while true; do
    for P in A B; do
        if CLAIMED=$(try_claim "$P"); then
            # Persist the assignment to the runner host so prepare.sh can read
            # it even when downstream jobs strip claim-pool from their
            # dependencies and the dotenv variable is not propagated.
            echo "${CLAIMED}" > "$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}.pool"
            echo "POOL=${CLAIMED}" | tee pool.env
            echo "Claimed pool ${CLAIMED} for pipeline ${PIPELINE_ID}"
            exit 0
        fi
    done

    ELAPSED=$(( $(date +%s) - START ))
    if [[ "$ELAPSED" -ge "$WAIT_TIMEOUT" ]]; then
        echo "ERROR: No pool free after ${WAIT_TIMEOUT}s (1 hour). Failing." >&2
        exit "${BUILD_FAILURE_EXIT_CODE:-1}"
    fi

    echo "Both pools busy. Waited ${ELAPSED}s / ${WAIT_TIMEOUT}s..."
    sleep "$WAIT_INTERVAL"
done
