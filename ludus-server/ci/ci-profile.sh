#!/usr/bin/env bash

# Pure profile configuration shared by the executor and seed provisioner.
export CI_PROFILE="${CUSTOM_ENV_LUDUS_CI_PROFILE:-host-2.3}"
case "$CI_PROFILE" in
    host-2.3)
        export CI_SEED_BASE_VMID=${CI_SEED_BASE_VMID:-1000}
        export CI_SEED_CLEAN_INSTALL_VMID=${CI_SEED_CLEAN_INSTALL_VMID:-1001}
        export CI_SEED_TEMPLATES_BUILT_VMID=${CI_SEED_TEMPLATES_BUILT_VMID:-1002}
        export CI_SEED_RANGE_ADMIN_VMID=${CI_SEED_RANGE_ADMIN_VMID:-1003}
        export CI_SEED_RANGE_USER_VMID=${CI_SEED_RANGE_USER_VMID:-1004}
        export CI_SEED_INTEGRATION_VMID=${CI_SEED_INTEGRATION_VMID:-1007}
        export CI_SEED_LEGACY_MIGRATION_VMID=${CI_SEED_LEGACY_MIGRATION_VMID:-}
        export CLUSTER_NODE1_VMID=1005 CLUSTER_NODE2_VMID=1006 BUILD_VMID=1012
        export CI_NAMESPACE= CI_VM_NAME_PREFIX=ci
        export CI_CLONE_MIN_VMID=100
        ;;
    lxc-2.4)
        export CI_SEED_BASE_VMID=2400 CI_SEED_CLEAN_INSTALL_VMID=2401
        export CI_SEED_TEMPLATES_BUILT_VMID=2402 CI_SEED_RANGE_ADMIN_VMID=2403
        export CI_SEED_RANGE_USER_VMID=2404 CI_SEED_INTEGRATION_VMID=2407
        export CI_SEED_LEGACY_MIGRATION_VMID=2409
        export CLUSTER_NODE1_VMID=2405 CLUSTER_NODE2_VMID=2406 BUILD_VMID=2412
        export CI_NAMESPACE=lxc-2.4- CI_VM_NAME_PREFIX=ci-24
        # Reserve the entire persistent fixture block, including unused slots.
        export CI_CLONE_MIN_VMID=2413
        ;;
    *)
        echo "Error: Unknown LUDUS_CI_PROFILE: $CI_PROFILE" >&2
        return 1
        ;;
esac

# Disposable CI jobs use linked clones; provisioning fixed fixtures uses --full 1.
export CI_CLONE_FULL=0
