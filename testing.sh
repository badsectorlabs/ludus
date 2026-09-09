#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
STATE_FILE=${LUDUS_TESTING_STATE_FILE:-"$SCRIPT_DIR/.ludus-testing-vm.json"}
TUNNEL_STATE_FILE=${LUDUS_TESTING_TUNNEL_STATE_FILE:-"$SCRIPT_DIR/.ludus-testing-tunnel.json"}
TUNNEL_TMP_ROOT=${TMPDIR:-/tmp}
TUNNEL_TMP_ROOT=${TUNNEL_TMP_ROOT%/}
PROXMOX_HOST=${LUDUS_TESTING_PROXMOX_HOST:-}
PVE_CONFIG_FILE=${LUDUS_TESTING_PVE_CONFIG:-"$HOME/.config/ludus/testing-pve.json"}
PVE_API_URL=${LUDUS_TESTING_PVE_API_URL:-}
PVE_TOKEN_ID=${LUDUS_TESTING_PVE_TOKEN_ID:-}
PVE_TOKEN_SECRET=${LUDUS_TESTING_PVE_TOKEN_SECRET:-}
PVE_CA_FILE=${LUDUS_TESTING_PVE_CA_FILE:-}
PVE_INSECURE=${LUDUS_TESTING_PVE_INSECURE:-false}
PVE_CURL_TLS_MODE=system
RELEASE_API="https://gitlab.com/api/v4/projects/54052321/releases/permalink/latest"
AVAILABLE_TAG="available"
IN_USE_TAG="in-use"

RELEASE_ACTIVE=false
RELEASE_NODE=""
RELEASE_VMID=""
RELEASE_BRANCH_TAG=""

usage() {
    cat <<EOF
Usage: $0 [-H <proxmox-host>] list
       $0 [-H <proxmox-host>] checkout <VMID> [--ludus-port <port>] [--proxmox-port <port>] [--ssh-port <port>]
       $0 [-H <proxmox-host>] release <VMID>
       $0 status
       $0 [-H <proxmox-host>] tunnel start
       $0 [-H <proxmox-host>] tunnel stop

Options:
  -H <host>        Proxmox SSH host; overrides the environment and JSON config

Environment:
  LUDUS_TESTING_PROXMOX_HOST  Default Proxmox SSH host; overrides JSON config
  LUDUS_TESTING_PVE_CONFIG    JSON config file (default: ~/.config/ludus/testing-pve.json)
  LUDUS_TESTING_PVE_API_URL   Proxmox API base URL
  LUDUS_TESTING_PVE_TOKEN_ID  Proxmox API token ID
  LUDUS_TESTING_PVE_TOKEN_SECRET  Proxmox API token secret
  LUDUS_TESTING_PVE_CA_FILE   Optional Proxmox CA certificate
  LUDUS_TESTING_PVE_INSECURE  Skip API certificate validation (default: false)

Commands:
  list             List available Ludus test VMs
  checkout <VMID>  Reserve a VM and select its local tunnel ports
  release <VMID>   Restore, update, snapshot if needed, and release a VM
  tunnel start     Forward test VM ports 8080, 8006, and 22 to localhost
  tunnel stop      Stop the test VM SSH tunnel
  status           Show this worktree's VM checkout and dev tunnel status
EOF
}

die() {
    echo "Error: $*" >&2
    exit 1
}

require_commands() {
    local command_name
    for command_name in "$@"; do
        command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
    done
}

load_proxmox_host() {
    local config_host

    [ -z "$PROXMOX_HOST" ] || return
    [ -f "$PVE_CONFIG_FILE" ] || return

    config_host=$(jq -er '
        if .version != 1 then error("unsupported version") else . end
        | (.ssh_host // "")
        | if type == "string" then . else error("ssh_host must be a string") end
    ' "$PVE_CONFIG_FILE") || die "invalid Proxmox SSH host config at $PVE_CONFIG_FILE"
    PROXMOX_HOST=$config_host
}

load_pve_config() {
    local config

    if [ -f "$PVE_CONFIG_FILE" ]; then
        config=$(jq -ce '
            select(
                .version == 1 and
                (.api_url | type) == "string" and
                (.token_id | type) == "string" and
                (.token_secret | type) == "string" and
                ((.ssh_host // "") | type) == "string"
            )
        ' "$PVE_CONFIG_FILE") || die "invalid Proxmox API config at $PVE_CONFIG_FILE"
        [ -n "$PVE_API_URL" ] || PVE_API_URL=$(printf '%s\n' "$config" | jq -r '.api_url')
        [ -n "$PVE_TOKEN_ID" ] || PVE_TOKEN_ID=$(printf '%s\n' "$config" | jq -r '.token_id')
        [ -n "$PVE_TOKEN_SECRET" ] || PVE_TOKEN_SECRET=$(printf '%s\n' "$config" | jq -r '.token_secret')
        [ -n "$PVE_CA_FILE" ] || PVE_CA_FILE=$(printf '%s\n' "$config" | jq -r '.ca_file // empty')
    fi

    PVE_API_URL=${PVE_API_URL%/}
    case "$PVE_API_URL" in
        https://*) ;;
        *) die "a valid HTTPS Proxmox API URL is required" ;;
    esac
    [ -n "$PVE_TOKEN_ID" ] || die "a Proxmox API token ID is required"
    [ -n "$PVE_TOKEN_SECRET" ] || die "a Proxmox API token secret is required"

    PVE_CURL_TLS_MODE=system
    case "$PVE_INSECURE" in
        1|true|TRUE|yes|YES)
            PVE_CURL_TLS_MODE=insecure
            ;;
        0|false|FALSE|no|NO|'')
            if [ -n "$PVE_CA_FILE" ]; then
                [ -r "$PVE_CA_FILE" ] || die "Proxmox CA file is not readable: $PVE_CA_FILE"
                PVE_CURL_TLS_MODE=ca-file
            fi
            ;;
        *)
            die "LUDUS_TESTING_PVE_INSECURE must be true or false"
            ;;
    esac
}

validate_vmid() {
    case "$1" in
        ''|*[!0-9]*) die "VMID must be a positive integer" ;;
    esac
    [ "$1" -gt 0 ] || die "VMID must be a positive integer"
}

