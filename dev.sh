#!/bin/bash

# This script is used to build and run Ludus in a development environment
# It assumes you are on a macOS or Linux host and have root SSH access to the target machine

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TESTING_STATE_FILE=${LUDUS_TESTING_STATE_FILE:-"$SCRIPT_DIR/.ludus-testing-vm.json"}
DEV_ENV_FILE=${LUDUS_DEV_ENV_FILE:-"$SCRIPT_DIR/.ludus-dev-env"}
TARGET_EXPLICIT=false

# Parse command line arguments
while getopts "hlap:t:n:cdwsSDCPLv:" opt; do
  case $opt in
    h)
      echo "Usage: $0 [-h] [-l] [-a] [-t target] [-n lines] [-c] [-d] [-p] [-w] [-s] [-D] [-C] [-v version]"
      echo "  -h  Show this help message"
      echo "  -l  Show Ludus service logs (default 100 lines)"
      echo "  -a  Show Ludus admin service logs (requires -l)"
      echo "  -n  Number of log lines to show (default 100)"
      echo "  -t  Target development hostname (required unless a test VM is checked out)"
      echo "  -p  Port to use for SSH/rsync"
      echo "  -c  Build and install client locally"
      echo "  -C  Build and install client remotely"
      echo "  -w  Build and install web UI"
      echo "  -s  Skip plugins"
      echo "  -S  Skip building the server, just sync the code"
      echo "  -d  Enable debug mode for Ludus server"
      echo "  -D  Enable debug mode for database"
      echo "  -P  Enable debug mode for Proxmox"
      echo "  -L  Enable debug mode for license requests"
      echo "  -v  Version string to use for server and client builds"
      echo ""
      echo "Examples:"
      echo "  $0 -t ludus-dev-hostname -C -d -s # Build and install client remotely, Build and install Ludus server with debug mode, skip plugins"
      exit 0
      ;;
    l)
      SHOW_LOGS=true
      ;;
    a)
      ADMIN_LOGS=true
      ;;
    t)
      DEVELOPMENT_HOSTNAME=$OPTARG
      TARGET_EXPLICIT=true
      ;;
    n)
      NUM_LINES=$OPTARG
      ;;
    c)
      BUILD_CLIENT=true
      ;;
    d)
      DEBUG_MODE=true
      ;;
    p)
      PORT=$OPTARG
      ;;
    w)
      BUILD_WEB_UI=true
      ;;
    s)
      SKIP_PLUGINS=true
      ;;
    S)
      SKIP_SERVER=true
      ;;
    D)
      DEBUG_DATABASE=true
      ;;
    C)
      BUILD_CLIENT_REMOTELY=true
      ;;
    P)
      DEBUG_PROXMOX=true
      ;;
    L)
      DEBUG_LICENSE=true
      ;;
    v)
      VERSION_STRING=$OPTARG
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
  esac
done

# Use a checked-out test VM unless the caller explicitly supplied a target.
if [ -z "${PORT:-}" ]; then
  PORT=22
fi
LOCAL_LUDUS_PORT=8080

case "$PORT" in
  ''|*[!0-9]*)
    echo "Invalid SSH port: $PORT" >&2
    exit 1
    ;;
esac

SSH_COMMAND=(ssh -p "$PORT" -o StrictHostKeyChecking=accept-new)
RSYNC_SSH="ssh -p $PORT -o StrictHostKeyChecking=accept-new"

if [ "$TARGET_EXPLICIT" = false ] && [ -f "$TESTING_STATE_FILE" ]; then
  if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required to use the checked-out testing VM" >&2
    exit 1
  fi

  if ! TESTING_STATE=$(jq -ce '
    select(
      .version == 1 and
      (.ip | type) == "string" and
      (.proxmox_host | type) == "string" and
      (.ports.ludus | type) == "number"
    )
  ' "$TESTING_STATE_FILE"); then
    echo "Invalid testing VM state: $TESTING_STATE_FILE" >&2
    exit 1
  fi

  TESTING_IP=$(printf '%s\n' "$TESTING_STATE" | jq -er '.ip')
  PROXMOX_HOST=$(printf '%s\n' "$TESTING_STATE" | jq -er '.proxmox_host')
  LOCAL_LUDUS_PORT=$(printf '%s\n' "$TESTING_STATE" | jq -r '.ports.ludus')
  case "$TESTING_IP" in
    ''|*[!A-Za-z0-9._:-]*)
      echo "Invalid testing VM IP in $TESTING_STATE_FILE" >&2
      exit 1
      ;;
  esac
  case "$PROXMOX_HOST" in
    ''|*[!A-Za-z0-9._@-]*)
      echo "Invalid Proxmox host in $TESTING_STATE_FILE" >&2
      exit 1
      ;;
  esac
  if [ "$LOCAL_LUDUS_PORT" -lt 1 ] || [ "$LOCAL_LUDUS_PORT" -gt 65535 ]; then
    echo "Invalid local Ludus port in $TESTING_STATE_FILE" >&2
    exit 1
  fi

  DEVELOPMENT_HOSTNAME="root@$TESTING_IP"
  SSH_COMMAND+=(-J "$PROXMOX_HOST")
  RSYNC_SSH="$RSYNC_SSH -J $PROXMOX_HOST"
  echo "[+] Using checked-out Ludus test VM $TESTING_IP via $PROXMOX_HOST"
