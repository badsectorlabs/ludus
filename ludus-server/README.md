# Ludus Server

This server controls Ludus user management, template management, range deployment, range power state, and range testing state.

## Overview

To view the API documentation, run ludus-server and browse to https://<ip>:8080/api

## Building without embedded documentation

```
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-no-docs" -o ludus-server
```

## Building with embedded documentation

```
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
cd docs
yarn install
yarn build
mv ./build ../ludus-server/src/docs
cd ../ludus-server
GOOS=linux GOARCH=amd64 go build -tags=embeddocs -trimpath -ldflags "-s -w -X main.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual-with-docs" -o ludus-server
```

## Plugins

Plugins are SDK-based packages built against `ludus-plugin-sdk`; see
`docs/docs/developers/rpc-plugins.md` for authoring and the package format.
Build a plugin's executable and generate its manifest from the workspace root:

```sh
mkdir -p /tmp/ludus-enterprise
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /tmp/ludus-enterprise/plugin ./ludus-enterprise-plugin
/tmp/ludus-enterprise/plugin --ludus-manifest > /tmp/ludus-enterprise/plugin.json
```

Server-installed packages live in a root-owned directory per plugin ID:

```text
/opt/ludus/plugins/<id>/plugin.json
/opt/ludus/plugins/<id>/plugin          # 0755
/opt/ludus/plugins/<id>/ui/index.html   # optional
/opt/ludus/plugins/uploaded/<resourceID>/  # admin-approved uploaded root-run packages
```

`/opt/ludus/plugins` and everything under it must be `root:root` and not
group- or world-writable. Both services scan `/opt/ludus/plugins/*/plugin.json`;
`ludus.service` starts packages declaring `runAs: ludus`, and
`ludus-admin.service` starts packages declaring `runAs: root`.

Plugin playbooks run through the host service's `RunPlaybook`, which archives
each run (including failures) in the range log history. Use
`ludus range -r <range> logs --history` to list runs and
`ludus range -r <range> logs --id <log-id>` to read one.
