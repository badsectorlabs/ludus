---
title: "🤖 MCP Server"
sidebar_position: 10
description: Control Ludus from AI coding assistants via MCP
keywords: [mcp, ai, claude, cursor, vscode, automation]
---

# 🤖 MCP Server

`@badsectorlabs/ludus-mcp` is a local [MCP](https://modelcontextprotocol.io/) server that connects AI coding assistants to your Ludus server.

```bash
# Add the MCP server
claude mcp add ludus -- npx -y @badsectorlabs/ludus-mcp \
  --url https://<LUDUS_HOST>:8080 --api-key <YOUR_API_KEY>

# Then ask things like:
# "deploy my range with the user-defined-roles tag"
# "list all templates and tell me which ones are built"
# "show me the range config"
```

## Setup

You need Node.js 20+, your Ludus server URL, and an API key (see `ludus apikey`).

### Claude Code

```bash
claude mcp add ludus -- npx -y @badsectorlabs/ludus-mcp \
  --url https://<LUDUS_HOST>:8080 --api-key <YOUR_API_KEY>
```

### Codex

```bash
codex mcp add ludus -- npx -y @badsectorlabs/ludus-mcp \
  --url https://<LUDUS_HOST>:8080 --api-key <YOUR_API_KEY>
```

### Claude Desktop

```json title="claude_desktop_config.json"
{
  "mcpServers": {
    "ludus": {
      "command": "npx",
      "args": ["-y", "@badsectorlabs/ludus-mcp", "--url", "https://<LUDUS_HOST>:8080", "--api-key", "<YOUR_API_KEY>"]
    }
  }
}
```

macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
Windows: `%APPDATA%\Claude\claude_desktop_config.json`

### Cursor

```json title=".cursor/mcp.json"
{
  "mcpServers": {
    "ludus": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@badsectorlabs/ludus-mcp", "--url", "https://<LUDUS_HOST>:8080", "--api-key", "<YOUR_API_KEY>"]
    }
  }
}
```

### Environment Variables

The `--url` and `--api-key` flags can also be set via `LUDUS_URL` and `LUDUS_API_KEY` environment variables.

## Tools

The server exposes the full Ludus API through three tools:

- **`list_ludus_operations`** — search and filter available API operations
- **`describe_ludus_operation`** — get parameter and request body schemas for an operation
- **`call_ludus_api`** — execute an API operation

## How It Works

```
┌─────────────┐     stdio      ┌──────────────┐     HTTPS      ┌──────────────┐
│  AI Client  │ ◄────────────► │  ludus-mcp   │ ─────────────► │ Ludus Server │
│ (Claude,    │  MCP protocol  │  (local)     │  REST API      │ (remote)     │
│  Cursor...) │                │              │  + API key     │              │
└─────────────┘                └──────────────┘                └──────────────┘
```

The MCP server runs on your machine as a stdio process. On startup it loads a bundled copy of the Ludus OpenAPI spec, then tries to fetch the latest version from your server at `/api/v2/openapi`.

`list` and `describe` just read from the parsed spec. `call` builds an HTTP request from the operation definition and your arguments, sends it to the Ludus REST API, and returns the result.

## Good to Know

- **Self-signed certs** — The MCP server accepts self-signed certificates, but only for requests to your Ludus server. It uses a scoped TLS agent, not `NODE_TLS_REJECT_UNAUTHORIZED=0`.
- **API key storage** — Your API key is sent via the `X-API-KEY` header and is never logged or included in tool output. It is stored in plaintext in your MCP client config file.
- **Destructive operations** — The MCP server can call any API operation your API key has access to, including deletes and deploys.
- **Ludus v1** — This server only works with Ludus v2.

## Source

[gitlab.com/badsectorlabs/ludus-mcp](https://gitlab.com/badsectorlabs/ludus-mcp)
