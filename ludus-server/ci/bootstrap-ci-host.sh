#!/usr/bin/env bash

# Bootstrap a Ludus CI host after:
#   1. debian-13-x64-server-template has been built by Ludus
#
# Run from a Ludus repository checkout as root on the Proxmox/Ludus host:
#   LUDUS_ADMIN_API_KEY=EH... ./ludus-server/ci/bootstrap-ci-host.sh
#
# PROXMOX_USERNAME/PROXMOX_PASSWORD may be provided directly. If omitted,
# the script derives them from LUDUS_ADMIN_API_KEY via `ludus user creds get`.

set -euo pipefail

if [[ $(id -u) -ne 0 ]]; then
    echo "Error: this script must run as root" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_DIR="${REPO_DIR:-"$SCRIPT_DIR/../../ludus-source"}"
LUDUS_DIR="${LUDUS_DIR:-/opt/ludus}"
CI_DIR="$LUDUS_DIR/ci"
CONFIG_FILE="${LUDUS_CONFIG_FILE:-$LUDUS_DIR/config.yml}"

PROXMOX_HOSTNAME="${PROXMOX_HOSTNAME:-127.0.0.1}"
GITLAB_RUNNER_CONCURRENT="${GITLAB_RUNNER_CONCURRENT:-4}"
GITLAB_URL="${GITLAB_URL:-https://gitlab.com}"
CI_RECREATE="${CI_RECREATE:-0}"
CI_RECREATE_TEMPLATE="${CI_RECREATE_TEMPLATE:-0}"
CI_BUILD_BINARIES="${CI_BUILD_BINARIES:-1}"
CI_TEMPLATE_PARALLEL="${CI_TEMPLATE_PARALLEL:-2}"
CI_VM_DISK_SIZE="${CI_VM_DISK_SIZE:-250G}"
CI_CLUSTER_NODE1_IP="${CI_CLUSTER_NODE1_IP:-203.0.113.184}"
CI_CLUSTER_NODE2_IP="${CI_CLUSTER_NODE2_IP:-203.0.113.185}"
CI_CLUSTER_CEPH_OSD_DISK_SIZE="${CI_CLUSTER_CEPH_OSD_DISK_SIZE:-100G}"
CI_CLUSTER_CEPH_PG_NUM="${CI_CLUSTER_CEPH_PG_NUM:-32}"
CI_SETUP_TEMPLATE="${CI_SETUP_TEMPLATE:-auto}"
CI_SETUP_SEEDS="${CI_SETUP_SEEDS:-1}"

if [[ -f "$CONFIG_FILE" ]]; then
    PROXMOX_NODE="${PROXMOX_NODE:-$(awk '/^proxmox_node:/ {print $2}' "$CONFIG_FILE")}"
    PROXMOX_VM_STORAGE_POOL="${PROXMOX_VM_STORAGE_POOL:-$(awk '/^proxmox_vm_storage_pool:/ {print $2}' "$CONFIG_FILE")}"
    PROXMOX_VM_STORAGE_FORMAT="${PROXMOX_VM_STORAGE_FORMAT:-$(awk '/^proxmox_vm_storage_format:/ {print $2}' "$CONFIG_FILE")}"
fi

PROXMOX_NODE="${PROXMOX_NODE:-$(hostname -s 2>/dev/null || hostname)}"
PROXMOX_VM_STORAGE_POOL="${PROXMOX_VM_STORAGE_POOL:-zfs}"
PROXMOX_VM_STORAGE_FORMAT="${PROXMOX_VM_STORAGE_FORMAT:-raw}"

if [[ -z "${PROXMOX_USERNAME:-}" || -z "${PROXMOX_PASSWORD:-}" ]]; then
    if [[ -z "${LUDUS_ADMIN_API_KEY:-}" ]]; then
        echo "Error: set PROXMOX_USERNAME/PROXMOX_PASSWORD or LUDUS_ADMIN_API_KEY" >&2
        exit 1
    fi
    CREDS_JSON="$(LUDUS_API_KEY="$LUDUS_ADMIN_API_KEY" ludus user creds get --json)"
    PROXMOX_USERNAME="$(jq -r '.result.proxmoxUsername + "@" + .result.proxmoxRealm' <<<"$CREDS_JSON")"
    PROXMOX_PASSWORD="$(jq -r '.result.proxmoxPassword' <<<"$CREDS_JSON")"