elif [ -z "${DEVELOPMENT_HOSTNAME:-}" ]; then
  echo "No development hostname specified. Use -t <hostname> or check out a test VM." >&2
  exit 1
fi

run_remote_login() {
  local remote_command=$1
  local quoted_command

  printf -v quoted_command '%q' "$remote_command"
  "${SSH_COMMAND[@]}" "$DEVELOPMENT_HOSTNAME" "bash -lc $quoted_command"
}

run_remote_command() {
  local remote_command=""
  local argument
  local quoted_argument

  for argument in "$@"; do
    printf -v quoted_argument '%q' "$argument"
    if [ -n "$remote_command" ]; then
      remote_command="${remote_command} ${quoted_argument}"
    else
      remote_command=$quoted_argument
    fi
  done

  run_remote_login "$remote_command"
}

run_remote_in_dir() {
  local directory=$1
  local remote_command
  local argument
  local quoted_argument

  shift
  printf -v quoted_argument '%q' "$directory"
  remote_command="cd ~/ludus-dev/${quoted_argument} &&"
  for argument in "$@"; do
    printf -v quoted_argument '%q' "$argument"
    remote_command="${remote_command} ${quoted_argument}"
  done

  run_remote_login "$remote_command"
}

get_remote_dev_api_key() {
  local remote_script

  remote_script=$(cat <<'REMOTE_SCRIPT'
set -euo pipefail

key_file="$HOME/.ludus-api-key"
if [ ! -s "$key_file" ]; then
  response=$(
    LUDUS_URL=https://127.0.0.1:8080 \
    LUDUS_API_KEY="$(cat /opt/ludus/install/root-api-key)" \
      ludus user add -a -e dev@localhost.local -n Dev -p password -i DEV --json
  )
  api_key=$(printf '%s\n' "$response" | jq -er '.apiKey | select(type == "string" and length > 0)')
  temp_file=$(mktemp "${key_file}.tmp.XXXXXX")
  chmod 600 "$temp_file"
  printf '%s\n' "$api_key" >"$temp_file"
  mv "$temp_file" "$key_file"
fi

api_key=$(cat "$key_file")
[ -n "$api_key" ] || { echo "Saved development API key is empty" >&2; exit 1; }
printf '%s\n' "$api_key"
REMOTE_SCRIPT
)

  run_remote_login "$remote_script"
}

write_local_dev_env() {
  local api_key=$1
  local temp_file

  temp_file=$(mktemp "${DEV_ENV_FILE}.tmp.XXXXXX") || return 1
  chmod 600 "$temp_file"
  {
    printf 'export LUDUS_URL=%q\n' "$LUDUS_URL"
    printf 'export LUDUS_API_KEY=%q\n' "$api_key"
  } >"$temp_file"

  if ! mv "$temp_file" "$DEV_ENV_FILE"; then
    rm -f "$temp_file"
    return 1
  fi
}

if ! LOCAL_GIT_COMMIT=$(git -C "$SCRIPT_DIR" rev-parse --short HEAD); then
  echo "Unable to determine the local Git commit" >&2
  exit 1
fi

if [ -n "${VERSION_STRING:-}" ]; then
  DEVELOPMENT_VERSION=$VERSION_STRING
elif ! DEVELOPMENT_VERSION=$(git -C "$SCRIPT_DIR" symbolic-ref --quiet --short HEAD); then
  DEVELOPMENT_VERSION=$LOCAL_GIT_COMMIT
fi

# Add the plugins to the go workspace if they exist
if [ -d "./ludus-enterprise-plugin" ]; then
    go work use ./ludus-enterprise-plugin
fi

if [ -d "./ludus-antisandbox-plugin" ]; then
    go work use ./ludus-antisandbox-plugin
fi

