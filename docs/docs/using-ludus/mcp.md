---
title: "🤖 AI Assistants"
sidebar_position: 10
description: Control Ludus from AI coding assistants via MCP and skills
keywords: [mcp, ai, claude, cursor, vscode, automation, skills]
---

# 🤖 AI Assistants

Ludus ships two packages for AI coding assistants. The MCP server handles execution; skills provide context. They work independently or together.

```bash
# MCP server — calls the Ludus API
local:~$ claude mcp add ludus -- npx -y @badsectorlabs/ludus-mcp \
  --url https://<LUDUS_HOST>:8080 --api-key <YOUR_API_KEY>

# Skills — Ludus-specific knowledge for range config, troubleshooting, etc.
local:~$ npx skills add https://gitlab.com/badsectorlabs/ludus-skills
```

## MCP Server

`@badsectorlabs/ludus-mcp` is a local [MCP](https://modelcontextprotocol.io/) server that connects AI coding assistants to the Ludus API. Anything you can do with the [CLI](cli) works through MCP — [templates](templates), [deploy tags](tags), [snapshots](snapshots), [testing mode](../quick-start/testing-mode), [blueprints](blueprints), [roles](roles), and everything else.

```
"snapshot all my VMs, then start testing mode and allow example.com"
```
```
"deploy with the vm-deploy, network, and dns-rewrites tags, then deploy again limited to my new kali box"
```

### Setup

Requires Node.js 20+, your Ludus server URL, and an API key (see `ludus apikey`).

#### Claude Code

```bash
local:~$ claude mcp add ludus -- npx -y @badsectorlabs/ludus-mcp \
  --url https://<LUDUS_HOST>:8080 --api-key <YOUR_API_KEY>
```

#### Codex

```bash
local:~$ codex mcp add ludus -- npx -y @badsectorlabs/ludus-mcp \
  --url https://<LUDUS_HOST>:8080 --api-key <YOUR_API_KEY>
```

#### Claude Desktop

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

#### Cursor

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

#### Environment Variables

`--url` and `--api-key` can also be set via `LUDUS_URL` and `LUDUS_API_KEY`.

### How It Works

```
┌─────────────┐     stdio      ┌──────────────┐     HTTPS      ┌──────────────┐
│  AI Client  │ ◄────────────► │  ludus-mcp   │ ─────────────► │ Ludus Server │
│ (Claude,    │  MCP protocol  │  (local)     │  REST API      │ (remote)     │
│  Cursor...) │                │              │  + API key     │              │
└─────────────┘                └──────────────┘                └──────────────┘
```

The server runs locally as a stdio process. On startup it loads a bundled OpenAPI spec, then attempts to fetch the latest from your server at `/api/v2/openapi`. Three tools are exposed:

- **`list_ludus_operations`** — search and filter available API operations
- **`describe_ludus_operation`** — get parameter and request body schemas for an operation
- **`call_ludus_api`** — execute an API operation

`list` and `describe` read from the parsed spec. `call` builds an HTTP request from the operation definition and your arguments, sends it to the Ludus REST API, and returns the result.

### Good to Know

- **Self-signed certs** — Accepted, but only for requests to your Ludus server. Uses a scoped TLS agent, not `NODE_TLS_REJECT_UNAUTHORIZED=0`.
- **API key storage** — Sent via `X-API-KEY` header, never logged or included in tool output. Stored in plaintext in your MCP client config file.
- **Destructive operations** — The server can call any API operation your key has access to, including deletes and deploys.
- **Ludus v1** — Not supported. This server requires Ludus v2.

## Skills

`ludus-skills` are [agent skills](https://agentskills.io/) that give AI assistants Ludus-specific knowledge. They don't require a running Ludus server.

```bash
local:~$ npx skills add https://gitlab.com/badsectorlabs/ludus-skills
```

| Skill | What it covers |
|-------|----------------|
| **range-config** | Range configuration YAML: VM definitions, domains, networking, validation rules |
| **troubleshooting** | Deployment failures, networking, templates, WireGuard, Proxmox, Ansible |
| **environment-guide** | Pre-built environments: GOAD, Elastic, SCCM, Vulhub, Pivot Lab, and more |
| **ludus-cli** | CLI command syntax, flags, and workflows |

Supported by [Claude Code](https://claude.ai/code), [Cursor](https://cursor.sh), [Windsurf](https://codeium.com/windsurf), and any editor that implements the [Agent Skills](https://agentskills.io/) standard.

## Better Together

With both installed, "build me an Elastic server" does the whole thing:

1. Looks up the Elastic lab requirements from the **environment-guide** skill — templates, roles, `depends_on` wiring, sizing
2. Checks your server for built templates and installed roles via the **MCP server**, installs what's missing
3. Writes a valid range config with the correct VLANs, role assignments, and dependency chain, sets it, and deploys

Troubleshooting works the same way — paste an error and the **troubleshooting** skill identifies the failure category while the **MCP server** pulls range status, deploy errors, and config to narrow it down.

## Source

- MCP Server: [gitlab.com/badsectorlabs/ludus-mcp](https://gitlab.com/badsectorlabs/ludus-mcp)
- Skills: [gitlab.com/badsectorlabs/ludus-skills](https://gitlab.com/badsectorlabs/ludus-skills)