fi

if [[ -z "${GITLAB_RUNNER_TOKEN:-}" ]]; then
    echo "GITLAB_RUNNER_TOKEN is not set, assuming the GitLab Runner is already installed and registered with GitLab" >&2
    GITLAB_RUNNER_CONFIGURE=false
    GITLAB_RUNNER_REGISTER=false
else
    GITLAB_RUNNER_CONFIGURE=true
    GITLAB_RUNNER_REGISTER=true
fi

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Error: required command '$1' not found" >&2
        exit 1
    }
}

require_command ansible-playbook
require_command ansible-galaxy
require_command go
require_command jq
require_command qm

ensure_host_packages() {
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update >/dev/null
        DEBIAN_FRONTEND=noninteractive apt-get install -y python3-passlib rsync >/dev/null
    fi
}

template_exists() {
    qm list | awk -v name="$1" '$2 == name { found=1 } END { exit found ? 0 : 1 }'
}

vmid_for_name() {
    qm list | awk -v name="$1" '$2 == name { print $1; found=1 } END { exit found ? 0 : 1 }'
}

ensure_bootstrap_acls() {
    local source_vmid

    if [[ "$PROXMOX_USERNAME" == "root@pam" ]]; then
        return 0
    fi

    source_vmid="$(vmid_for_name debian-13-x64-server-template)"
    pvesh create /pools --poolid CICD >/dev/null 2>&1 || true
    pveum aclmod "/vms/$source_vmid" -user "$PROXMOX_USERNAME" -role PVEAdmin
    pveum aclmod /pool/CICD -user "$PROXMOX_USERNAME" -role PVEAdmin
    pveum aclmod "/storage/$PROXMOX_VM_STORAGE_POOL" -user "$PROXMOX_USERNAME" -role PVEDatastoreAdmin
}

destroy_vm_if_exists() {
    local vmid="$1"
    if qm status "$vmid" >/dev/null 2>&1; then
        qm shutdown "$vmid" --timeout 60 || qm stop "$vmid" --skiplock 1 || true
        qm destroy "$vmid" --purge 1 --destroy-unreferenced-disks 1
    fi
}

destroy_vm_by_name_if_exists() {
    local name="$1"
    local vmid
    if vmid="$(vmid_for_name "$name")"; then
        destroy_vm_if_exists "$vmid"
    fi
}

