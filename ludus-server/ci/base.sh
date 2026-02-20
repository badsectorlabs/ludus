#!/usr/bin/env bash

# /opt/ludus/ci/base.sh

# Export variables needed for dynamic inventory
export PROXMOX_USERNAME=gitlab-runner@pam
export PROXMOX_PASSWORD=$(cat /opt/ludus/ci/.gitlab-runner-password)
export PROXMOX_URL=https://127.0.0.1:8006/
export PROXMOX_NODE=ludus
export PROXMOX_INVALID_CERT=true
export PROXMOX_HOSTNAME=127.0.0.1

# Set the VM name for the runner
export RUNNER_VM_NAME="runner-$CUSTOM_ENV_CI_RUNNER_ID-project-$CUSTOM_ENV_CI_PROJECT_ID-pipeline-$CUSTOM_ENV_CI_PIPELINE_ID"

# Hardcoded VMIDs for the two cluster nodes on the physical host
export NODE1_VMID=106  # EH-Ludus
export NODE2_VMID=107  # EH-Ludus-2

if [[ ! -z "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"cluster"* ]]; then
    export RUNNER_VM_NAME="EH-Ludus-Dev"
    export CLUSTER_NODE1=203.0.113.1
    export CLUSTER_NODE2=203.0.113.2
    export CLUSTER_NODE1_NAME=EH-Ludus
    export CLUSTER_NODE2_NAME=EH-Ludus-2
    export CLUSTER_NODES="$CLUSTER_NODE1 $CLUSTER_NODE2"
    # Primary node is where Ludus gets installed
    export CLUSTER_PRIMARY=$CLUSTER_NODE1
fi

# Set the ludus base dir
export LUDUS_DIR=/opt/ludus