validate_local_port() {
    local port=$1
    local label=$2

    case "$port" in
        ''|*[!0-9]*) die "$label port must be an integer between 1 and 65535" ;;
    esac
    [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || \
        die "$label port must be an integer between 1 and 65535"
}

port_in_use() {
    nc -z 127.0.0.1 "$1" >/dev/null 2>&1
}

choose_local_port() {
    local candidate=$1
    local explicit=$2
    local label=$3
    local used_one=${4:-}
    local used_two=${5:-}

    validate_local_port "$candidate" "$label"
    if [ "$explicit" = true ]; then
        [ "$candidate" != "$used_one" ] && [ "$candidate" != "$used_two" ] || \
            die "$label port $candidate is already selected for another tunnel"
        if port_in_use "$candidate"; then
            die "$label port $candidate is already in use"
        fi
        SELECTED_PORT=$candidate
        return
    fi

    while [ "$candidate" -le 65535 ]; do
        if [ "$candidate" != "$used_one" ] && [ "$candidate" != "$used_two" ] && \
            ! port_in_use "$candidate"; then
            SELECTED_PORT=$candidate
            return
        fi
        candidate=$((candidate + 1))
    done

    die "no available $label port found"
}

pve_api() {
    local method=$1
    local path=$2
    local response
    local curl_args

    shift 2
    curl_args=(-fsS
        --request "$method"
        --header "Authorization: PVEAPIToken=${PVE_TOKEN_ID}=${PVE_TOKEN_SECRET}")
    case "$PVE_CURL_TLS_MODE" in
        insecure) curl_args+=(--insecure) ;;
        ca-file) curl_args+=(--cacert "$PVE_CA_FILE") ;;
    esac
    response=$(curl "${curl_args[@]}" \
        "$@" \
        "${PVE_API_URL}/api2/json${path}") || return 1
    printf '%s\n' "$response" | jq -c '
        if has("data") then .data else error("Proxmox API response has no data field") end
    '
}

urlencode() {
    jq -rn --arg value "$1" '$value | @uri'
}

wait_for_pve_task() {
    local node=$1
    local upid=$2
    local attempts=${LUDUS_TESTING_PVE_TASK_ATTEMPTS:-180}
    local delay=${LUDUS_TESTING_PVE_TASK_DELAY:-5}
    local encoded_upid task_status status exit_status
    local attempt=0

    encoded_upid=$(urlencode "$upid")
    while [ "$attempt" -lt "$attempts" ]; do
        task_status=$(pve_api GET "/nodes/${node}/tasks/${encoded_upid}/status")
        status=$(printf '%s\n' "$task_status" | jq -r '.status')
        if [ "$status" = "stopped" ]; then
            exit_status=$(printf '%s\n' "$task_status" | jq -r '.exitstatus // empty')
            [ "$exit_status" = "OK" ] || {
                echo "Error: Proxmox task failed: ${exit_status:-unknown error}" >&2
                return 1
            }
            return 0
        fi
        attempt=$((attempt + 1))
        sleep "$delay"
    done

    echo "Error: timed out waiting for Proxmox task $upid" >&2
    return 1
}

run_pve_task() {
    local node=$1
    local method=$2
    local path=$3
    local upid

    shift 3
    upid=$(pve_api "$method" "$path" "$@" | jq -er '
        select(type == "string" and length > 0)
    ') || {
        echo "Error: Proxmox API did not return a task ID for $path" >&2
        return 1
    }
    wait_for_pve_task "$node" "$upid"
}

get_resources() {
    pve_api GET /cluster/resources --get --data-urlencode type=vm
}

get_vm_config() {
    local node=$1
    local vmid=$2
    pve_api GET "/nodes/${node}/qemu/${vmid}/config"
}

get_vm_snapshots() {
    local node=$1
    local vmid=$2
    pve_api GET "/nodes/${node}/qemu/${vmid}/snapshot"
}

set_vm_tags() {
    local node=$1
    local vmid=$2
    local tags=$3
    local digest=$4
    pve_api PUT "/nodes/${node}/qemu/${vmid}/config" \
        --data-urlencode "tags=$tags" \
        --data-urlencode "digest=$digest" >/dev/null
}

