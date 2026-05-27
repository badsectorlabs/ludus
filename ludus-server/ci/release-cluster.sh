#!/usr/bin/env bash

# /opt/ludus/ci/release-cluster.sh
#
# Releases the cluster lock claimed by claim-cluster.sh, but only if the
# lock is still owned by this pipeline. Idempotent.

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

LOCK="$POOL_ASSIGNMENT_DIR/cluster.lock"
if [[ ! -d "$LOCK" ]]; then
    echo "Cluster lock not present; nothing to release."
    exit 0
fi

OWNER=$(cat "$LOCK/owner" 2>/dev/null || echo "")
if [[ "$OWNER" == "$PIPELINE_ID" ]]; then
    rm -rf "$LOCK"
    echo "Released cluster for pipeline ${PIPELINE_ID}"
else
    echo "Cluster owned by '${OWNER}', not pipeline ${PIPELINE_ID}; not releasing."
fi

# Tidy this pipeline's cluster rollback tracking files
rm -f /tmp/.ludus-ci-cluster-"${PIPELINE_ID}"-* 2>/dev/null || true

exit 0
