#!/usr/bin/env bash

# /opt/ludus/ci/base.sh

# Reject unknown profiles before performing host-side setup.
source "$(dirname "${BASH_SOURCE[0]}")/ci-profile.sh" || return 1

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

# Dynamic CI clone assignment directory (must exist on the Proxmox host)
export CI_ASSIGNMENT_DIR="/opt/ludus/ci/${CI_NAMESPACE}vm-assignments"
mkdir -p "$CI_ASSIGNMENT_DIR"

# Dynamic clones are built from Ludus seeds that have a static CI-network
# address. Give each clone its own static control IP before any SSH step runs.
export CI_IP_ASSIGNMENT_DIR=/opt/ludus/ci/ip-assignments
mkdir -p "$CI_IP_ASSIGNMENT_DIR"
export CI_CLONE_IP_PREFIX=${CI_CLONE_IP_PREFIX:-203.0.113}
export CI_CLONE_IP_START=${CI_CLONE_IP_START:-10}
export CI_CLONE_IP_END=${CI_CLONE_IP_END:-99}
export CI_CLONE_GATEWAY=${CI_CLONE_GATEWAY:-203.0.113.254}
export CI_CLONE_DNS_SERVERS=${CI_CLONE_DNS_SERVERS:-"1.1.1.1 8.8.8.8"}

# Backward-compatible name for cluster lock scripts. Pool locks are no
# longer used for non-cluster CI.
export POOL_ASSIGNMENT_DIR="/opt/ludus/ci/${CI_NAMESPACE}pool-assignments"
mkdir -p "$POOL_ASSIGNMENT_DIR"

# Pipeline ID for pool tracking
export PIPELINE_ID="${CUSTOM_ENV_CI_PIPELINE_ID}"

# --- Helper Functions ---

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

get_source_stage() {
    local BUILD_TYPE="$1"
    local SNAPSHOT_NAME="$2"

    if [[ "$BUILD_TYPE" == "full" ]]; then
        echo "base"
        return 0
    fi

    if [[ "$BUILD_TYPE" == "from-snapshot" ]]; then
        case "$SNAPSHOT_NAME" in
            "proxmox")           echo "base" ;;
            "legacy_migration")  echo "legacy-migration" ;;
            "clean_install")     echo "clean-install" ;;
            "templates_built")   echo "templates-built" ;;
            "range_built_admin") echo "range-admin" ;;
            "range_built_user")  echo "range-user" ;;
            "integration_ready") echo "integration" ;;
            *)
                echo "Error: Unknown LUDUS_SNAPSHOT_NAME: $SNAPSHOT_NAME" >&2
                return 1
                ;;
        esac
        return 0
    fi

    echo "Error: Cannot derive source stage for LUDUS_BUILD_TYPE: $BUILD_TYPE" >&2
    return 1
}

get_seed_vmid_for_stage() {
    local SOURCE_STAGE="$1"
    case "$SOURCE_STAGE" in
        "base")            echo "$CI_SEED_BASE_VMID" ;;
        "clean-install")   echo "$CI_SEED_CLEAN_INSTALL_VMID" ;;
        "templates-built") echo "$CI_SEED_TEMPLATES_BUILT_VMID" ;;
        "range-admin")     echo "$CI_SEED_RANGE_ADMIN_VMID" ;;
        "range-user")      echo "$CI_SEED_RANGE_USER_VMID" ;;
        "integration")     echo "$CI_SEED_INTEGRATION_VMID" ;;
        "legacy-migration")
            if [[ -z "$CI_SEED_LEGACY_MIGRATION_VMID" ]]; then
                echo "Error: legacy_migration is unavailable for profile $CI_PROFILE" >&2
                return 1
            fi
            echo "$CI_SEED_LEGACY_MIGRATION_VMID"
            ;;
        *)
            echo "Error: Unknown CI source stage: $SOURCE_STAGE" >&2
            return 1
            ;;
    esac
}

sanitize_slug() {
    tr '[:upper:]' '[:lower:]' \
        | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g'
}

