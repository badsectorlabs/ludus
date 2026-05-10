#!/usr/bin/env bash

# /opt/ludus/ci/base.sh

# Export variables needed for dynamic inventory
export PROXMOX_USERNAME=gitlab-runner@pam
export PROXMOX_PASSWORD=$(cat /opt/ludus/ci/.gitlab-runner-password)
export PROXMOX_URL=https://127.0.0.1:8006/
# Discover the local node name; fall back to short hostname if pvesh/jq unavailable
export PROXMOX_NODE=$(hostname -s 2>/dev/null || hostname)
export PROXMOX_INVALID_CERT=true
export PROXMOX_HOSTNAME=127.0.0.1

# Set the ludus base dir
export LUDUS_DIR=/opt/ludus

# Pool assignment directory (must exist on the Proxmox host)
export POOL_ASSIGNMENT_DIR=/opt/ludus/ci/pool-assignments
mkdir -p "$POOL_ASSIGNMENT_DIR"

# --- VM Pool Definitions ---
# Pool A: non-cluster testing VMs
export POOL_A_BASE=1000  # VMIDs 1000-1004

# Pool B: non-cluster testing VMs
export POOL_B_BASE=1007  # VMIDs 1007-1011

# Shared VMs (not pool-locked)
export CLUSTER_NODE1_VMID=1005
export CLUSTER_NODE2_VMID=1006
export BUILD_VMID=1012

# Pipeline ID for pool tracking
export PIPELINE_ID="${CUSTOM_ENV_CI_PIPELINE_ID}"

# --- Helper Functions ---

# Map snapshot name to VM offset within a pool
get_vm_offset() {
    local SNAPSHOT_NAME="$1"
    case "$SNAPSHOT_NAME" in
        "clean_install")     echo 1 ;;
        "templates_built")   echo 2 ;;
        "range_built_admin") echo 3 ;;
        "range_built_user")  echo 4 ;;
        *)                   echo 0 ;;  # base VM
    esac
}

# Compute VMID from pool name and offset
get_vmid_for_pool() {
    local POOL="$1"
    local OFFSET="$2"
    if [ "$POOL" = "A" ]; then
        echo $(( POOL_A_BASE + OFFSET ))
    else
        echo $(( POOL_B_BASE + OFFSET ))
    fi
}

# Authenticate to Proxmox API and return the VM's 203.0.113.x IP
get_vm_ip_by_vmid() {
    local VMID="$1"

    if [[ -z "$VMID" ]]; then
        echo "Error: VMID not provided" >&2
        return 1
    fi

    # Authenticate
    local TICKET_RESPONSE
    TICKET_RESPONSE=$(curl -s -k -d "username=${PROXMOX_USERNAME}" \
        --data-urlencode "password=$PROXMOX_PASSWORD" \
        https://127.0.0.1:8006/api2/json/access/ticket)
    local COOKIE
    COOKIE=$(echo "${TICKET_RESPONSE}" | jq -r '.data.ticket')

    # Retry loop - VM may be booting after rollback
    local IP=""
    for i in {1..30}; do
        IP=$(curl -s -k -b "PVEAuthCookie=$COOKIE" \
            "https://127.0.0.1:8006/api2/json/nodes/$PROXMOX_NODE/qemu/$VMID/agent/network-get-interfaces" \
            | jq -r '.data.result[]? | ."ip-addresses"[]? | ."ip-address"? // empty' \
            | grep 203.0.113)
        if [[ -n "$IP" ]]; then
            echo "$IP"
            return 0
        fi
        sleep 5
    done

    echo "Error: Could not get IP for VM $VMID after 30 attempts" >&2
    return 1
}

# Populate CLUSTER_NODE{1,2}_IP, CLUSTER_NODES, and CLUSTER_PRIMARY by
# querying the Proxmox API. Caches values; safe to call multiple times.
discover_cluster_ips() {
    if [[ -z "$CLUSTER_NODE1_IP" ]]; then
        CLUSTER_NODE1_IP=$(get_vm_ip_by_vmid "$CLUSTER_NODE1_VMID") || return 1
        export CLUSTER_NODE1_IP
    fi
    if [[ -z "$CLUSTER_NODE2_IP" ]]; then
        CLUSTER_NODE2_IP=$(get_vm_ip_by_vmid "$CLUSTER_NODE2_VMID") || return 1
        export CLUSTER_NODE2_IP
    fi
    export CLUSTER_NODES="$CLUSTER_NODE1_IP $CLUSTER_NODE2_IP"
    export CLUSTER_PRIMARY="$CLUSTER_NODE1_IP"
}

# Resolve the target VMID for the current job based on build type, snapshot name, and pool
# Sets: VM_ID, VM_IP
resolve_vm() {
    local BUILD_TYPE="$CUSTOM_ENV_LUDUS_BUILD_TYPE"
    local SNAPSHOT_NAME="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"

    # Claim/release jobs run on the runner host directly; no target VM.
    if [[ "$BUILD_TYPE" == *"claim"* || "$BUILD_TYPE" == *"release"* ]]; then
        echo "Claim/release job ($BUILD_TYPE) — no VM resolution needed"
        return 0
    fi

    # Cluster builds use dedicated shared VMs
    if [[ -n "$BUILD_TYPE" && "$BUILD_TYPE" == *"cluster"* ]]; then
        VM_ID=$CLUSTER_NODE1_VMID
        export VM_ID
        discover_cluster_ips || return 1
        VM_IP="$CLUSTER_NODE1_IP"
        export VM_IP
        echo "Cluster build type, using VM ID: $VM_ID ($VM_IP)"
        return 0
    fi

    # Build jobs use the dedicated build VM (supports concurrent builds)
    if [[ "$BUILD_TYPE" == "any-built" ]]; then
        VM_ID=$BUILD_VMID
        export VM_ID
        VM_IP=$(get_vm_ip_by_vmid "$VM_ID")
        export VM_IP
        echo "Build job, using dedicated build VM: $VM_ID ($VM_IP)"
        return 0
    fi

    # Full build: always use offset 0 (base VM)
    if [[ "$BUILD_TYPE" == "full" ]]; then
        VM_ID=$(get_vmid_for_pool "$POOL" 0)
        export VM_ID
        VM_IP=$(get_vm_ip_by_vmid "$VM_ID")
        export VM_IP
        echo "Full build, using base VM: $VM_ID ($VM_IP)"
        return 0
    fi

    # from-snapshot: use the dedicated VM for this snapshot type
    if [[ "$BUILD_TYPE" == "from-snapshot" ]]; then
        local OFFSET
        OFFSET=$(get_vm_offset "$SNAPSHOT_NAME")
        VM_ID=$(get_vmid_for_pool "$POOL" "$OFFSET")
        export VM_ID
        VM_IP=$(get_vm_ip_by_vmid "$VM_ID")
        export VM_IP
        echo "Quick test (snapshot=$SNAPSHOT_NAME), using VM: $VM_ID ($VM_IP)"
        return 0
    fi

    echo "Error: Unknown LUDUS_BUILD_TYPE: $BUILD_TYPE" >&2
    return 1
}