sync_ci_files() {
    install -d "$CI_DIR"
    if [[ "$SCRIPT_DIR" != "$CI_DIR" ]]; then
        if command -v rsync >/dev/null 2>&1; then
            rsync -a --delete \
                --exclude binaries \
                --exclude pool-assignments \
                --exclude vm-assignments \
                --exclude ip-assignments \
                --exclude .gitlab-runner-password \
                "$SCRIPT_DIR/" "$CI_DIR/"
        else
            cp -a "$SCRIPT_DIR/." "$CI_DIR/"
        fi
    fi

    install -d -o gitlab-runner -g gitlab-runner "$CI_DIR/pool-assignments" "$CI_DIR/vm-assignments" "$CI_DIR/ip-assignments" "$CI_DIR/binaries"
    chmod +x "$CI_DIR"/*.sh
}

build_seed_binaries() {
    local bin_dir="$CI_DIR/binaries"
    local hash version

    # If the ludus source directory is not present, clone it
    if [[ ! -d "$REPO_DIR" ]]; then
        git clone https://gitlab.com/badsectorlabs/ludus.git "$REPO_DIR"
    else
        git -C "$REPO_DIR" fetch origin
    fi

    hash="$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo local)"
    version="${VERSION:-ci-bootstrap}"

    install -d "$bin_dir"

    echo "Building dynamic-inventory for CI seed server embed..."
    (
        cd "$REPO_DIR/dynamic-inventory"
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
            -ldflags "-s -w" \
            -o "$REPO_DIR/ludus-server/ansible/range-management/dynamic-inventory"
    )

    echo "Building ludus-server for CI seeds..."
    (
        cd "$REPO_DIR/ludus-server"
        CGO_ENABLED=1 go build -trimpath \
            -ldflags "-s -w -X main.GitCommitHash=$hash -X main.VersionString=$version" \
            -o "$bin_dir/ludus-server"
    )

    echo "Building linux/amd64 ludus-client for CI seeds..."
    (
        cd "$REPO_DIR/ludus-client"
        if [[ ! -d spinner/.git ]]; then
            rm -rf spinner
            git clone https://github.com/zimeg/spinner
        else
            git -C spinner fetch origin
        fi
        git -C spinner checkout unhide-interrupts
        git -C spinner reset --hard origin/unhide-interrupts
        go mod edit -replace github.com/briandowns/spinner=./spinner
        trap 'go mod edit -dropreplace=github.com/briandowns/spinner >/dev/null 2>&1 || true' EXIT
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
            -ldflags "-s -w -X ludus/cmd.GitCommitHash=$hash -X ludus/cmd.VersionString=$version" \
            -o "$bin_dir/ludus-client_linux-amd64"
    )

}

run_ci_template_setup() {
    local extra_vars_file
    export PROXMOX_URL="${PROXMOX_URL:-https://127.0.0.1:8006/}"
    export PROXMOX_NODE
    export PROXMOX_INVALID_CERT="${PROXMOX_INVALID_CERT:-true}"
    export PROXMOX_HOSTNAME
    export PROXMOX_USERNAME
    export PROXMOX_PASSWORD
    export PROXMOX_VM_STORAGE_POOL
    export LUDUS_DIR

    extra_vars_file="$(mktemp)"
    chmod 600 "$extra_vars_file"
    jq -n \
        --arg api_user "$PROXMOX_USERNAME" \
        --arg api_password "$PROXMOX_PASSWORD" \
        --arg api_host "$PROXMOX_HOSTNAME" \
        --arg node_name "$PROXMOX_NODE" \
        --arg ludus_install_path "$LUDUS_DIR" \
        --arg proxmox_vm_storage_pool "$PROXMOX_VM_STORAGE_POOL" \
        --arg ci_vm_disk_size "$CI_VM_DISK_SIZE" \
        --arg gitlab_registration_token "$GITLAB_RUNNER_TOKEN" \
        --arg gitlab_url "$GITLAB_URL" \
        --arg gitlab_runner_concurrent "$GITLAB_RUNNER_CONCURRENT" \
        --argjson gitlab_runner_configure "$GITLAB_RUNNER_CONFIGURE" \
        --argjson gitlab_runner_register "$GITLAB_RUNNER_REGISTER" \
        '{
            api_user: $api_user,
            api_password: $api_password,
            api_host: $api_host,
            node_name: $node_name,
            ludus_install_path: $ludus_install_path,
            proxmox_vm_storage_pool: $proxmox_vm_storage_pool,
            ci_vm_disk_size: $ci_vm_disk_size,
            gitlab_runner_register: $gitlab_runner_register,
            gitlab_runner_configure: $gitlab_runner_configure,
            gitlab_registration_token: $gitlab_registration_token,
            gitlab_url: $gitlab_url,
            gitlab_runner_concurrent: $gitlab_runner_concurrent
        }' > "$extra_vars_file"

    ansible-galaxy collection install community.general ansible.utils ansible.posix >/dev/null

    set +e
    ansible-playbook -i "$LUDUS_DIR/ansible/range-management/dynamic-inventory" \
        --extra-vars "@$CONFIG_FILE" \
        --extra-vars "@$extra_vars_file" \
        "$CI_DIR/ci-setup.yml"
    local rc=$?
    set -e

    rm -f "$extra_vars_file"
    return "$rc"
}

run_seed_setup() {
    local runner_password_file="$CI_DIR/.gitlab-runner-password"
    local runner_password extra_vars_file
    if [[ ! -f "$runner_password_file" ]]; then
        echo "$runner_password_file does not exist, creating it..." >&2
        # set a random password
        runner_password="$(openssl rand -hex 16)"
        echo "$runner_password" > "$runner_password_file"
        chmod 600 "$runner_password_file"
        # set the password for the gitlab-runner user in linux with passwd
        echo "gitlab-runner:$runner_password" | chpasswd
        echo "Password set for gitlab-runner user"
    fi
    runner_password="$(cat "$runner_password_file")"

    extra_vars_file="$(mktemp)"
    chmod 600 "$extra_vars_file"
    jq -n \
        --arg api_user "gitlab-runner@pam" \
        --arg api_password "$runner_password" \
        --arg api_host "$PROXMOX_HOSTNAME" \
        --arg node_name "$PROXMOX_NODE" \
        --arg ludus_install_path "$LUDUS_DIR" \
        --arg proxmox_vm_storage_pool "$PROXMOX_VM_STORAGE_POOL" \
        --arg proxmox_vm_storage_format "$PROXMOX_VM_STORAGE_FORMAT" \
        --arg ci_vm_disk_size "$CI_VM_DISK_SIZE" \
        --arg ci_cluster_node1_ip "$CI_CLUSTER_NODE1_IP" \
        --arg ci_cluster_node2_ip "$CI_CLUSTER_NODE2_IP" \
        --arg ci_cluster_ceph_osd_disk_size "$CI_CLUSTER_CEPH_OSD_DISK_SIZE" \
        --argjson ci_cluster_ceph_pg_num "$CI_CLUSTER_CEPH_PG_NUM" \
        --argjson ci_template_parallel "$CI_TEMPLATE_PARALLEL" \
        '{
            api_user: $api_user,
            api_password: $api_password,
            api_host: $api_host,
            node_name: $node_name,
            ludus_install_path: $ludus_install_path,
            proxmox_vm_storage_pool: $proxmox_vm_storage_pool,
            proxmox_vm_storage_format: $proxmox_vm_storage_format,
            ci_vm_disk_size: $ci_vm_disk_size,
            ci_cluster_node1_ip: $ci_cluster_node1_ip,
            ci_cluster_node2_ip: $ci_cluster_node2_ip,
            ci_cluster_ceph_osd_disk_size: $ci_cluster_ceph_osd_disk_size,
            ci_cluster_ceph_pg_num: $ci_cluster_ceph_pg_num,
            ci_template_parallel: $ci_template_parallel
        }' > "$extra_vars_file"

    ansible-galaxy role install lae.proxmox >/dev/null
    ansible-galaxy collection install community.general ansible.utils ansible.posix >/dev/null

    set +e
    ANSIBLE_ALLOW_BROKEN_CONDITIONALS="${ANSIBLE_ALLOW_BROKEN_CONDITIONALS:-true}" \
    ansible-playbook "$CI_DIR/ci-vm-setup.yml" \
        --extra-vars "@$extra_vars_file"
    local rc=$?
    set -e

    rm -f "$extra_vars_file"
    return "$rc"
}

if ! template_exists debian-13-x64-server-template; then
    echo "Error: debian-13-x64-server-template is not present. Build it with Ludus before running this script." >&2
    exit 1
fi

ensure_host_packages
ensure_bootstrap_acls

if [[ "$CI_RECREATE" == "1" ]]; then
    destroy_vm_by_name_if_exists debian-13-x64-server-ludus-ci
    if [[ "$CI_RECREATE_TEMPLATE" == "1" ]]; then
        destroy_vm_by_name_if_exists debian-13-x64-server-ludus-ci-template
    fi
    for vmid in 1000 1001 1002 1003 1004 1005 1006 1007 1012; do
        destroy_vm_if_exists "$vmid"
    done
    rm -f "$CI_DIR"/vm-assignments/*.env "$CI_DIR"/vm-assignments/*.lock
fi

if [[ "$CI_BUILD_BINARIES" == "1" ]]; then
    build_seed_binaries
fi

if [[ "$CI_SETUP_TEMPLATE" == "auto" ]]; then
    if template_exists debian-13-x64-server-ludus-ci-template; then
        CI_SETUP_TEMPLATE=0
    else
        CI_SETUP_TEMPLATE=1
    fi
fi

if [[ "$CI_SETUP_TEMPLATE" == "1" ]]; then
    run_ci_template_setup
else
    echo "Skipping CI base template setup; debian-13-x64-server-ludus-ci-template already exists."
fi

if [[ "$CI_SETUP_SEEDS" == "1" ]]; then
    run_seed_setup
fi

sync_ci_files

echo "Ludus CI host bootstrap complete."
