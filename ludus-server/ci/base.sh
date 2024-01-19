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

# Set the ludus base dir
export LUDUS_DIR=/opt/ludus