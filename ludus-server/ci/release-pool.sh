#!/usr/bin/env bash

# /opt/ludus/ci/release-pool.sh
#
# Releases the pool lock claimed by claim-pool.sh, but only if the lock
# is still owned by this pipeline. Idempotent.
#
# POOL is read from the dotenv artifact published by the claim-pool job.

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

if [[ -z "$POOL" ]]; then
    echo "POOL not set; nothing to release."
    exit 0
fi

LOCK="$POOL_ASSIGNMENT_DIR/pool-${POOL}.lock"
if [[ ! -d "$LOCK" ]]; then
    echo "Pool ${POOL} lock not present; nothing to release."
    exit 0
fi

OWNER=$(cat "$LOCK/owner" 2>/dev/null || echo "")
if [[ "$OWNER" == "$PIPELINE_ID" ]]; then
    rm -rf "$LOCK"
    echo "Released pool ${POOL} for pipeline ${PIPELINE_ID}"
else
    echo "Pool ${POOL} owned by '${OWNER}', not pipeline ${PIPELINE_ID}; not releasing."
fi

# Tidy this pipeline's rollback tracking files
rm -f /tmp/.ludus-ci-"${PIPELINE_ID}"-* 2>/dev/null || true

exit 0
