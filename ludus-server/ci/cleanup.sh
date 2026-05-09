#!/usr/bin/env bash

# /opt/ludus/ci/cleanup.sh
#
# Custom executor cleanup phase. Called after every job.
# Releases the pool assignment when LUDUS_RELEASE_POOL is set (terminal jobs)
# or LUDUS_DESTROY_VM is set (legacy compatibility).

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

set -eo pipefail

ASSIGNMENT_FILE="$POOL_ASSIGNMENT_DIR/${PIPELINE_ID}.pool"

if [[ "$CUSTOM_ENV_LUDUS_RELEASE_POOL" == "true" || "$CUSTOM_ENV_LUDUS_DESTROY_VM" == "true" ]]; then
    echo "Releasing pool for pipeline $PIPELINE_ID"

    # Remove pool assignment
    if [[ -f "$ASSIGNMENT_FILE" ]]; then
        POOL=$(cat "$ASSIGNMENT_FILE")
        echo "Released pool $POOL for pipeline $PIPELINE_ID"
        rm -f "$ASSIGNMENT_FILE"
    fi

    # Clean up rollback tracking files for this pipeline
    rm -f /tmp/.ludus-ci-"${PIPELINE_ID}"-*
fi