find_vm_resource() {
    local resources=$1
    local vmid=$2
    printf '%s\n' "$resources" | jq -ce --argjson vmid "$vmid" '
        [.[] | select(
            .type == "qemu" and
            ((.template // 0) | tonumber) == 0 and
            ((.vmid | tonumber) == $vmid)
        )] |
        if length == 1 then .[0] else empty end
    '
}

config_test_ip() {
    local config=$1
    printf '%s\n' "$config" | jq -er '
        .description as $description |
        (try ($description | fromjson) catch null) |
        select(type == "object" and .role == "test-instance") |
        .ip |
        select(type == "string" and length > 0)
    '
}

config_has_tag() {
    local config=$1
    local tag=$2
    printf '%s\n' "$config" | jq -e --arg tag "$tag" '
        ((.tags // "") | split(";") | index($tag)) != null
    ' >/dev/null
}

branch_tag_for() {
    local branch=$1
    local tag
    local first

    tag=$(printf '%s' "$branch" | LC_ALL=C tr -c 'A-Za-z0-9_.+-' '-')
    [ -n "$tag" ] || return 1
    first=${tag%"${tag#?}"}
    case "$first" in
        [A-Za-z0-9_]) ;;
        *) tag="_${tag}" ;;
    esac

    case "$tag" in
        "$AVAILABLE_TAG"|"$IN_USE_TAG") return 1 ;;
    esac

    printf '%s\n' "$tag"
}

write_state() {
    local vmid=$1
    local node=$2
    local name=$3
    local ip=$4
    local branch=$5
    local branch_tag=$6
    local ludus_port=$7
    local proxmox_port=$8
    local ssh_port=$9
    local temp_file

    temp_file=$(mktemp "${STATE_FILE}.tmp.XXXXXX") || return 1
    chmod 600 "$temp_file"
    if ! jq -n \
        --arg proxmox_host "$PROXMOX_HOST" \
        --arg node "$node" \
        --argjson vmid "$vmid" \
        --arg name "$name" \
        --arg ip "$ip" \
        --arg branch "$branch" \
        --arg branch_tag "$branch_tag" \
        --argjson ludus_port "$ludus_port" \
        --argjson proxmox_port "$proxmox_port" \
        --argjson ssh_port "$ssh_port" \
        --arg checked_out_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
        '{
            version: 1,
            proxmox_host: $proxmox_host,
            node: $node,
            vmid: $vmid,
            name: $name,
            ip: $ip,
            branch: $branch,
            branch_tag: $branch_tag,
            ports: {
                ludus: $ludus_port,
                proxmox: $proxmox_port,
                ssh: $ssh_port
            },
            checked_out_at: $checked_out_at
        }' >"$temp_file"; then
        rm -f "$temp_file"
        return 1
    fi

    if ! mv "$temp_file" "$STATE_FILE"; then
        rm -f "$temp_file"
        return 1
    fi
}

mark_checked_out() {
    local node=$1
    local vmid=$2
    local branch_tag=$3
    local config
    local digest

    config=$(get_vm_config "$node" "$vmid")
    config_test_ip "$config" >/dev/null
    digest=$(printf '%s\n' "$config" | jq -er '.digest')
    set_vm_tags "$node" "$vmid" "${IN_USE_TAG};${branch_tag}" "$digest"
}

