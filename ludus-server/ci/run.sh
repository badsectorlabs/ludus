#!/usr/bin/env bash

# /opt/ludus/ci/run.sh

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source ${currentDir}/base.sh # Get variables from base script.

# Get the IP of the current pipline VM (just the 203.0.113.x IP)
source ${currentDir}/get-vm-ip.sh

# If we are in the check step, run a custom check loop. We must loop as the box reboots twice
if [[ ! -z "$CUSTOM_ENV_LUDUS_INSTALL_STEP" && "$CUSTOM_ENV_LUDUS_INSTALL_STEP" = "check" ]]; then
    while true; do
        (ssh -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" /bin/bash --login < $LUDUS_DIR/ci/check-install-status.sh  | tee /dev/stderr | grep -q 'Ludus install completed successfully') 2>&1
        if [[ $? -eq 0 ]]; then
            break
        fi
        sleep 3
    done
fi

ssh -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" /bin/bash --login < "${1}"
if [[ $? -ne 0 && (-z "$CUSTOM_ENV_LUDUS_INSTALL_STEP" || "$CUSTOM_ENV_LUDUS_INSTALL_STEP" != "kickoff") ]]; then
    # Exit using the variable, to make the build as failure in GitLab CI.
    exit "$BUILD_FAILURE_EXIT_CODE"
elif [[ ! -z "$CUSTOM_ENV_LUDUS_INSTALL_STEP" && "$CUSTOM_ENV_LUDUS_INSTALL_STEP" = "kickoff" ]]; then
    echo "SSH connection lost, assuming reboot during install."
fi