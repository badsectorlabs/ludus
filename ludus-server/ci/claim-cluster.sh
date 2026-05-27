#!/usr/bin/env bash

# /opt/ludus/ci/claim-cluster.sh
#
# Atomically claims the single cluster slot (VMIDs 1005/1006) for the
# current pipeline. The gitlab-ci.yml claim-cluster job runs this and
# all cluster-* jobs depend on it via `needs:`.
#
# Hard fail after 1 hour. Stale locks (>6h old) are automatically broken.

set -eo pipefail

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

WAIT_TIMEOUT=3600       # 1 hour
WAIT_INTERVAL=10
STALE_THRESHOLD=21600   # 6 hours
LOCK="$POOL_ASSIGNMENT_DIR/cluster.lock"

START=$(date +%s)
while true; do
    if mkdir "$LOCK" 2>/dev/null; then
        echo "$PIPELINE_ID" > "$LOCK/owner"
        echo "Claimed cluster for pipeline ${PIPELINE_ID}"
        exit 0
    fi

    # Active recovery: if the owning pipeline is in a terminal state
    # (canceled / failed / done), break the lock immediately so we don't
    # have to wait for STALE_THRESHOLD.
    OWNER=$(cat "$LOCK/owner" 2>/dev/null)
    if [[ -n "$OWNER" ]] && is_pipeline_terminal "$OWNER"; then
        echo "Breaking cluster lock — owner pipeline ${OWNER} is in a terminal state" >&2
        rm -rf "$LOCK"
        continue
    fi

    # Time-based fallback for unreachable API / unknown pipeline.
    LOCK_MTIME=$(stat -c %Y "$LOCK" 2>/dev/null || stat -f %m "$LOCK" 2>/dev/null || echo 0)
    AGE=$(( $(date +%s) - LOCK_MTIME ))
    if [[ "$AGE" -gt "$STALE_THRESHOLD" ]]; then
        echo "Breaking stale cluster lock (age ${AGE}s)" >&2
        rm -rf "$LOCK"
        continue
    fi

    ELAPSED=$(( $(date +%s) - START ))
    if [[ "$ELAPSED" -ge "$WAIT_TIMEOUT" ]]; then
        echo "ERROR: Cluster not free after ${WAIT_TIMEOUT}s (1 hour). Failing." >&2
        exit "${BUILD_FAILURE_EXIT_CODE:-1}"
    fi

    echo "Cluster busy. Waited ${ELAPSED}s / ${WAIT_TIMEOUT}s..."
    sleep "$WAIT_INTERVAL"
done
