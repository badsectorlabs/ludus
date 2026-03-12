#!/bin/bash

# This script builds the ludus client and overwrites the system client for local testing on a macOS or Linux machine

# Parse command line arguments
while getopts "hv:" opt; do
  case $opt in
    h)
      echo "Usage: $0 [-h] [-v version]"
      echo "  -v  Version string to embed in the client binary"
      exit 0
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

pushd .

# cd to the directory of the script
cd "$(dirname "$0")" || exit

GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export GIT_COMMIT_SHORT_HASH
if [ -z "$VERSION_STRING" ]; then
    VERSION_STRING=$(git rev-parse --abbrev-ref HEAD)
fi
export VERSION=$VERSION_STRING

if [[ ! -d spinner ]]; then
    git clone https://github.com/zimeg/spinner
    cd spinner || exit
    git checkout unhide-interrupts
    cd .. || exit
fi
go mod edit -replace github.com/briandowns/spinner=./spinner
go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-build -X ludus/cmd.VersionString=$VERSION"
# GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-build -X ludus/cmd.VersionString=$VERSION" -o ludus-linux
if [[ $? -ne 0 ]]; then
    echo
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
    echo "[!] ERROR building ludus client"
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
    echo
    exit 1
fi
go mod edit -dropreplace=github.com/briandowns/spinner
if command -v sudo &> /dev/null; then
  sudo mv ludus /usr/local/bin/ludus
  if [[ $? -ne 0 ]]; then
      echo
      echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
      echo "[!] ERROR moving ludus client to /usr/local/bin"
      echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
      echo
      exit 1
  fi
else
  mv ludus /usr/local/bin/ludus
  if [[ $? -ne 0 ]]; then
      echo
      echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
      echo "[!] ERROR moving ludus client to /usr/local/bin"
      echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
      echo
      exit 1
  fi
fi

echo
echo "[=] Ludus client built and installed to /usr/local/bin/ludus"
echo

popd || return
