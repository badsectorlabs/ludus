#!/usr/bin/env bash

# /opt/ludus/ci/run.sh
#
# Custom executor run phase. Executes the job script:
# - Claim/release jobs run on the runner host directly (no VM).
# - All other jobs are SSH'd into the resolved VM.

currentDir="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
source "${currentDir}/base.sh"

# POOL is forwarded by GitLab from the claim-pool dotenv artifact.
export POOL="${CUSTOM_ENV_POOL:-${POOL:-}}"

# --- Claim/release jobs run on the runner host (gitlab-runner shell) ---
if [[ "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"claim"* || "$CUSTOM_ENV_LUDUS_BUILD_TYPE" == *"release"* ]]; then
    /bin/bash --login < "${1}"
    exit $?
fi

# --- Resolve target VM (sets VM_ID, VM_IP) ---
resolve_vm

# If we are in the install-check step, run a custom check loop.
# We must loop as the box reboots twice during install.
if [[ -n "$CUSTOM_ENV_LUDUS_INSTALL_STEP" && "$CUSTOM_ENV_LUDUS_INSTALL_STEP" = "check" ]]; then
    while true; do
        (ssh -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" /bin/bash --login < "$LUDUS_DIR/ci/check-install-status.sh" | tee /dev/stderr | grep -q 'Ludus install completed successfully') 2>&1
        if [[ $? -eq 0 ]]; then
            break
        fi
        sleep 3
    done
fi

# Transfer build artifacts (binaries/) from the HOST build directory to the target VM.
BUILD_DIR=$(dirname "${1}")
if [[ -d "$BUILD_DIR/binaries" ]]; then
    echo "Transferring build artifacts to $VM_IP..."
    scp -r -F /home/gitlab-runner/.ssh/config "$BUILD_DIR/binaries" gitlab-runner@"$VM_IP":~/binaries
fi

# Execute the job script on the target VM via SSH
ssh -F /home/gitlab-runner/.ssh/config gitlab-runner@"$VM_IP" /bin/bash --login < "${1}"
SSH_EXIT=$?

if [[ $SSH_EXIT -ne 0 && (-z "$CUSTOM_ENV_LUDUS_INSTALL_STEP" || "$CUSTOM_ENV_LUDUS_INSTALL_STEP" != "kickoff") ]]; then
    exit "${BUILD_FAILURE_EXIT_CODE:-1}"
elif [[ -n "$CUSTOM_ENV_LUDUS_INSTALL_STEP" && "$CUSTOM_ENV_LUDUS_INSTALL_STEP" = "kickoff" && $SSH_EXIT -ne 0 ]]; then
    echo "SSH connection lost, assuming reboot during install."
fi
