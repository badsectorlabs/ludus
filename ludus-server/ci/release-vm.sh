#!/usr/bin/env bash

# /opt/ludus/ci/release-vm.sh
#
# Destroys the dynamic CI clone for the requested pipeline series. This is
# invoked by success-only GitLab cleanup jobs. If any test in the series
# fails, the cleanup job does not run and the clone remains online for
# troubleshooting.

set -eo pipefail

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

SERIES="${CUSTOM_ENV_LUDUS_CI_SERIES:-${1:-}}"
if [[ -z "$SERIES" ]]; then
    SERIES=$(get_ci_series)
fi
SERIES=$(printf '%s\n' "$SERIES" | sanitize_slug)

destroy_ci_vm "$SERIES"