list_vms() {
    local resources
    local candidates
    local found=false
    local vmid node name status config ip

    resources=$(get_resources)
    candidates=$(printf '%s\n' "$resources" | jq -r --arg tag "$AVAILABLE_TAG" '
        [.[] | select(
            .type == "qemu" and
            ((.template // 0) | tonumber) == 0 and
            (((.tags // "") | split(";") | index($tag)) != null)
        )] |
        sort_by(.vmid | tonumber)[] |
        [.vmid, .node, .name, .status] | @tsv
    ')

    printf '%-6s %-20s %-10s %s\n' "VMID" "NAME" "STATUS" "IP"
    while IFS=$'\t' read -r vmid node name status; do
        [ -n "$vmid" ] || continue
        ip="-"
        if config=$(get_vm_config "$node" "$vmid"); then
            ip=$(config_test_ip "$config") || ip="-"
        fi
        printf '%-6s %-20s %-10s %s\n' "$vmid" "$name" "$status" "$ip"
        found=true
    done <<EOF
$candidates
EOF

    if [ "$found" = false ]; then
        echo "No available Ludus test VMs."
    fi
}

checkout_vm() {
    local vmid=$1
    local ludus_port=$2
    local proxmox_port=$3
    local ssh_port=$4
    local resources resource node name status config ip digest
    local branch branch_tag cleanup_config cleanup_digest

    [ ! -e "$STATE_FILE" ] || die "a VM is already checked out in this worktree; release it first (${STATE_FILE})"

    branch=$(git -C "$SCRIPT_DIR" symbolic-ref --quiet --short HEAD) || \
        die "checkout requires a named Git branch (detached HEAD is not supported)"
    branch_tag=$(branch_tag_for "$branch") || \
        die "branch name cannot be represented safely as a Proxmox tag: $branch"

    resources=$(get_resources)
    resource=$(find_vm_resource "$resources" "$vmid") || die "VM $vmid is not a Ludus test VM"
    node=$(printf '%s\n' "$resource" | jq -er '.node')
    name=$(printf '%s\n' "$resource" | jq -er '.name')
    status=$(printf '%s\n' "$resource" | jq -er '.status')
    [ "$status" = "running" ] || die "VM $vmid is $status; only running test VMs can be checked out"

    config=$(get_vm_config "$node" "$vmid")
    ip=$(config_test_ip "$config") || die "VM $vmid is not configured with a test-instance role and IP"
    config_has_tag "$config" "$AVAILABLE_TAG" || die "VM $vmid is not available"
    digest=$(printf '%s\n' "$config" | jq -er '.digest')

    set_vm_tags "$node" "$vmid" "${IN_USE_TAG};${branch_tag}" "$digest"

    if ! write_state "$vmid" "$node" "$name" "$ip" "$branch" "$branch_tag" \
        "$ludus_port" "$proxmox_port" "$ssh_port"; then
        echo "Error: could not write checkout state; returning VM $vmid to the pool" >&2
        if cleanup_config=$(get_vm_config "$node" "$vmid") && \
            cleanup_digest=$(printf '%s\n' "$cleanup_config" | jq -er '.digest'); then
            set_vm_tags "$node" "$vmid" "$AVAILABLE_TAG" "$cleanup_digest" || \
                echo "Warning: failed to restore the available tag on VM $vmid" >&2
        fi
        exit 1
    fi

    echo "Checked out VM $vmid ($name, $ip) for branch $branch."
    if [ "$branch_tag" != "$branch" ]; then
        echo "Proxmox branch tag: $branch_tag"
    fi
    echo "Local ports: Ludus $ludus_port, Proxmox $proxmox_port, SSH $ssh_port"
    echo "Run ./dev.sh without -t to sync and build on this VM."
}

checkout_command() {
    local vmid=""
    local ludus_port=8080
    local proxmox_port=8006
    local ssh_port=2222
    local ludus_explicit=false
    local proxmox_explicit=false
    local ssh_explicit=false

    shift
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --ludus-port)
                [ "$#" -ge 2 ] || die "--ludus-port requires a value"
                ludus_port=$2
                ludus_explicit=true
                shift 2
                ;;
            --ludus-port=*)
                ludus_port=${1#*=}
                ludus_explicit=true
                shift
                ;;
            --proxmox-port)
                [ "$#" -ge 2 ] || die "--proxmox-port requires a value"
                proxmox_port=$2
                proxmox_explicit=true
                shift 2
                ;;
            --proxmox-port=*)
                proxmox_port=${1#*=}
                proxmox_explicit=true
                shift
                ;;
            --ssh-port)
                [ "$#" -ge 2 ] || die "--ssh-port requires a value"
                ssh_port=$2
                ssh_explicit=true
                shift 2
                ;;
            --ssh-port=*)
                ssh_port=${1#*=}
                ssh_explicit=true
                shift
                ;;
            -*)
                die "unknown checkout option: $1"
                ;;
            *)
                [ -z "$vmid" ] || die "checkout accepts exactly one VMID"
                vmid=$1
                shift
                ;;
        esac
    done

    [ -n "$vmid" ] || die "checkout requires a VMID"
    validate_vmid "$vmid"

    choose_local_port "$ludus_port" "$ludus_explicit" "Ludus"
    ludus_port=$SELECTED_PORT
    choose_local_port "$proxmox_port" "$proxmox_explicit" "Proxmox" "$ludus_port"
    proxmox_port=$SELECTED_PORT
    choose_local_port "$ssh_port" "$ssh_explicit" "SSH" "$ludus_port" "$proxmox_port"
    ssh_port=$SELECTED_PORT

    checkout_vm "$vmid" "$ludus_port" "$proxmox_port" "$ssh_port"
}

read_tunnel_state() {
    jq -ce '
        select(
            .version == 1 and
            (.vmid | type) == "number" and
            (.ip | type) == "string" and
            (.proxmox_host | type) == "string" and
            (.target | type) == "string" and
            (.control_socket | type) == "string" and
            (.ports.ludus | type) == "number" and
            (.ports.proxmox | type) == "number" and
            (.ports.ssh | type) == "number" and
            (.started_at | type) == "string"
        )
    ' "$TUNNEL_STATE_FILE"
}

write_tunnel_state() {
    local vmid=$1
    local ip=$2
    local target=$3
    local control_socket=$4
    local ludus_port=$5
    local proxmox_port=$6
    local ssh_port=$7
    local temp_file

    temp_file=$(mktemp "${TUNNEL_STATE_FILE}.tmp.XXXXXX") || return 1
    chmod 600 "$temp_file"
    if ! jq -n \
        --argjson vmid "$vmid" \
        --arg ip "$ip" \
        --arg proxmox_host "$PROXMOX_HOST" \
        --arg target "$target" \
        --arg control_socket "$control_socket" \
        --argjson ludus_port "$ludus_port" \
        --argjson proxmox_port "$proxmox_port" \
        --argjson ssh_port "$ssh_port" \
        --arg started_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
        '{
            version: 1,
            vmid: $vmid,
            ip: $ip,
            proxmox_host: $proxmox_host,
            target: $target,
            control_socket: $control_socket,
            ports: {
                ludus: $ludus_port,
                proxmox: $proxmox_port,
                ssh: $ssh_port
            },
            started_at: $started_at
        }' >"$temp_file"; then
        rm -f "$temp_file"
        return 1
    fi

    if ! mv "$temp_file" "$TUNNEL_STATE_FILE"; then
        rm -f "$temp_file"
        return 1
    fi
}

