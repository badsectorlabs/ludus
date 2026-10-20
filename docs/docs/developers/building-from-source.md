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

Assuming a Debian 12/13 or Proxmox 8/9 host, install the Go version declared in `go.work`:

```shell
# Install Go 1.25
GO_VERSION=1.25.12
wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Build the dynamic inventory executable first because it is embedded in the server binary, then build Ludus:

```shell
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)
cd dynamic-inventory
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o ../ludus-server/ansible/range-management/dynamic-inventory .
cd ../ludus-server
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-no-docs -X main.VersionString=$VERSION" \
  -o ludus-server .
```

### Building with embedded documentation

Assuming a Debian 12/13 or Proxmox 8/9 host, install Yarn and the Go version declared in `go.work`:

```shell
# Install yarn
echo "deb https://dl.yarnpkg.com/debian/ stable main" | tee /etc/apt/sources.list.d/yarn.list
wget -qO- https://dl.yarnpkg.com/debian/pubkey.gpg | tee /etc/apt/trusted.gpg.d/dl.yarnpkg.com.asc
apt update
apt install yarn
# Install Go 1.25
GO_VERSION=1.25.12
wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Build Ludus:

```shell
# Get the code
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
export VERSION=$(git rev-parse --abbrev-ref HEAD)
# Build the dynamic inventory binary that the server embeds
cd dynamic-inventory
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o ../ludus-server/ansible/range-management/dynamic-inventory .
cd ..
# Build the docs
cd docs
yarn install
yarn build
# Remove videos to make the binary smaller
rm -f ./build/video/*
rm -f ./build/img/hardware/Debian_12_RAID0.mp4
# Move the docs to the location ludus-api expects to embed
mv ./build ../ludus-api/docs
cd ../ludus-server
# Build Ludus
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=embeddocs -trimpath \
  -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-with-docs -X main.VersionString=$VERSION" \
  -o ludus-server .
```

## Plugins

Plugins are standalone Go programs built with the Ludus plugin SDK
(`ludus-plugin-sdk`). They do not import Ludus server code, need no CGO, and
are distributed as package ZIPs containing `plugin.json`, the `plugin`
executable, and an optional `ui/index.html`.

Running the executable with `--ludus-manifest` prints its `plugin.json`. For
example, build the Enterprise package from an authorized development checkout
at the workspace root (on a linux/amd64 host):

```shell
mkdir -p package
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" \
  -o package/plugin ./ludus-enterprise-plugin
./package/plugin --ludus-manifest > package/plugin.json
(cd package && zip -X ../ludus-enterprise.zip plugin.json plugin)
```

Install a server package by extracting it, root-owned, into
`/opt/ludus/plugins/<id>/` (for example `/opt/ludus/plugins/ludus-enterprise/`),
or pass the ZIP to `install.sh --enterprise-plugin`. Other packages can be
uploaded through **Resources → Plugins**. See [Plugins](./rpc-plugins.md) for
the SDK, package format, levels, and approval.

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