# rsync the Ludus source code to the target machine
# excluding and files from .gitignore
rsync -av --progress \
    --no-owner --no-group \
    --exclude='/.git' \
    --exclude='/.ludus-dev-env' \
    --exclude='/.ludus-testing-vm.json' \
    --exclude='/.ludus-testing-tunnel.json' \
    --exclude='.vscode/' \
    --exclude='docs/' \
    --exclude='webUI/' \
    --exclude='ludus-gui/node_modules/' \
    --exclude='ludus-gui/.next/' \
    --include='ludus-antisandbox-plugin/' \
    --include='ludus-enterprise-plugin/' \
    --filter=':- ./*/.gitignore' \
    --delete \
    -e "$RSYNC_SSH" \
    . "$DEVELOPMENT_HOSTNAME":~/ludus-dev

# Remove local-only metadata copied by older versions of this script. Remote
# builds receive local commit/version metadata explicitly and keep the API key
# only in ~/.ludus-api-key.
run_remote_in_dir . rm -rf \
    .git \
    .ludus-dev-env \
    .ludus-testing-vm.json \
    .ludus-testing-tunnel.json

# If the enterprise plugin exists, build it first
if [ -d "./ludus-enterprise-plugin" ] && [ "$SKIP_PLUGINS" != true ]; then
    run_remote_in_dir ludus-enterprise-plugin ./dev.sh
fi

# If the anti-sandbox plugin exists, build it before the Ludus server
if [ -d "./ludus-antisandbox-plugin" ] && [ "$SKIP_PLUGINS" != true ]; then
    run_remote_in_dir ludus-antisandbox-plugin ./dev.sh
fi

# If the web UI exists, build it before the Ludus server
if [ -d "./ludus-gui" ] && [ "$BUILD_WEB_UI" = true ]; then
    run_remote_in_dir ludus-gui ./dev.sh
fi

# Build the Ludus server in a login shell so the target's configured Go path
# matches an interactive root login.
SERVER_ARGS=()
if [ "$DEBUG_MODE" = true ]; then
    SERVER_ARGS+=(-d)
fi

if [ "$DEBUG_DATABASE" = true ]; then
    SERVER_ARGS+=(-D)
fi

if [ "$DEBUG_PROXMOX" = true ]; then
    SERVER_ARGS+=(-P)
fi

if [ "$DEBUG_LICENSE" = true ]; then
    SERVER_ARGS+=(-L)
fi

SERVER_ARGS+=(-v "$DEVELOPMENT_VERSION")

if [ "$SKIP_SERVER" != true ]; then
    run_remote_in_dir ludus-server env \
        "LUDUS_DEV_GIT_COMMIT=$LOCAL_GIT_COMMIT" \
        ./dev.sh "${SERVER_ARGS[@]}"
else
    echo "[-] Skipping server build"
fi

if ! DEV_API_KEY=$(get_remote_dev_api_key); then
    echo "Unable to provision the remote development API key" >&2
    exit 1
fi

LUDUS_URL="https://127.0.0.1:$LOCAL_LUDUS_PORT"
LUDUS_API_KEY=$DEV_API_KEY
export LUDUS_URL LUDUS_API_KEY
if ! write_local_dev_env "$DEV_API_KEY"; then
    echo "Unable to write the local Ludus environment file: $DEV_ENV_FILE" >&2
    exit 1
fi
echo "[+] Local Ludus environment written to $DEV_ENV_FILE"
echo "    Run: source $DEV_ENV_FILE"

# Build the client remotely if requested
if [ "$BUILD_CLIENT_REMOTELY" = true ]; then
    run_remote_in_dir ludus-client env \
        "LUDUS_DEV_GIT_COMMIT=$LOCAL_GIT_COMMIT" \
        ./dev.sh -v "$DEVELOPMENT_VERSION"
fi

# Build the client locally if requested
if [ "$BUILD_CLIENT" = true ]; then
    CLIENT_VERSION_ARG=""
    if [ -n "$VERSION_STRING" ]; then
        CLIENT_VERSION_ARG="-v $VERSION_STRING"
    fi
    ./ludus-client/dev.sh $CLIENT_VERSION_ARG
fi

# Handle log viewing
if [ "$SHOW_LOGS" = true ]; then
  if [ -z "$NUM_LINES" ]; then
    NUM_LINES=100
  fi
  
  # Wait 1 second for the server to start and load plugins
  sleep 1

  if [ "$ADMIN_LOGS" = true ]; then
    run_remote_command journalctl -u ludus-admin -n "$NUM_LINES"
    exit 0
  else
    run_remote_command journalctl -u ludus -n "$NUM_LINES"
    exit 0
  fi
fi