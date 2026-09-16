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

## Building the RPC plugins

Run these commands from the workspace root:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o ludus-enterprise-plugin/ludus-enterprise.plugin ./ludus-enterprise-plugin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o ludus-antisandbox-plugin/ludus-antisandbox.plugin ./ludus-antisandbox-plugin
```

The plugins are standalone executables served through `hashicorp/go-plugin`.
They do not need CGO and do not need to use the server's exact `ludus-api`
source revision, build tags, or Go toolchain patch release. The host and plugin
must support the same Ludus RPC protocol version.

Install the enterprise executable as
`/opt/ludus/plugins/enterprise/ludus-enterprise.plugin` for the `ludus` service
or under `/opt/ludus/plugins/enterprise/admin/` for the root service. Install
the anti-sandbox executable as
`/opt/ludus/plugins/enterprise/admin/ludus-antisandbox.plugin`. Plugin files
must be executable (`chmod 0755`).

Community RPC plugins use the `.plugin` suffix and belong in the matching
`/opt/ludus/plugins/community/` or `/opt/ludus/plugins/community/admin/`
directory.

## Plugin playbook log history

Anti-sandbox and Windows licensing playbooks archive each VM's run in the
standard range log history, including failed runs. Use
`ludus range -r <range> logs --history` to list runs and
`ludus range -r <range> logs --id <log-id>` to read one.

Plugins create and finalize history through authenticated, loopback-only
host endpoints under `/api/plugins/range-logs`. They send record IDs and
completion status; the host resolves the log path, stores the file in
PocketBase, and applies `max_log_history` retention. Plugin subprocesses
do not open the database or manage PocketBase files.

Upgrade the host before installing plugins that use these endpoints.
The RPC wire protocol is unchanged; older plugins can still load, but
must be rebuilt with the log-history support to archive their playbooks.
