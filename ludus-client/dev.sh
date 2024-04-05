#!/bin/bash

# This script builds the ludus client and overwrites the system client for local testing on a macOS or Linux machine

export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)

if [[ ! -d spinner ]]; then
    git clone https://github.com/zimeg/spinner
    cd spinner
    git checkout unhide-interrupts
    cd .. 
fi
go mod edit -replace github.com/briandowns/spinner=./spinner
go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION"
go mod edit -dropreplace=github.com/briandowns/spinner
sudo mv ludus /usr/local/bin/ludus
