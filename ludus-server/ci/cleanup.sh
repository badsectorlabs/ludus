#!/usr/bin/env bash

# /opt/ludus/ci/cleanup.sh
#
# Custom executor cleanup phase. Called after every job.
#
# Dynamic clone cleanup and cluster lock release are handled by explicit
# YAML jobs. This custom-executor cleanup hook must stay a no-op so failed
# test series keep their VMs running for troubleshooting.

exit 0