cleanup_tunnel_files() {
    local control_socket=${1:-}
    local tunnel_directory

    rm -f "$TUNNEL_STATE_FILE"
    case "$control_socket" in
        "$TUNNEL_TMP_ROOT"/ludus-testing-tunnel.*/control)
            rm -f "$control_socket"
            tunnel_directory=${control_socket%/control}
            rmdir "$tunnel_directory" 2>/dev/null || true
            ;;
    esac
}

tunnel_start() {
    local checkout_state checkout_host vmid ip target
    local ludus_port proxmox_port ssh_port
    local tunnel_state control_socket tunnel_directory

    if [ -f "$TUNNEL_STATE_FILE" ]; then
        tunnel_state=$(read_tunnel_state) || die "invalid tunnel state at $TUNNEL_STATE_FILE"
        control_socket=$(printf '%s\n' "$tunnel_state" | jq -er '.control_socket')
        target=$(printf '%s\n' "$tunnel_state" | jq -er '.target')
        if ssh -S "$control_socket" -O check "$target" >/dev/null 2>&1; then
            die "an SSH tunnel is already running"
        fi
        cleanup_tunnel_files "$control_socket"
    fi

    [ -f "$STATE_FILE" ] || die "no VM checkout state found at $STATE_FILE"
    checkout_state=$(jq -ce '
        select(
            .version == 1 and
            (.vmid | type) == "number" and
            (.ip | type) == "string" and
            (.proxmox_host | type) == "string" and
            (.ports.ludus | type) == "number" and
            (.ports.proxmox | type) == "number" and
            (.ports.ssh | type) == "number"
        )
    ' "$STATE_FILE") || die "invalid VM checkout state at $STATE_FILE"

    checkout_host=$(printf '%s\n' "$checkout_state" | jq -er '.proxmox_host')
    [ "$PROXMOX_HOST" = "$checkout_host" ] || \
        die "Proxmox host $PROXMOX_HOST does not match checkout host $checkout_host"
    vmid=$(printf '%s\n' "$checkout_state" | jq -r '.vmid')
    ip=$(printf '%s\n' "$checkout_state" | jq -er '.ip')
    ludus_port=$(printf '%s\n' "$checkout_state" | jq -r '.ports.ludus')
    proxmox_port=$(printf '%s\n' "$checkout_state" | jq -r '.ports.proxmox')
    ssh_port=$(printf '%s\n' "$checkout_state" | jq -r '.ports.ssh')
    case "$ip" in
        ''|*[!A-Za-z0-9._:-]*) die "invalid test VM IP in $STATE_FILE" ;;
    esac

    target="root@$ip"
    tunnel_directory=$(mktemp -d "${TUNNEL_TMP_ROOT}/ludus-testing-tunnel.XXXXXX") || \
        die "could not create tunnel control directory"
    control_socket="$tunnel_directory/control"

    if ! ssh -M -S "$control_socket" -f -N \
        -o BatchMode=yes \
        -o StrictHostKeyChecking=accept-new \
        -o ExitOnForwardFailure=yes \
        -J "$PROXMOX_HOST" \
        -L "127.0.0.1:${ludus_port}:127.0.0.1:8080" \
        -L "127.0.0.1:${proxmox_port}:127.0.0.1:8006" \
        -L "127.0.0.1:${ssh_port}:127.0.0.1:22" \
        "$target"; then
        cleanup_tunnel_files "$control_socket"
        die "could not start the SSH tunnel"
    fi

    if ! write_tunnel_state "$vmid" "$ip" "$target" "$control_socket" \
        "$ludus_port" "$proxmox_port" "$ssh_port"; then
        ssh -S "$control_socket" -O exit "$target" >/dev/null 2>&1 || true
        cleanup_tunnel_files "$control_socket"
        die "could not record the SSH tunnel state"
    fi

    echo "SSH tunnel started for VM $vmid ($ip):"
    echo "  localhost:$ludus_port -> 127.0.0.1:8080"
    echo "  localhost:$proxmox_port -> 127.0.0.1:8006"
    echo "  localhost:$ssh_port -> 127.0.0.1:22"
}

