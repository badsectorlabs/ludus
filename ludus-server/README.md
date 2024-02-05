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
