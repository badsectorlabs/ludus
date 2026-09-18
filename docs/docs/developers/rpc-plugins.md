---
title: "RPC plugins"
sidebar_position: 6
---

# RPC plugins

Ludus runs extensions as separate processes with
[`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin). The server and
plugins communicate over local `net/rpc` connections.

This replaces Go's standard-library plugin system. Ludus plugins are regular
Linux executables, not `.so` shared objects, and are built with a normal
`go build` command.

## How loading works

When the server loads a plugin, it:

1. Starts the plugin executable as a subprocess.
2. Completes the Ludus handshake and checks the RPC protocol version.
3. Reads the plugin name and route declarations.
4. Sends the current server state and an authenticated connection to the
   parent's PocketBase Web API.
5. Registers proxy handlers for the declared routes.
6. Registers any plugin jobs with the server scheduler.
7. Forwards requests and responses over RPC until shutdown.

The parent Ludus process remains the PocketBase owner. Plugins never open
`data.db` or bootstrap a second PocketBase app. The host sends the HTTP request
and the IDs of the authenticated user and selected range. The plugin reloads
those records through the parent's Web API before calling its route handler.
Enterprise jobs use the same API for reads and writes. Shared Ansible helpers
also query range access and ROOT Proxmox credentials through this connection
when called from a plugin; they must not dereference the host-only PocketBase
app. The plugin's cached root Proxmox client is cleared when its runtime closes.
The host starts scheduled jobs only after PocketBase has bound its listener,
so their initial record queries can reach the parent API.

The plugin connection is restricted to a loopback address. It uses a
plugin-specific superuser token and pins the parent server's TLS certificate
when the endpoint uses HTTPS. The plugin refreshes the token through
PocketBase before it expires. Closing the plugin removes its idle HTTP
connections.

Plugin jobs also run through the host. For example, the enterprise license job
returns updated license and entitlement state to the server. Base Ludus
refreshes the enterprise executable; management of enterprise add-ons belongs
to the enterprise plugin.

## Protocol compatibility

The current Ludus RPC protocol version is defined in
`ludus-api/pluginrpc/plugin.go`. The protocol is the compatibility boundary
between independently built server and plugin binaries.

Adding optional fields that older binaries can ignore is normally compatible.
Changing the meaning or type of an existing field, removing a required field,
or changing method behavior may require a protocol version bump. Keep wire
messages limited to stable data types. Do not add PocketBase records,
`*core.RequestEvent`, `fs.FS`, functions, or `*ludusapi.Server` to the RPC
contract.

A protocol mismatch produces a plugin loading error. This is separate from Go
package compatibility: changing an unrelated function in `ludus-api` no longer
prevents an otherwise compatible plugin from loading.

## Optional VM lifecycle hooks

A plugin advertises lifecycle support through `Metadata.VMHooks`. Every field
defaults to false. Ludus skips lifecycle RPC entirely for plugins that omit the
metadata, which preserves the normal Proxmox start, stop, status, address, and
delete paths.

Plugins can advertise `Start`, `Stop`, `Status`, `BeforeDelete`, and `Address`.
Set `Select` when only some VMs belong to the plugin. Ludus first sends a
`VMHookSelect` request for each candidate VM and dispatches the operation only
when the plugin returns `Selected: true`. The selector can use the range ID and
VM name to read the range's arbitrary YAML during deployment planning, when a
VMID may not exist yet. Ludus verifies the VMID, name, type, and pool against
Proxmox again before privileged runtime operations.

Lifecycle support is an optional interface on the plugin implementation:

```go
func (p *Plugin) Metadata() (pluginrpc.Metadata, error) {
	return pluginrpc.Metadata{
		Name: "example",
		VMHooks: pluginrpc.VMHookCapabilities{
			Select: true,
			Start:  true,
		},
	}, nil
}

func (p *Plugin) VMHook(request pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
	switch request.Operation {
	case pluginrpc.VMHookSelect:
		return pluginrpc.VMHookResponse{Selected: pluginOwnsVM(request)}, nil
	case pluginrpc.VMHookStart:
		return pluginrpc.VMHookResponse{Decision: pluginrpc.VMHookHandled}, startVM(request)
	default:
		return pluginrpc.VMHookResponse{Decision: pluginrpc.VMHookContinue}, nil
	}
}
```

Plugins without lifecycle metadata do not implement `VMHook`. Adding the
optional fields therefore does not require rebuilding or changing existing RPC
plugins.

## Artifact names and installation

Ludus uses these executable names:

| Plugin | Installed filename | Service account | Directory |
| --- | --- | --- | --- |
| Enterprise | `ludus-enterprise.plugin` | `ludus` | `/opt/ludus/plugins/enterprise/` |
| Enterprise admin | `ludus-enterprise.plugin` | `root` | `/opt/ludus/plugins/enterprise/admin/` |

Community plugins use the `.plugin` suffix and belong in
`/opt/ludus/plugins/community/` or
`/opt/ludus/plugins/community/admin/`, depending on which server process should
load them.

Plugin files must be executable:

```shell
chmod 0755 /opt/ludus/plugins/enterprise/ludus-enterprise.plugin
chmod 0755 /opt/ludus/plugins/enterprise/admin/ludus-enterprise.plugin
```

Release artifacts include the plugin version in the download name, such as
`ludus-enterprise_2.3.0.plugin`. Plugin versions can advance independently of
the server. The server installs the artifact under the unversioned filename
shown above.

At startup and on each scheduled license check, the host asks Keygen for the
latest stable release of the entitled enterprise plugin. A downloaded
artifact must pass its Keygen signature check, complete the RPC handshake, and
report the expected plugin name and version before it replaces the installed
file. A plugin that is already running keeps serving requests until Ludus is
restarted; the replacement is used on the next start.

For a first install or a development version, the host lists published stable
releases, then checks for an upgrade from the discovered release to select the
highest semantic version. This does not require a `latest` tag. Release
publishers must provide the versioned `.plugin` executables; existing `.so`
artifacts cannot be loaded by the RPC host.

## Building the enterprise plugins

The enterprise plugin source directory is available only in an authorized
development checkout. From the workspace root, run:

```shell
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -ldflags "-s -w" \
  -o ludus-enterprise-plugin/ludus-enterprise.plugin \
  ./ludus-enterprise-plugin
```

The builds do not require CGO or a Linux C cross-compiler. The `dev.sh` script
in each plugin directory uses the same build mode and installs the executable
under `/opt/ludus/plugins` using an atomic rename. This permits rebuilding while
the previous executable is running without `Text file busy` errors. Restart
Ludus to activate the replacement; the top-level `dev.sh` does this when it
rebuilds the server.

## Adding a plugin route

A plugin route has three matching declarations:

1. A placeholder route in `ludus-api/api_placeholder.go` so PocketBase exposes
   the endpoint even when the plugin is absent.
2. Route metadata in the plugin entrypoint.
3. A route-name-to-handler entry in the plugin's `Handle` method.

The metadata name is the RPC dispatch key. Its HTTP method and path register the
server-side proxy. Keep all three values synchronized.

The handler can continue to use `*core.RequestEvent`. The RPC runtime rebuilds
that event inside the plugin process, including headers, request body, auth
record, user record, and selected range. Records are reconstructed from JSON
returned by the parent PocketBase Web API. Handler updates and scheduled-job
updates also go through that API, so the parent's validation hooks, realtime
broadcasts, caches, and file lifecycle remain active.

## Testing real plugin processes

Build a plugin executable, then provide its absolute path to the integration
test:

```shell
cd ludus-api
LUDUS_PLUGIN_PATH=/absolute/path/example.plugin \
go test -run '^TestRPCPluginInteroperability$' -v
```

The test starts the executable, initializes its PocketBase API client,
registers every declared route, and checks that the subprocess shuts down
cleanly. When testing the enterprise plugin, it also runs the inactivity job
with an undeployed range, calls the KMS status endpoint, and exercises a record
update through a fake parent PocketBase API.

To verify independent-build compatibility, build the plugin first, make a
harmless change to host-side `ludus-api`, and run the integration test without
rebuilding the plugin. Remove the temporary change afterward and rerun the
test with current-source release binaries.

## Plugin repository CI

Plugin repositories can include `ludus-server/ci/plugin.gitlab-ci.yml` from
the Ludus repository. The shared
build job runs on a disposable `range_built_user` VM, checks out the current
Ludus `main` branch, replaces the matching plugin directory with the pipeline
checkout, and then:

1. runs the plugin's Go tests;
2. builds the plugin, server, client, and dynamic inventory;
3. runs `TestRPCPluginInteroperability` against the real plugin executable.

The functional job reuses the same disposable VM and tests the plugin through
the HTTPS API. Enterprise CI covers licensing, quotas, inactivity settings,
KMS, and inbound and outbound WireGuard. Add-on functional tests belong in
their respective plugin repositories. A successful
pipeline releases the VM claim. A failed pipeline leaves the VM running so the
state and collected journal artifacts can be inspected.

Licensed plugin projects must define `LUDUS_LICENSE_KEY` as a masked CI
variable. The functional jobs install their artifacts in the production
enterprise plugin directories and start the server with that license.

Tagged licensed plugin pipelines require `KEYGEN_HOST`,
`KEYGEN_ACCOUNT_ID`, `KEYGEN_TOKEN`, the matching `LUDUS_*_PACKAGE_ID`, and
the matching `LUDUS_*_ENTITLEMENT_ID` as protected variables. The upload job
adds the entitlement constraint, publishes the versioned executable to the
licensed Keygen package, and moves that package's `latest` tag before the
GitLab release can be created.

Each plugin pipeline must set `LUDUS_PLUGIN_MODULE` and `LUDUS_PLUGIN_BINARY`.
It must also use one `LUDUS_CI_SERIES` value for its
build, functional, and VM-release jobs. Keep tests on the disposable VM; do not
run package installation, VM mutation, or plugin functional tests on the
Proxmox host.