tunnel_stop() {
    local tunnel_state state_host target control_socket

    if [ ! -f "$TUNNEL_STATE_FILE" ]; then
        echo "No SSH tunnel is recorded."
        return 0
    fi

    tunnel_state=$(read_tunnel_state) || die "invalid tunnel state at $TUNNEL_STATE_FILE"
    state_host=$(printf '%s\n' "$tunnel_state" | jq -er '.proxmox_host')
    [ "$PROXMOX_HOST" = "$state_host" ] || \
        die "Proxmox host $PROXMOX_HOST does not match tunnel host $state_host"
    target=$(printf '%s\n' "$tunnel_state" | jq -er '.target')
    control_socket=$(printf '%s\n' "$tunnel_state" | jq -er '.control_socket')

    if ssh -S "$control_socket" -O check "$target" >/dev/null 2>&1; then
        ssh -S "$control_socket" -O exit "$target" >/dev/null
        echo "SSH tunnel stopped."
    else
        echo "SSH tunnel is not running; removing stale state."
    fi

    cleanup_tunnel_files "$control_socket"
}

testing_status() {
    local status_code=0
    local checkout_state tunnel_state
    local target control_socket
    local ludus_port proxmox_port ssh_port

    echo "VM checkout:"
    if [ ! -f "$STATE_FILE" ]; then
        echo "  status: none"
    elif ! checkout_state=$(jq -ce '
        select(
            .version == 1 and
            (.vmid | type) == "number" and
            (.name | type) == "string" and
            (.ip | type) == "string" and
            (.branch | type) == "string" and
            (.branch_tag | type) == "string" and
            (.proxmox_host | type) == "string" and
            (.ports.ludus | type) == "number" and
            (.ports.proxmox | type) == "number" and
            (.ports.ssh | type) == "number" and
            (.checked_out_at | type) == "string"
        )
    ' "$STATE_FILE"); then
        echo "  status: invalid state"
        echo "  file: $STATE_FILE"
        status_code=1
    else
        echo "  status: checked out"
        printf '  VMID: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.vmid')"
        printf '  name: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.name')"
        printf '  IP: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.ip')"
        printf '  branch: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.branch')"
        printf '  branch tag: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.branch_tag')"
        printf '  Proxmox host: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.proxmox_host')"
        printf '  checked out at: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.checked_out_at')"
        printf '  local Ludus port: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.ports.ludus')"
        printf '  local Proxmox port: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.ports.proxmox')"
        printf '  local SSH port: %s\n' "$(printf '%s\n' "$checkout_state" | jq -r '.ports.ssh')"
    fi

    echo "Dev tunnel:"
    if [ ! -f "$TUNNEL_STATE_FILE" ]; then
        echo "  status: stopped"
    elif ! tunnel_state=$(read_tunnel_state); then
        echo "  status: invalid state"
        echo "  file: $TUNNEL_STATE_FILE"
        status_code=1
    else
        target=$(printf '%s\n' "$tunnel_state" | jq -er '.target')
        control_socket=$(printf '%s\n' "$tunnel_state" | jq -er '.control_socket')
        ludus_port=$(printf '%s\n' "$tunnel_state" | jq -r '.ports.ludus')
        proxmox_port=$(printf '%s\n' "$tunnel_state" | jq -r '.ports.proxmox')
        ssh_port=$(printf '%s\n' "$tunnel_state" | jq -r '.ports.ssh')
        if ssh -S "$control_socket" -O check "$target" >/dev/null 2>&1; then
            echo "  status: running"
        else
            echo "  status: stale"
            status_code=1
        fi
        printf '  VMID: %s\n' "$(printf '%s\n' "$tunnel_state" | jq -r '.vmid')"
        printf '  IP: %s\n' "$(printf '%s\n' "$tunnel_state" | jq -r '.ip')"
        printf '  Proxmox host: %s\n' "$(printf '%s\n' "$tunnel_state" | jq -r '.proxmox_host')"
        printf '  started at: %s\n' "$(printf '%s\n' "$tunnel_state" | jq -r '.started_at')"
        echo "  localhost:$ludus_port -> 127.0.0.1:8080"
        echo "  localhost:$proxmox_port -> 127.0.0.1:8006"
        echo "  localhost:$ssh_port -> 127.0.0.1:22"
    fi

    return "$status_code"
}

latest_public_release() {
    curl -fsSL "$RELEASE_API" | jq -er '.tag_name | select(type == "string" and length > 0)'
}

