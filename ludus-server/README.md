# Ludus Server

This server controls Ludus user management, template management, range deployment, range power state, and range testing state.

## Overview

To view the API documentation, run ludus-server and browse to https://<ip>:8080/api

## Building

```
go build -trimpath -ldflags "-s -w -X main.GitCommitHash=manual-build" -o ../binaries/ludus-server
```

