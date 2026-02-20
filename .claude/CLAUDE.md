# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Ludus is a cyber range platform built on Proxmox for creating test and development environments. It automates the deployment of virtual networks including Active Directory domains, workstations, and attack VMs.

## Repository Structure

This is a Go monorepo using a `go.work` workspace with these modules:
- **ludus-server**: Server binary that installs Proxmox, runs the API, and contains embedded Ansible/Packer
- **ludus-api**: REST API built on PocketBase, handles all server-side logic
- **ludus-client**: CLI client using Cobra for interacting with the API
- **ludus-antisandbox-plugin**: Plugin for anti-sandbox features (Go plugin .so)
- **ludus-enterprise-plugin**: Plugin for enterprise features like KMS and WireGuard
- **ludus-gui**: React/Next.js web interface (separate from Go workspace)
- **docs**: Docusaurus documentation site

## Development Commands

### Building and Deploying (requires a Ludus development server)
```bash
# Full development workflow - syncs code to remote, builds, and installs
./dev.sh -t <hostname>           # Build and deploy server to remote host
./dev.sh -t <hostname> -l        # Build, deploy, and tail logs
./dev.sh -t <hostname> -d        # Build with debug mode
./dev.sh -t <hostname> -c        # Also build client locally
./dev.sh -t <hostname> -w        # Also build web UI

# Individual component dev scripts (run on the Ludus server)
ludus-server/dev.sh              # Build server binary, install to /opt/ludus
ludus-server/dev.sh docs         # Build docs and embed into server
ludus-client/dev.sh              # Build client, install to /usr/local/bin
```

### Running Tests
```bash
# Go tests (in ludus-antisandbox-plugin)
cd ludus-antisandbox-plugin && go test ./...

# Ansible linting
ansible-lint ludus-server/ansible/

# GUI tests (in ludus-gui directory)
bun run test          # All tests
bun run test:unit     # Unit tests only
bunx vitest run <file>  # Single test file
```

### Documentation
```bash
cd docs
yarn install
yarn start    # Development server
yarn build    # Production build
```

## Architecture

### API Layer (ludus-api)
- Built on PocketBase with custom routes at `/api/v2`
- `routers.go`: Route registration, plugin system initialization
- `api_*.go`: API endpoint handlers grouped by domain (range, template, user, group, etc.)
- Plugin interface in `plugins.go`: Plugins implement `LudusPlugin` interface

### Server (ludus-server)
- `main.go`: Entry point, handles install process and API serving
- Embeds `ansible/`, `packer/`, `ci/` directories into the binary
- Runs as both root (admin API on :8081) and ludus user (user API on :8080)
- Ansible playbooks in `ansible/range-management/` for VM deployment and configuration

### Client (ludus-client)
- Cobra-based CLI, commands in `cmd/` directory
- `cmd/root.go`: Root command setup, global flags, config loading
- Uses Viper for config management (`~/.config/ludus/config.yml`)
- API key stored in system keyring via `go-keyring`

### Plugin System
Plugins are Go `.so` files loaded at runtime:
- Must implement `LudusPlugin` interface (Initialize, RegisterRoutes, etc.)
- Plugins can embed their own Ansible playbooks/Packer configs
- Community plugins: `/opt/ludus/plugins/community/`

## Commit Message Format

Follow conventional commits with emoji prefixes:
```
feat: ✨ description
fix: 🐛 description
docs: 📚 description
refactor: 🔨 description
test: 🚨 description
ci: 🤖 description
```

## Key Configuration Files

- `go.work`: Go workspace definition for all modules
- `.ansible-lint`: Ansible linting configuration
- `.gitlab-ci.yml`: CI pipeline with extensive integration tests
- `ludus-server/ansible/range-management/ludus.yml`: Main deployment playbook

## GUI (ludus-gui)

The GUI is a separate Next.js static application (see `ludus-gui/CLAUDE.md` for details):
- Uses `bun` as package manager
- Static export for offline/self-hosted deployment
- React Flow for network topology visualization

## Go Best Practices:

- Use slices.Contains() instead of writing custom helper functions for checking if a string is in a slice