latest_release_snapshot() {
    local snapshots=$1
    printf '%s\n' "$snapshots" | jq -er '
        [.[] | select((.name // "") | test("^ludus-v"))] |
        if length == 0 then empty else max_by((.snaptime // 0) | tonumber).name end
    '
}

ssh_guest() {
    local stdin_mode=$1
    local ip=$2
    local remote_command=""
    local argument quoted_argument

    shift 2
    for argument in "$@"; do
        printf -v quoted_argument '%q' "$argument"
        if [ -n "$remote_command" ]; then
            remote_command="${remote_command} ${quoted_argument}"
        else
            remote_command=$quoted_argument
        fi
    done

    if [ "$stdin_mode" = "with-stdin" ]; then
        ssh -J "$PROXMOX_HOST" \
            -o BatchMode=yes \
            -o StrictHostKeyChecking=accept-new \
            "root@$ip" "$remote_command"
    else
        ssh -n -J "$PROXMOX_HOST" \
            -o BatchMode=yes \
            -o StrictHostKeyChecking=accept-new \
            "root@$ip" "$remote_command"
    fi
}

ensure_vm_running() {
    local node=$1
    local vmid=$2
    local status

    status=$(pve_api GET "/nodes/${node}/qemu/${vmid}/status/current" | jq -er '.status')
    case "$status" in
        running)
            return 0
            ;;
        stopped)
            echo "Starting VM $vmid..."
            run_pve_task "$node" POST "/nodes/${node}/qemu/${vmid}/status/start"
            ;;
        *)
            echo "Error: could not determine VM $vmid status: $status" >&2
            return 1
            ;;
    esac
}

wait_for_guest_ssh() {
    local ip=$1
    local attempts=${LUDUS_TESTING_SSH_ATTEMPTS:-60}
    local delay=${LUDUS_TESTING_SSH_DELAY:-5}
    local attempt=0

    while [ "$attempt" -lt "$attempts" ]; do
        if ssh_guest no-stdin "$ip" true >/dev/null 2>&1; then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep "$delay"
    done

    echo "Error: SSH did not become ready for test VM $ip" >&2
    return 1
}

update_guest() {
    local ip=$1
    local release=$2
    local guest_script result

    guest_script=$(cat <<'GUEST_SCRIPT'
set -euo pipefail

release=$1
project_id=54052321
package_root="https://gitlab.com/api/v4/projects/${project_id}/packages/generic/ludus"
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT
package_name="ludus-server-${release}"
server_name="ludus-server"
checksum_name="ludus_${release}_checksums.txt"

curl -fsSL "${package_root}/${release}/${package_name}" -o "${temp_dir}/${server_name}"
curl -fsSL "${package_root}/${release}/${checksum_name}" -o "${temp_dir}/${checksum_name}"
expected=$(awk -v filename="$server_name" '$2 == filename || $2 == "*" filename { print $1; exit }' "${temp_dir}/${checksum_name}")
[ -n "$expected" ] || { echo "No checksum found for ${server_name}" >&2; exit 1; }
actual=$(sha256sum "${temp_dir}/${server_name}" | awk '{ print $1 }')
[ "$actual" = "$expected" ] || { echo "Checksum mismatch for ${server_name}" >&2; exit 1; }
chmod +x "${temp_dir}/${server_name}"
"${temp_dir}/${server_name}" --update
GUEST_SCRIPT
)

    if ! result=$(printf '%s\n' "$guest_script" | \
        ssh_guest with-stdin "$ip" /bin/bash -s -- "$release"); then
        echo "Error: failed to execute the public release update in VM $ip" >&2
        return 1
    fi

    printf '%s\n' "$result"
}

restore_checkout_tags() {
    local config digest

    [ -n "$RELEASE_NODE" ] && [ -n "$RELEASE_VMID" ] && [ -n "$RELEASE_BRANCH_TAG" ] || return 1
    config=$(get_vm_config "$RELEASE_NODE" "$RELEASE_VMID") || return 1
    digest=$(printf '%s\n' "$config" | jq -er '.digest') || return 1
    set_vm_tags "$RELEASE_NODE" "$RELEASE_VMID" \
        "${IN_USE_TAG};${RELEASE_BRANCH_TAG}" "$digest"
}

release_exit() {
    local exit_code=$?
    trap - EXIT

    if [ "$RELEASE_ACTIVE" = true ]; then
        set +e
        echo "Release did not complete; VM $RELEASE_VMID remains checked out." >&2
        if ! restore_checkout_tags; then
            echo "Warning: could not restore the in-use tags on VM $RELEASE_VMID; inspect it on $PROXMOX_HOST." >&2
        fi
    fi

    exit "$exit_code"
}

release_vm() {
    local requested_vmid=$1
    local state state_vmid state_ip state_host resource resources config ip snapshots snapshot
    local release snapshot_release_name digest encoded_snapshot

    [ -f "$STATE_FILE" ] || die "no VM checkout state found at $STATE_FILE"
    state=$(jq -ce '
        select(
            .version == 1 and
            (.vmid | type) == "number" and
            (.ip | type) == "string" and
            (.proxmox_host | type) == "string" and
            (.branch_tag | type) == "string"
        )
    ' "$STATE_FILE") || die "invalid VM checkout state at $STATE_FILE"

    state_vmid=$(printf '%s\n' "$state" | jq -r '.vmid')
    [ "$requested_vmid" = "$state_vmid" ] || \
        die "VM $requested_vmid does not match this worktree's checked-out VM $state_vmid"

    state_host=$(printf '%s\n' "$state" | jq -er '.proxmox_host')
    [ "$PROXMOX_HOST" = "$state_host" ] || \
        die "Proxmox host $PROXMOX_HOST does not match checkout host $state_host"
    RELEASE_VMID=$state_vmid
    RELEASE_BRANCH_TAG=$(printf '%s\n' "$state" | jq -er '.branch_tag')
    state_ip=$(printf '%s\n' "$state" | jq -er '.ip')

    resources=$(get_resources)
    resource=$(find_vm_resource "$resources" "$state_vmid") || die "VM $state_vmid is not a Ludus test VM"
    RELEASE_NODE=$(printf '%s\n' "$resource" | jq -er '.node')
    config=$(get_vm_config "$RELEASE_NODE" "$state_vmid")
    ip=$(config_test_ip "$config") || die "VM $state_vmid no longer has a valid test-instance configuration"
    [ "$ip" = "$state_ip" ] || die "VM $state_vmid IP changed from $state_ip to $ip; refusing to release"
    config_has_tag "$config" "$IN_USE_TAG" || die "VM $state_vmid is not tagged $IN_USE_TAG"
    config_has_tag "$config" "$RELEASE_BRANCH_TAG" || \
        die "VM $state_vmid is not tagged for branch $RELEASE_BRANCH_TAG"

    digest=$(printf '%s\n' "$config" | jq -er '.digest')
    set_vm_tags "$RELEASE_NODE" "$state_vmid" \
        "${IN_USE_TAG};${RELEASE_BRANCH_TAG}" "$digest"
    RELEASE_ACTIVE=true

    release=$(latest_public_release)
    case "$release" in
        ''|*[!A-Za-z0-9._+-]*) die "latest public release has an unsafe tag: $release" ;;
    esac

    snapshots=$(get_vm_snapshots "$RELEASE_NODE" "$state_vmid")
    snapshot=$(latest_release_snapshot "$snapshots") || \
        die "VM $state_vmid has no ludus-v* snapshot to restore"

    snapshot_release_name=${release#v}
    snapshot_release_name=$(printf '%s' "$snapshot_release_name" | tr '.+' '--')
    snapshot_release_name="ludus-v${snapshot_release_name}"

    echo "Rolling VM $state_vmid back to $snapshot..."
    encoded_snapshot=$(urlencode "$snapshot")
    run_pve_task "$RELEASE_NODE" POST \
        "/nodes/${RELEASE_NODE}/qemu/${state_vmid}/snapshot/${encoded_snapshot}/rollback"

    # Snapshot rollback can restore old VM tags. Reassert ownership before any
    # fallible update work so a failed release never returns the VM to the pool.
    mark_checked_out "$RELEASE_NODE" "$state_vmid" "$RELEASE_BRANCH_TAG"
    ensure_vm_running "$RELEASE_NODE" "$state_vmid"
    wait_for_guest_ssh "$ip"

    echo "Updating VM $state_vmid to public Ludus release $release..."
    update_guest "$ip" "$release"

    if [ "$snapshot" != "$snapshot_release_name" ]; then
        echo "Creating release snapshot $snapshot_release_name..."
        run_pve_task "$RELEASE_NODE" POST \
            "/nodes/${RELEASE_NODE}/qemu/${state_vmid}/snapshot" \
            --data-urlencode "snapname=$snapshot_release_name" \
            --data-urlencode vmstate=1 \
            --data-urlencode "description=Ludus public release $release"
    fi

    config=$(get_vm_config "$RELEASE_NODE" "$state_vmid")
    config_test_ip "$config" >/dev/null
    digest=$(printf '%s\n' "$config" | jq -er '.digest')
    set_vm_tags "$RELEASE_NODE" "$state_vmid" "$AVAILABLE_TAG" "$digest"
    RELEASE_ACTIVE=false

    rm -f "$STATE_FILE"
    echo "Released VM $state_vmid at public Ludus release $release."
}

main() {
    local opt
    local command

    if [ "${1:-}" = "--help" ] || [ "${1:-}" = "help" ]; then
        usage
        exit 0
    fi

    while getopts ":H:h" opt; do
        case "$opt" in
            H) PROXMOX_HOST=$OPTARG ;;
            h)
                usage
                exit 0
                ;;
            :)
                die "option -$OPTARG requires a value"
                ;;
            \?)
                die "unknown option: -$OPTARG"
                ;;
        esac
    done
    shift $((OPTIND - 1))

    command=${1:-}
    case "$command" in
        list|checkout|release|tunnel)
            require_commands jq
            load_proxmox_host
            case "$PROXMOX_HOST" in
                ''|'-'*|*[!A-Za-z0-9._@:-]*)
                    die "a valid Proxmox SSH host is required with -H, LUDUS_TESTING_PROXMOX_HOST, or ssh_host in $PVE_CONFIG_FILE"
                    ;;
            esac
            ;;
    esac
    case "$command" in
        status)
            [ "$#" -eq 1 ] || { usage >&2; exit 1; }
            require_commands ssh jq
            testing_status
            ;;
        list)
            [ "$#" -eq 1 ] || { usage >&2; exit 1; }
            require_commands curl jq
            load_pve_config
            list_vms
            ;;
        checkout)
            require_commands curl jq git nc
            load_pve_config
            checkout_command "$@"
            ;;
        release)
            [ "$#" -eq 2 ] || { usage >&2; exit 1; }
            require_commands curl jq ssh
            load_pve_config
            validate_vmid "$2"
            trap release_exit EXIT
            release_vm "$2"
            ;;
        tunnel)
            [ "$#" -eq 2 ] || { usage >&2; exit 1; }
            require_commands ssh jq
            case "$2" in
                start) tunnel_start ;;
                stop) tunnel_stop ;;
                *)
                    usage >&2
                    exit 1
                    ;;
            esac
            ;;
        *)
            usage >&2
            exit 1
            ;;
    esac
}

main "$@"