get_ci_series() {
    if [[ -n "${CUSTOM_ENV_LUDUS_CI_SERIES:-}" ]]; then
        printf '%s\n' "$CUSTOM_ENV_LUDUS_CI_SERIES" | sanitize_slug
        return 0
    fi

    local JOB_NAME="${CUSTOM_ENV_CI_JOB_NAME:-unknown}"
    case "$JOB_NAME" in
        "install kickoff"|"install check")
            echo "install"
            ;;
        "templates build"|"templates check")
            echo "templates"
            ;;
        "client basic-commands")
            echo "client-basic"
            ;;
        "range deploy-admin"|"range check-admin")
            echo "range-admin"
            ;;
        post-deploy*"as-admin")
            echo "post-deploy-admin"
            ;;
        "range deploy-user"|"range check-user")
            echo "range-user"
            ;;
        post-deploy*"as-user")
            echo "post-deploy-user"
            ;;
        "test-everything")
            echo "integration"
            ;;
        *)
            printf '%s\n' "$JOB_NAME" | sanitize_slug
            ;;
    esac
}

get_assignment_file() {
    local SERIES="$1"
    echo "$CI_ASSIGNMENT_DIR/${PIPELINE_ID}-${SERIES}.env"
}

get_ip_assignment_file() {
    local SERIES="$1"
    echo "$CI_IP_ASSIGNMENT_DIR/${CI_NAMESPACE}${PIPELINE_ID}-${SERIES}.ip"
}

get_vm_name_by_vmid() {
    local VMID="$1"
    qm config "$VMID" 2>/dev/null | awk -F': ' '$1 == "name" { print $2; exit }'
}


