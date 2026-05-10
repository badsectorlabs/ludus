#!/usr/bin/env bash

# /opt/ludus/ci/cleanup.sh
#
# Custom executor cleanup phase. Called after every job.
#
# Pool/cluster release is handled by explicit release-pool / release-cluster
# YAML jobs (stage: release, when: always), so this script is a no-op.

exit 0
