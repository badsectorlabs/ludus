#!/usr/bin/env bash

# /opt/ludus/ci/prepare.sh

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source ${currentDir}/base.sh # Get variables from base script

echo "CUSTOM_ENV_LUDUS_BUILD_TYPE: $CUSTOM_ENV_LUDUS_BUILD_TYPE"

# This will set VM_ID
source ${currentDir}/get-vm-ip.sh

SKIP_BUILD="false"
if [[ ! -z "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && ("$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "any-built" || "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "from-snapshot") ]]; then
    if [[ -z "$VM_ID" ]]; then
        echo "Could not find a runner VM, will build one"
    else
        echo "Using VM: $VM_ID"
        SKIP_BUILD="true"
    fi
elif [[ ! -z "$CUSTOM_ENV_LUDUS_INSTALL_STEP" && "$CUSTOM_ENV_LUDUS_INSTALL_STEP" == "take-snapshot" ]]; then
    qm listsnapshot $VM_ID | grep -q "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    if [[ $? -ne 0 ]]; then
        echo "Snapshotting VM: ($VM_ID) $RUNNER_VM_NAME -> $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
        qm snapshot $VM_ID "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" --vmstate true # TODO replace this with API when runner is properly run as gitlab-runner
        if [[ $? -ne 0 ]]; then
            exit "$BUILD_FAILURE_EXIT_CODE"
        fi
    fi
fi

if [[ ! -z "$CUSTOM_ENV_LUDUS_BUILD_TYPE" && "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == "from-snapshot" && "$SKIP_BUILD" == "true" && ! -f /tmp/.ludus-ci-$CUSTOM_ENV_CI_PIPELINE_ID-rolled-back ]]; then
    # We want to use a snapshot and we have a CI VM that already exists
    qm listsnapshot $VM_ID | grep -q "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    if [[ $? -eq 0 ]]; then
        echo "Rolling back VM $VM_ID to $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME snapshot"
        qm rollback $VM_ID "$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME" --start
        # Use a file to track rollbacks for this pipline - only roll back once per pipeline
        touch /tmp/.ludus-ci-$CUSTOM_ENV_CI_PIPELINE_ID-rolled-back
    else
        echo "Failed to rollback VM $VM_ID to snapshot $CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
        SKIP_BUILD="false"
    fi
fi

if [[ "$SKIP_BUILD" == "false" ]]; then
    # Do it
    ansible-playbook -i /opt/ludus/ansible/range-management/proxmox.py --extra-vars "api_user=$PROXMOX_USERNAME \
    api_password=$PROXMOX_PASSWORD api_host=$PROXMOX_HOSTNAME node_name=$PROXMOX_NODE ludus_install_path=$LUDUS_DIR \
    runner_vm_name=$RUNNER_VM_NAME skip_build=$SKIP_BUILD" $LUDUS_DIR/ci/prepare.yml
fi
