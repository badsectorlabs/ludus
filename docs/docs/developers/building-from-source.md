---
title: 🛠️ Building from source
---

# 🛠️ Building from source

:::danger

The main branch is not guaranteed to be stable. For guaranteed stability, use the most recent release's tag:

```shell
STABLE_VERSION=$(curl -s https://gitlab.com/api/v4/projects/54052321/releases/ | \
  jq '.[]' | jq -r '.name' | head -1 | egrep -o '[0-9]+\.[0-9]+\.[0-9]+')
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus
git checkout tags/$STABLE_VERSION
```

:::

## Server

### Building without embedded documentation

Assuming a Debian 12/Proxmox 8 host, install the build dependencies 

```shell
# Install Go
wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Build Ludus

```shell
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)
cd ludus-server
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-no-docs -X main.VersionString=$VERSION" -o ludus-server
```

### Building with embedded documentation

Assuming a Debian 12/Proxmox 8 host, install the build dependencies 

```shell
# Install yarn
echo "deb https://dl.yarnpkg.com/debian/ stable main" | tee /etc/apt/sources.list.d/yarn.list
wget -qO- https://dl.yarnpkg.com/debian/pubkey.gpg | tee /etc/apt/trusted.gpg.d/dl.yarnpkg.com.asc
apt update
apt install yarn
# Install Go
wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Build Ludus

```shell
# Get the code
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)
# Build the docs
cd docs
yarn install
yarn build
# Remove videos to make the binary smaller
rm -f ./build/video/*
rm -f ./build/img/hardware/Debian_12_RAID0.mp4
# Move the docs to the location the server expects to embed them
mv ./build ../ludus-server/src/docs
cd ../ludus-server
# Build Ludus
GOOS=linux GOARCH=amd64 go build -tags=embeddocs -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-with-docs -X main.VersionString=$VERSION" -o ludus-server
```

## Client

First, install [Go](https://go.dev/doc/install) for your operating system.

### Building for your current OS/Arch

```shell
git clone https://gitlab.com/badsectorlabs/ludus.git
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)
cd ludus-client
go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X main.VersionString=$VERSION"
```

### Building for all OS/Archs

```shell
git clone https://gitlab.com/badsectorlabs/ludus.git
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)
cd ludus-client
# Use the fork that doesn't break the terminal on control+c for Linux and macOS
git clone https://github.com/zimeg/spinner
cd spinner && git checkout unhide-interrupts && cd .. && go mod edit -replace github.com/briandowns/spinner=./spinner
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_linux-amd64
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_linux-arm64
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_macOS-amd64
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_macOS-arm64
# The forked spinner library doesn't compile for windows, so switch back to the original
go mod edit -dropreplace=github.com/briandowns/spinner
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_windows-amd64.exe
GOOS=windows GOARCH=386 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_windows-386.exe
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual -X ludus/cmd.VersionString=$VERSION" -o ./binaries/ludus-client_windows-arm64.exe
# All client binaries will be in the `binaries` folder
```
