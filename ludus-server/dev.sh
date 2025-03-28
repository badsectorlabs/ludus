#!/bin/bash

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
    mv ./build ../ludus-server/src/docs
    cd ../ludus-server || exit
    TAGS="-tags=embeddocs"
fi

GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
GIT_ABBREV_REF=$(git rev-parse --abbrev-ref HEAD)
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build  -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-build -X main.VersionString=${GIT_ABBREV_REF}" ${TAGS} -o ludus-server
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

./ludus-server --update --no-dep-update

echo
echo "[=] Ludus server built and installed to /opt/ludus/ludus-server"
echo

popd || return
