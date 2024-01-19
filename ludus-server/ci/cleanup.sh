#!/usr/bin/env bash

# /opt/ludus/ci/cleanup.sh

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source ${currentDir}/base.sh # Get variables from base script

set -eo pipefail

if [[ "$CUSTOM_ENV_LUDUS_DESTROY_VM" == "true" ]]; then
    ansible-playbook -i /opt/ludus/ansible/range-management/proxmox.py --extra-vars "api_user=$PROXMOX_USERNAME api_password=$PROXMOX_PASSWORD api_host=$PROXMOX_HOSTNAME node_name=$PROXMOX_NODE ludus_install_path=$LUDUS_DIR runner_vm_name=$RUNNER_VM_NAME" $LUDUS_DIR/ci/cleanup.yml
fi