# Check every assigned clone before any start, guest command, or destruction.
# Fixed fixtures from both generations remain protected even with overrides.
assert_ci_clone_vmid() {
    local VMID="$1"
    if [[ ! "$VMID" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: Invalid CI clone VMID '$VMID'" >&2
        return 1
    fi
    case "$VMID" in
        100[0-7]|1012|240[0-9]|241[0-2])
            echo "Error: Refusing to mutate protected fixture VM $VMID" >&2
            return 1
            ;;
    esac
    local SEED
    for SEED in "$CI_SEED_BASE_VMID" "$CI_SEED_CLEAN_INSTALL_VMID" \
        "$CI_SEED_TEMPLATES_BUILT_VMID" "$CI_SEED_RANGE_ADMIN_VMID" \
        "$CI_SEED_RANGE_USER_VMID" "$CI_SEED_INTEGRATION_VMID" \
        "$CI_SEED_LEGACY_MIGRATION_VMID" "$CLUSTER_NODE1_VMID" \
        "$CLUSTER_NODE2_VMID" "$BUILD_VMID"; do
        if [[ "$VMID" == "$SEED" ]]; then
            echo "Error: Refusing to mutate source/shared VM $VMID as a clone" >&2
            return 1
        fi
    done
    if (( VMID < CI_CLONE_MIN_VMID )); then
        echo "Error: Clone VM $VMID is below profile $CI_PROFILE minimum $CI_CLONE_MIN_VMID" >&2
        return 1
    fi
}

validate_ci_assignment() {
    local SERIES="$1"
    assert_ci_clone_vmid "${VM_ID:-}" || return 1
    if [[ "${VM_NAME:-}" != "${CI_VM_NAME_PREFIX}-${PIPELINE_ID}-${SERIES}-"* ]]; then
        echo "Error: Assignment has unexpected clone name '${VM_NAME:-}'" >&2
        return 1
    fi
}

next_ci_clone_vmid() {
    if [[ "$CI_PROFILE" == "lxc-2.4" ]]; then
        # nextid's --vmid checks one ID; it does not search from that ID.
        # Include all cluster VM/container IDs, not just the local node.
        pvesh get /cluster/resources --type vm --output-format=json \
            | jq -er --argjson minimum "$CI_CLONE_MIN_VMID" \
                'reduce ([.[].vmid | tonumber] | sort | .[]) as $used
                    ($minimum; if . == $used then . + 1 else . end)'
    else
        pvesh get /cluster/nextid
    fi
}

set_ci_control_link() {
    local VMID="$1"
    local LINK_DOWN="$2"
    local NET_CONFIG
    assert_ci_clone_vmid "$VMID" || return 1
    NET_CONFIG=$(qm config "$VMID" | awk -F': ' '$1 == "net0" { print $2; exit }') || return 1
    if [[ -z "$NET_CONFIG" ]]; then
        echo "Error: CI clone $VMID has no net0 control interface" >&2
        return 1
    fi
    if [[ "$NET_CONFIG" =~ (^|,)link_down=[01](,|$) ]]; then
        NET_CONFIG="${NET_CONFIG/link_down=0/link_down=$LINK_DOWN}"
        NET_CONFIG="${NET_CONFIG/link_down=1/link_down=$LINK_DOWN}"
    else
        NET_CONFIG="${NET_CONFIG},link_down=$LINK_DOWN"
    fi
    qm set "$VMID" --net0 "$NET_CONFIG" >/dev/null
}
load_ci_assignment() {
    local SERIES="$1"
    local ASSIGNMENT_FILE
    ASSIGNMENT_FILE=$(get_assignment_file "$SERIES")
    unset VM_ID VM_NAME VM_SOURCE_STAGE VM_SERIES VM_IP_STATIC
    if [[ -f "$ASSIGNMENT_FILE" ]]; then
        # shellcheck disable=SC1090
        source "$ASSIGNMENT_FILE"
        [[ -n "${VM_ID:-}" ]] && return 0
    fi
    return 1
}

allocate_ci_ip() {
    local SERIES="$1"
    local IP_FILE IP_LOCK CANDIDATE
    IP_FILE=$(get_ip_assignment_file "$SERIES")
    IP_LOCK="$CI_IP_ASSIGNMENT_DIR/.lock"

    (
        flock -x 9

        if [[ -f "$IP_FILE" ]]; then
            cat "$IP_FILE"
            exit 0
        fi

        for HOST_OCTET in $(seq "$CI_CLONE_IP_START" "$CI_CLONE_IP_END"); do
            CANDIDATE="${CI_CLONE_IP_PREFIX}.${HOST_OCTET}"
            if ! grep -R -Fxq "$CANDIDATE" "$CI_IP_ASSIGNMENT_DIR"/*.ip 2>/dev/null; then
                printf '%s\n' "$CANDIDATE" > "$IP_FILE"
                printf '%s\n' "$CANDIDATE"
                exit 0
            fi
        done

        echo "Error: No free CI clone IPs in ${CI_CLONE_IP_PREFIX}.${CI_CLONE_IP_START}-${CI_CLONE_IP_END}" >&2
        exit 1
    ) 9>"$IP_LOCK"
}

wait_for_guest_agent() {
    local VMID="$1"

    echo "Waiting for QEMU guest agent on VM $VMID..."
    for i in {1..90}; do
        if qm guest cmd "$VMID" ping >/dev/null 2>&1; then
            echo "QEMU guest agent is ready on VM $VMID"
            return 0
        fi
        sleep 2
    done

    echo "Error: QEMU guest agent did not become ready on VM $VMID after 180 seconds" >&2
    return 1
}

guest_exec_wait() {
    local VMID="$1"
    shift

    local OUTPUT EXITCODE ERRORS
    if ! OUTPUT=$(qm guest exec "$VMID" --timeout "${CI_GUEST_EXEC_TIMEOUT:-30}" -- "$@" 2>&1); then
        echo "$OUTPUT" >&2
        return 1
    fi

    EXITCODE=$(printf '%s' "$OUTPUT" | jq -r '.exitcode // empty' 2>/dev/null)
    if [[ -z "$EXITCODE" ]]; then
        echo "Error: Guest command on VM $VMID did not complete: $OUTPUT" >&2
        return 1
    fi
    if [[ "$EXITCODE" != "0" ]]; then
        ERRORS=$(printf '%s' "$OUTPUT" | jq -r '."err-data" // empty' 2>/dev/null)
        [[ -n "$ERRORS" ]] && echo "$ERRORS" >&2
        echo "$OUTPUT" >&2
        return "$EXITCODE"
    fi

    return 0
}

configure_ci_control_ip() {
    local VMID="$1"
    local IP="$2"
    local SCRIPT

    wait_for_guest_agent "$VMID" || return 1

    echo "Configuring CI control IP on VM $VMID: $IP/24"
    SCRIPT=$(cat <<EOF
set -e
cp /etc/network/interfaces /etc/network/interfaces.ludus-ci.bak 2>/dev/null || true
awk -v ip="${IP}/24" -v gw="${CI_CLONE_GATEWAY}" -v dns="${CI_CLONE_DNS_SERVERS}" '
    /^iface ens18 inet / {
        in_ens18=1
        saw_address=0
        saw_gateway=0
        saw_dns=0
        print "iface ens18 inet static"
        next
    }
    /^(auto|allow-|iface) / && in_ens18 {
        if (!saw_address) {
            print "\taddress " ip
        }
        if (!saw_gateway) {
            print "\tgateway " gw
        }
        if (!saw_dns) {
            print "\tdns-nameservers " dns
        }
        in_ens18=0
    }
    in_ens18 && /^[[:space:]]*address[[:space:]]+/ {
        print "\taddress " ip
        saw_address=1
        next
    }
    in_ens18 && /^[[:space:]]*gateway[[:space:]]+/ {
        print "\tgateway " gw
        saw_gateway=1
        next
    }
    in_ens18 && /^[[:space:]]*dns-nameservers[[:space:]]+/ {
        print "\tdns-nameservers " dns
        saw_dns=1
        next
    }
    in_ens18 && /^[[:space:]]*(dhcp-|dns-|pre-up|post-up|post-down|metric)[[:space:]]*/ {
        next
    }
    { print }
    END {
        if (in_ens18) {
            if (!saw_address) {
                print "\taddress " ip
            }
            if (!saw_gateway) {
                print "\tgateway " gw
            }
            if (!saw_dns) {
                print "\tdns-nameservers " dns
            }
        }
    }
' /etc/network/interfaces > /tmp/interfaces.ludus-ci
mv /tmp/interfaces.ludus-ci /etc/network/interfaces

if command -v dhcpcd >/dev/null 2>&1; then
    dhcpcd -k ens18 >/dev/null 2>&1 || true
fi
pkill -x dhcpcd >/dev/null 2>&1 || true
pkill -x dhclient >/dev/null 2>&1 || true

for dns_server in ${CI_CLONE_DNS_SERVERS}; do
    printf 'nameserver %s\n' "\$dns_server"
done > /tmp/resolv.conf.ludus-ci
if [ -d /etc/resolvconf/resolv.conf.d ]; then
    cp /tmp/resolv.conf.ludus-ci /etc/resolvconf/resolv.conf.d/head
    resolvconf -u || true
fi
cp /tmp/resolv.conf.ludus-ci /etc/resolv.conf
rm -f /tmp/resolv.conf.ludus-ci

PROXMOX_NODE_NAME=\$(awk -F': *' '\$1 == "proxmox_node" { print \$2; exit }' /opt/ludus/config.yml 2>/dev/null || true)
if [ -z "\$PROXMOX_NODE_NAME" ]; then
    PROXMOX_NODE_NAME=\$(hostname -s)
fi
HOSTNAME_SHORT=\$(hostname -s)
HOST_NAMES="\$PROXMOX_NODE_NAME"
if [ "\$HOSTNAME_SHORT" != "\$PROXMOX_NODE_NAME" ]; then
    HOST_NAMES="\$HOST_NAMES \$HOSTNAME_SHORT"
fi
awk -v n1="\$PROXMOX_NODE_NAME" -v n2="\$HOSTNAME_SHORT" '
    \$1 !~ /^127\./ {
        for (i = 2; i <= NF; i++) {
            if (\$i == n1 || \$i == n2) {
                next
            }
        }
    }
    { print }
' /etc/hosts > /tmp/hosts.ludus-ci
printf '%s\t%s\n' "${IP}" "\$HOST_NAMES" >> /tmp/hosts.ludus-ci
mv /tmp/hosts.ludus-ci /etc/hosts

if [ -f /opt/ludus/config.yml ]; then
    for entry in "proxmox_local_ip ${IP}" "proxmox_public_ip ${IP}" "proxmox_hostname 127.0.0.1" "proxmox_url https://127.0.0.1:8006"; do
        key="\${entry%% *}"
        value="\${entry#* }"
        if grep -qE "^\${key}:" /opt/ludus/config.yml; then
            sed -i -E "s#^\${key}:.*#\${key}: \${value}#" /opt/ludus/config.yml
        else
            printf '%s: %s\n' "\$key" "\$value" >> /opt/ludus/config.yml
        fi
    done
fi

ip addr flush dev ens18
ip link set ens18 up
ip addr add "${IP}/24" dev ens18
ip route flush dev ens18 proto dhcp 2>/dev/null || true
ip route replace default via "${CI_CLONE_GATEWAY}" dev ens18
EOF
)

    guest_exec_wait "$VMID" /bin/bash -lc "$SCRIPT" || return 1

    if [[ "$CI_PROFILE" == "lxc-2.4" ]]; then
        # Only relocate the public CI WireGuard address. Container Proxmox
        # endpoints must keep their private, nested-host-reachable addresses.
        SCRIPT=$(cat <<'EOF'
set -e
[[ -f /etc/ludus-lxc.json ]] || exit 0
CTID=$(python3 -c 'import json; v=json.load(open("/etc/ludus-lxc.json"))["vmid"]; assert type(v) is int and v > 0; print(v)')
for _ in {1..60}; do
    if pct status "$CTID" | grep -q 'status: running'; then
        break
    fi
    sleep 2
done
pct exec "$CTID" -- python3 - "$1" "$2" <<'PY'
import ipaddress
import os
from pathlib import Path
import re
import sys
import tempfile

path = Path("/opt/ludus/config.yml")
text = path.read_text()
match = re.search(r"^wireguard_endpoint:[ \t]*(?P<value>[^\r\n#]*)(?P<comment>#[^\r\n]*)?$", text, re.MULTILINE)
if match is None:
    raise SystemExit("LXC config is missing wireguard_endpoint")
endpoint = match["value"].strip().strip("\"'")
try:
    address = ipaddress.ip_address(endpoint)
except ValueError:
    sys.exit(0)  # Explicit DNS/public endpoints are not clone-local.
if address not in ipaddress.ip_network(sys.argv[2] + ".0/24") or endpoint == sys.argv[1]:
    sys.exit(0)
value = sys.argv[1] + (" " if match["comment"] else "")
text = text[:match.start("value")] + value + text[match.end("value"):]
stat = path.stat()
with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as temp:
    temporary = temp.name
    temp.write(text)
    os.fchmod(temp.fileno(), stat.st_mode)
    os.fchown(temp.fileno(), stat.st_uid, stat.st_gid)
try:
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
# The API watches config.yml and hot-reloads WireguardEndpoint on replacement.
print(f"Relocated LXC WireGuard endpoint: {endpoint} -> {sys.argv[1]}")
PY
EOF
)
        CI_GUEST_EXEC_TIMEOUT=180 guest_exec_wait "$VMID" timeout 150 /bin/bash -c "$SCRIPT" -- "$IP" "$CI_CLONE_IP_PREFIX" || return 1
    fi
}

wait_for_ci_vm_ssh() {
    local VMID="$1"
    local IP="$2"

    echo "Waiting for SSH on VM $VMID ($IP)..."
    for i in {1..90}; do
        if ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" "echo ready" 2>/dev/null; then
            echo "SSH is up on VM $VMID ($IP)"
            return 0
        fi
        sleep 5
    done

    echo "Error: SSH did not come up on VM $VMID ($IP) after 450 seconds" >&2
    return 1
}

wait_for_ludus_command() {
    local IP="$1"
    local KEY_FILE="$2"
    local CHECK_CMD="$3"
    local LABEL="$4"
    local REMOTE_COMMAND
    if [[ "$CI_PROFILE" == "lxc-2.4" ]]; then
        REMOTE_COMMAND="export LUDUS_CI_PROFILE=lxc-2.4; source /opt/ludus/ci/job-server.sh && ci_server_init && ci_client_config && LUDUS_API_KEY=\$(ci_server_exec cat '$KEY_FILE') && export LUDUS_API_KEY && $CHECK_CMD >/dev/null 2>&1"
    else
        REMOTE_COMMAND="test -f '$KEY_FILE' && LUDUS_API_KEY=\$(cat '$KEY_FILE') && export LUDUS_API_KEY && $CHECK_CMD >/dev/null 2>&1"
    fi

    echo "Waiting for Ludus API readiness on $IP ($LABEL)..."
    for i in {1..60}; do
        if ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -F /home/gitlab-runner/.ssh/config gitlab-runner@"$IP" \
            "$REMOTE_COMMAND"; then
            echo "Ludus API is ready on $IP ($LABEL)"
            return 0
        fi
        sleep 5
    done

    echo "Error: Ludus API did not become ready on $IP ($LABEL) after 300 seconds" >&2
    return 1
}

ensure_ci_vm() {
    local BUILD_TYPE="$CUSTOM_ENV_LUDUS_BUILD_TYPE"
    local SNAPSHOT_NAME="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"
    local SERIES SOURCE_STAGE SEED_VMID ASSIGNMENT_FILE LOCK_FILE

    SERIES=$(get_ci_series)
    SOURCE_STAGE=$(get_source_stage "$BUILD_TYPE" "$SNAPSHOT_NAME") || return 1
    SEED_VMID=$(get_seed_vmid_for_stage "$SOURCE_STAGE") || return 1
    ASSIGNMENT_FILE=$(get_assignment_file "$SERIES")
    LOCK_FILE="$CI_ASSIGNMENT_DIR/${PIPELINE_ID}-${SERIES}.lock"

    (
        flock -x 9

        if load_ci_assignment "$SERIES"; then
            validate_ci_assignment "$SERIES" || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            if qm status "$VM_ID" >/dev/null 2>&1; then
                local ACTUAL_VM_NAME
                ACTUAL_VM_NAME="$(get_vm_name_by_vmid "$VM_ID")"
                if [[ "$ACTUAL_VM_NAME" == "$VM_NAME" ]]; then
                    echo "Using existing CI clone for pipeline $PIPELINE_ID / series $SERIES: VM $VM_ID"
                    if [[ -z "${VM_IP_STATIC:-}" ]]; then
                        VM_IP_STATIC=$(allocate_ci_ip "$SERIES") || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
                        printf 'VM_IP_STATIC=%q\n' "$VM_IP_STATIC" >> "$ASSIGNMENT_FILE"
                    fi
                else
                    echo "Error: Assignment $ASSIGNMENT_FILE points at VM $VM_ID named '$ACTUAL_VM_NAME', expected '$VM_NAME'; refusing to reuse its lease" >&2
                    exit "${BUILD_FAILURE_EXIT_CODE:-1}"
                fi
            else
                echo "Existing assignment $ASSIGNMENT_FILE points at missing VM $VM_ID; recreating" >&2
                rm -f "$ASSIGNMENT_FILE" "$(get_ip_assignment_file "$SERIES")"
                unset VM_ID VM_NAME VM_SOURCE_STAGE VM_SERIES VM_IP_STATIC
            fi
        fi

        if [[ -z "${VM_ID:-}" ]]; then
            local NEWID NAME CLONE_ARGS CLONE_OUTPUT
            NAME="${CI_VM_NAME_PREFIX}-${PIPELINE_ID}-${SERIES}-$(printf '%s' "$SOURCE_STAGE" | sanitize_slug)"

            for _ in {1..10}; do
                NEWID=$(next_ci_clone_vmid) || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
                assert_ci_clone_vmid "$NEWID" || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
                CLONE_ARGS=(clone "$SEED_VMID" "$NEWID" --name "$NAME" --pool CICD --full "$CI_CLONE_FULL")

                echo "Cloning seed VM $SEED_VMID ($SOURCE_STAGE) to VM $NEWID ($NAME)"
                if CLONE_OUTPUT=$(qm "${CLONE_ARGS[@]}" 2>&1); then
                    VM_ID="$NEWID"
                    VM_NAME="$NAME"
                    VM_SOURCE_STAGE="$SOURCE_STAGE"
                    VM_SERIES="$SERIES"
                    break
                fi

                echo "$CLONE_OUTPUT" >&2
                echo "Clone attempt with VMID $NEWID failed; retrying with a fresh VMID" >&2
                sleep 2
            done

            if [[ -z "${VM_ID:-}" ]]; then
                echo "Error: Failed to clone seed VM $SEED_VMID after 10 attempts" >&2
                exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            fi

            qm set "$VM_ID" --description "{\"groups\":[\"cicd\"],\"pipeline\":\"${PIPELINE_ID}\",\"series\":\"${SERIES}\",\"source\":\"${SOURCE_STAGE}\",\"profile\":\"${CI_PROFILE}\"}" >/dev/null || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            VM_IP_STATIC=$(allocate_ci_ip "$SERIES") || exit "${BUILD_FAILURE_EXIT_CODE:-1}"
            {
                printf 'VM_ID=%q\n' "$VM_ID"
                printf 'VM_NAME=%q\n' "$VM_NAME"
                printf 'VM_SOURCE_STAGE=%q\n' "$VM_SOURCE_STAGE"
                printf 'VM_SERIES=%q\n' "$VM_SERIES"
                printf 'VM_IP_STATIC=%q\n' "$VM_IP_STATIC"
            } > "$ASSIGNMENT_FILE"
        fi
    ) 9>"$LOCK_FILE" || return 1

    load_ci_assignment "$SERIES" || return 1
    validate_ci_assignment "$SERIES" || return 1

    if [[ "$CI_PROFILE" == "lxc-2.4" ]]; then
        # Prevent an inherited seed address from appearing on the CI network.
        # The guest agent uses its host channel and works with net0 disconnected.
        set_ci_control_link "$VM_ID" 1 || return 1
    fi

    if [[ "$(qm status "$VM_ID" | awk '{print $2}')" != "running" ]]; then
        qm start "$VM_ID" || return 1
    fi

    if [[ -n "${VM_IP_STATIC:-}" ]]; then
        configure_ci_control_ip "$VM_ID" "$VM_IP_STATIC" || return 1
        VM_IP="$VM_IP_STATIC"
    else
        VM_IP=$(get_vm_ip_by_vmid "$VM_ID") || return 1
    fi

    if [[ "$CI_PROFILE" == "lxc-2.4" ]]; then
        set_ci_control_link "$VM_ID" 0 || return 1
    fi

    export VM_ID VM_IP VM_NAME VM_SOURCE_STAGE VM_SERIES VM_IP_STATIC
    echo "Pipeline $PIPELINE_ID | Series $SERIES | Source $VM_SOURCE_STAGE | VM $VM_ID ($VM_IP)"
}

destroy_ci_vm() {
    local SERIES="$1"
    local ASSIGNMENT_FILE
    ASSIGNMENT_FILE=$(get_assignment_file "$SERIES")

    if ! load_ci_assignment "$SERIES"; then
        echo "No CI clone assignment found for pipeline $PIPELINE_ID / series $SERIES"
        rm -f "$(get_ip_assignment_file "$SERIES")"
        return 0
    fi

    validate_ci_assignment "$SERIES" || return 1
    if [[ -z "${VM_ID:-}" || -z "${VM_NAME:-}" ]]; then
        echo "Assignment $ASSIGNMENT_FILE is missing VM_ID or VM_NAME; refusing to destroy" >&2
        return 1
    fi

    local EXPECTED_PREFIX ACTUAL_VM_NAME
    EXPECTED_PREFIX="${CI_VM_NAME_PREFIX}-${PIPELINE_ID}-${SERIES}-"

    if [[ "$VM_NAME" != "$EXPECTED_PREFIX"* ]]; then
        echo "Refusing to destroy VM $VM_ID with unexpected name '$VM_NAME'" >&2
        return 1
    fi

    if ! qm status "$VM_ID" >/dev/null 2>&1; then
        echo "CI clone VM $VM_ID ($VM_NAME) is already gone; removing stale assignment"
        rm -f "$ASSIGNMENT_FILE" "$CI_ASSIGNMENT_DIR/${PIPELINE_ID}-${SERIES}.lock" "$(get_ip_assignment_file "$SERIES")"
        return 0
    fi

    ACTUAL_VM_NAME="$(get_vm_name_by_vmid "$VM_ID")"
    if [[ "$ACTUAL_VM_NAME" != "$VM_NAME" || "$ACTUAL_VM_NAME" != "$EXPECTED_PREFIX"* ]]; then
        echo "Refusing to destroy VM $VM_ID: live VM name '$ACTUAL_VM_NAME' does not match assignment '$VM_NAME' and expected prefix '$EXPECTED_PREFIX'" >&2
        return 1
    fi

    echo "Destroying successful CI clone VM $VM_ID ($VM_NAME)"
    # Clones inherit the seed's protection flag. Clear it only after the
    # disposable-ID and live-name ownership checks above have both passed.
    qm set "$VM_ID" --protection 0 >/dev/null || return 1
    qm shutdown "$VM_ID" --timeout 60 || qm stop "$VM_ID" --skiplock 1 || true
    qm destroy "$VM_ID" --purge 1 --destroy-unreferenced-disks 1 || return 1
    rm -f "$ASSIGNMENT_FILE" "$CI_ASSIGNMENT_DIR/${PIPELINE_ID}-${SERIES}.lock" "$(get_ip_assignment_file "$SERIES")"
}

# Returns 0 if the given GitLab pipeline ID is in a terminal state
# (success/failed/canceled/skipped); 1 if active, unknown, or the API
# call fails. Used by claim-cluster to recover locks held by pipelines a
# user canceled (canceled pipelines do not run when:always release jobs,
# so without this we'd wait until STALE_THRESHOLD).
is_pipeline_terminal() {
    local PIPELINE="$1"
    [[ -z "$PIPELINE" ]] && return 1
    command -v glab >/dev/null 2>&1 || return 1
    command -v jq >/dev/null 2>&1 || return 1

    local PROJECT="${CUSTOM_ENV_CI_PROJECT_ID:-badsectorlabs%2Fludus}"
    local RESPONSE STATUS
    RESPONSE=$(timeout 10 glab api "projects/${PROJECT}/pipelines/${PIPELINE}" 2>/dev/null) || return 1
    STATUS=$(printf '%s' "$RESPONSE" | jq -r '.status // empty' 2>/dev/null)

    case "$STATUS" in
        success|failed|canceled|skipped) return 0 ;;
        *)                                return 1 ;;
    esac
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

# Resolve the target VMID for the current job based on build type and source stage
# Sets: VM_ID, VM_IP
resolve_vm() {
    local BUILD_TYPE="$CUSTOM_ENV_LUDUS_BUILD_TYPE"
    local SNAPSHOT_NAME="$CUSTOM_ENV_LUDUS_SNAPSHOT_NAME"

    # Claim/release jobs run on the runner host directly; no target VM.
    if [[ "$BUILD_TYPE" == *"claim"* || "$BUILD_TYPE" == *"release"* ]]; then
        echo "Claim/release job ($BUILD_TYPE) - no VM resolution needed"
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
        VM_IP=$(get_vm_ip_by_vmid "$VM_ID") || return 1
        export VM_IP
        echo "Build job, using dedicated build VM: $VM_ID ($VM_IP)"
        return 0
    fi

    # Full and snapshot-based tests use per-pipeline clones from seed VMs.
    if [[ "$BUILD_TYPE" == "full" || "$BUILD_TYPE" == "from-snapshot" ]]; then
        ensure_ci_vm
        return $?
    fi

    echo "Error: Unknown LUDUS_BUILD_TYPE: $BUILD_TYPE" >&2
    return 1
}
