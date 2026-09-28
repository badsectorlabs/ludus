#!/usr/bin/env bash
# Source on the nested Proxmox host. Only server operations enter the appliance;
# qm, pvesh, pveum and client-side fixture uploads remain on this host.
ci_server_init() {
    CI_SERVER_VMID=
    CI_SERVER_IP=127.0.0.1
    case "${LUDUS_CI_PROFILE:-${CUSTOM_ENV_LUDUS_CI_PROFILE:-host-2.3}}" in
        host-2.3) ;;
        lxc-2.4)
            CI_SERVER_VMID=$(sudo cat /etc/ludus-lxc.json | jq -er '.vmid | select(type == "number" and . > 0 and floor == .)') || return
            CI_SERVER_IP=$(sudo pct exec "$CI_SERVER_VMID" -- ip -j -4 address show dev eth0 | jq -er '[.[0].addr_info[] | select(.scope == "global") | .local][0]') || return
            [[ -n "$CI_SERVER_IP" ]] || return 1
            ;;
        *) echo "Unknown CI profile: ${LUDUS_CI_PROFILE:-${CUSTOM_ENV_LUDUS_CI_PROFILE:-}}" >&2; return 1 ;;
    esac
    CI_SERVER_PORT=$(ci_server_exec awk '$1 == "port:" {print $2; exit}' /opt/ludus/config.yml) || return
    [[ "$CI_SERVER_PORT" =~ ^[0-9]+$ ]] || { echo 'Missing API port in server config' >&2; return 1; }
    CI_SERVER_URL="https://${CI_SERVER_IP}:${CI_SERVER_PORT}"
    export CI_SERVER_VMID CI_SERVER_IP CI_SERVER_PORT CI_SERVER_URL
}

ci_server_exec() {
    if [[ -n "${CI_SERVER_VMID:-}" ]]; then
        sudo pct exec "$CI_SERVER_VMID" -- "$@"
    else
        sudo "$@"
    fi
}

ci_server_push() {
    if [[ -n "${CI_SERVER_VMID:-}" ]]; then
        sudo pct push "$CI_SERVER_VMID" "$1" "$2" --perms "${3:-0600}"
    else
        sudo install -m "${3:-0600}" "$1" "$2"
    fi
}

ci_server_pull() {
    if [[ -n "${CI_SERVER_VMID:-}" ]]; then
        sudo pct pull "$CI_SERVER_VMID" "$1" "$2"
    else
        sudo cp "$1" "$2"
    fi
}

ci_server_push_tree() {
    ci_server_exec mkdir -p "$2" || return
    tar -C "$1" -cf - . | ci_server_exec tar -C "$2" -xf -
}

ci_server_wait() {
    local attempt
    for attempt in {1..120}; do
        if curl -fkSs --max-time 5 "${CI_SERVER_URL}/api/health" | jq -e '.code == 200' >/dev/null; then
            return 0
        fi
        sleep 5
    done
    ci_server_exec journalctl -u ludus -u ludus-admin -n 100 --no-pager >&2 || true
    echo 'Ludus API did not become ready' >&2
    return 1
}

ci_client_config() {
    mkdir -p "$HOME/.config/ludus" || return
    printf 'url: %s\n' "$CI_SERVER_URL" > "$HOME/.config/ludus/config.yml"
}

ci_server_update() (
    set -euo pipefail
    local candidate=${1:?candidate binary required} staged candidate_hash installed_hash
    candidate_hash=$(sha256sum "$candidate") || return
    installed_hash=$(ci_server_exec sha256sum /opt/ludus/ludus-server) || return
    if [[ "${candidate_hash%% *}" == "${installed_hash%% *}" ]]; then
        ci_server_wait
        return
    fi
    if ci_server_exec pgrep -f 'ansible-playbook|/packer '; then
        echo 'Cannot update while a deployment or template build is active' >&2
        return 1
    fi
    staged=$(ci_server_exec mktemp /tmp/ludus-ci-update.XXXXXX) || return
    trap 'ci_server_exec rm -f "$staged"' EXIT
    ci_server_push "$candidate" "$staged" 0700 || return
    ci_server_exec env HOME=/root TMPDIR=/tmp PATH=/opt/ludus/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin LANG=C.UTF-8 LC_ALL=C.UTF-8 "$staged" --update || return
    ci_server_wait
)

ci_job_setup() {
    # Build artifacts refresh fixtures even with GIT_STRATEGY:none.
    local root=${CI_PROJECT_DIR:-$PWD}
    if [[ -d "$root/ludus-server/ci/fixtures" ]]; then
        sudo mkdir -p /opt/ludus/ci || return
        sudo cp -a "$root/ludus-server/ci/fixtures" "$root/ludus-server/ci/configs" "$root/ludus-server/ci/roles" "$root/ludus-server/ci/check-password-rotation.py" /opt/ludus/ci/ || return
    fi
    ci_server_init && ci_client_config || return
    [[ -f "$root/binaries/ludus-server" && -f "$root/binaries/ludus-client_linux-amd64" ]] || {
        echo 'Candidate build artifacts are required' >&2; return 1;
    }
    sudo install -m 0755 "$root/binaries/ludus-client_linux-amd64" /usr/local/bin/ludus || return
    ci_server_update "$root/binaries/ludus-server" || return
    LUDUS_API_KEY=$(ci_server_exec cat /opt/ludus/install/root-api-key) || return
    export LUDUS_API_KEY
}
