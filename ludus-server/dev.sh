#!/bin/bash

# Parse command line arguments
while getopts "hDdP" opt; do
  case $opt in
    h)
      echo "Usage: $0 [-h] [-d] [-D]"
      echo "  -d  Enable debug logging for Ludus"
      echo "  -D  Enable debug logging for the database"
      echo "  -P  Enable debug logging for proxmox"
      exit 0
      ;;
    d)
      DEBUG_MODE=true
      ;;
    D)
      DEBUG_DATABASE=true
      ;;
    P)
      DEBUG_PROXMOX=true
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
  esac
done

pushd .

# cd to the directory of the script
cd "$(dirname "$0")" || exit

# if the first argument is "docs" then generate the docs
if [ "$1" == "docs" ]; then
    cd ../docs || exit
    yarn install
    yarn build
    rm -f ./build/video/*
    rm -f ./build/img/hardware/Debian_12_RAID0.mp4
    mv ./build ../ludus-api/docs
    cd ../ludus-server || exit
fi

TAGS=""
if [ -d "../ludus-api/docs" ]; then
    TAGS="embeddocs"
fi
if [ -d "../ludus-api/webUI" ]; then
    if [ -n "$TAGS" ]; then
        TAGS="${TAGS} embedwebui"
    else
        TAGS="embedwebui"
    fi
fi

GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
GIT_ABBREV_REF=$(git rev-parse --abbrev-ref HEAD)
echo CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-build -X main.VersionString=${GIT_ABBREV_REF}" -tags "${TAGS}" -o ludus-server
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-build -X main.VersionString=${GIT_ABBREV_REF}" -tags "${TAGS}" -o ludus-server
if [[ $? -ne 0 ]]; then
    echo
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
    echo "[!] ERROR building ludus server"
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
    echo
    exit 1
fi


# Check if scutil is available (macOS)
if command -v scutil &> /dev/null; then
    HOSTNAME=$(scutil --get LocalHostName)
else
    HOSTNAME=$(hostname)
fi

# If the current hostname is m1 run copy the binary to the K8 dev box and update Ludus
if [ "$HOSTNAME" == "m1" ]; then
    scp ludus-server lkdev2: && ssh lkdev2 "./ludus-server --update"
fi

if [ "$DEBUG_MODE" = true ]; then
    echo "[+] Setting LUDUS_DEBUG=1"
    systemctl set-environment LUDUS_DEBUG=1
else
    echo "[-] Unsetting LUDUS_DEBUG"
    systemctl unset-environment LUDUS_DEBUG
fi

if [ "$DEBUG_DATABASE" = true ]; then
    echo "[+] Setting LUDUS_DEBUG_DATABASE=1"
    systemctl set-environment LUDUS_DEBUG_DATABASE=1
else
    echo "[-] Unsetting LUDUS_DEBUG_DATABASE"
    systemctl unset-environment LUDUS_DEBUG_DATABASE
fi

if [ "$DEBUG_PROXMOX" = true ]; then
    echo "[+] Setting LUDUS_DEBUG_PROXMOX=1"
    systemctl set-environment LUDUS_DEBUG_PROXMOX=1
else
    echo "[-] Unsetting LUDUS_DEBUG_PROXMOX"
    systemctl unset-environment LUDUS_DEBUG_PROXMOX
fi

./ludus-server --update --no-dep-update

echo
echo "[=] Ludus server built and installed to /opt/ludus/ludus-server"
echo

popd || return
