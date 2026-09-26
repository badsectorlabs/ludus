---
title: "RPC plugins"
sidebar_position: 6
---

# RPC plugins

For the end-to-end upload, permissions, LXC, and range-machine data paths, see
[Plugin architecture](./plugin-architecture.md).

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
superuser token for `root@ludus.internal` and pins the parent server's TLS certificate
when the endpoint uses HTTPS. The plugin refreshes the token through
PocketBase before it expires. Closing the plugin removes its idle HTTP
connections. This is privileged backend access, not a restricted per-plugin
identity or a tenant isolation boundary.

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

A server-installed plugin's legacy route has three matching declarations:

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

## Resources → Plugins

The GUI has one **Plugins** page under **Resources**, alongside Blueprints,
Templates, and Ansible. Plugins with frontends open inside this page. Adding a
plugin no longer requires adding a sidebar item or rebuilding the GUI.

The integration has three parts:

1. A persistent resource record stores ownership, sharing, approval, and package
   identity. These records cannot be edited through the ordinary PocketBase API.
2. Normal middleware and the generic dispatcher check range access and current
   plugin access on every range-scoped request before calling RPC.
3. A sandboxed iframe renders a self-contained plugin page. A versioned message
   bridge provides authenticated API requests, range selection, theme, downloads,
   confirmations, and notifications. The iframe never receives the login token.

### Access and lifecycle

Uploads default to the authenticated uploader only. Owners can share with
specific Ludus user IDs. Administrators can select **Install globally** during
upload (`POST /plugins/install?allUsers=true`) or enable **All users** later and
retain management access to every plugin. Visibility does not grant access to
another user's ranges. Uploading while impersonating another user does not change
the authenticated uploader's ownership.

An upload is inert until its owner or an administrator selects **Activate**.
Only administrators can manage global plugins, including activation and deletion.
Activation may
execute code with the Ludus service account's privileges and provide privileged
PocketBase credentials. Only activate trusted packages: resource access controls
are **not an operating-system sandbox**. A package checksum identifies the uploaded
bytes; it is not a publisher signature. UI-only packages also require activation.

**Remove** opts a non-administrator out of a shared/private plugin without
affecting other users. **Show plugins available to add back** lets an entitled
user undo that choice. **Delete** permanently removes an uploaded plugin's
server record and package files and stops its process. Personal plugins can be
deleted by their owner or an administrator, and global plugins only by an
administrator. Deletion does not remove plugin-created data or undo changes to
range machines. An owner can also delete a pending submission. To use the same
plugin again after deletion, upload its package again.

To install another version with the same plugin ID, select **Replace existing
version** (`POST /plugins/install?replace=true`). Only the personal plugin's owner
or an administrator can replace it; global and system scope protections still
apply. Replacement preserves the owner and access settings, stops the previous
process, and leaves the new package pending activation. A failed package upload
keeps the previous version intact. Superseded package files are removed; data the
plugin created separately remains. A deleted ID can be uploaded again without
selecting replacement.

System-installed plugins remain managed by their server installer. Those that
advertise a stable `Metadata.ID` are discovered automatically and initially visible
only to administrators, who can share them. Their legacy routes enforce the same
resource access. Older plugins without an ID retain their existing route behavior
and do not appear here. System and uploaded copies have separate uniqueness
scopes; use `resourceID` to distinguish installations with the same plugin ID.

### Upload package format

Upload a ZIP with exactly the declared files below. Do not include directory
entries, symlinks, a containing directory, or additional assets:

```text
plugin.json
plugin           # optional executable for the server's OS/architecture
ui/index.html    # optional, self-contained HTML with inline JS/CSS
```

Example `plugin.json`:

```json
{
  "schemaVersion": 1,
  "id": "example-plugin",
  "name": "Example Plugin",
  "version": "1.0.0",
  "description": "Inspect the selected range",
  "author": "Example author",
  "executable": "plugin",
  "protocolVersion": 2,
  "ui": "ui/index.html",
  "rangeScoped": true
}
```

IDs use 2–64 lowercase letters, digits, or hyphens and start with a letter.
At least one of `executable` or `ui` is required. Omit unused optional fields.
The executable's `Metadata.ID`, `Name`, and `Version` must match the manifest.
The ZIP is limited to 128 MiB, its expanded executable to 128 MiB, its HTML to
2 MiB, and its manifest to 64 KiB. Each user may retain twenty packages,
with at most five pending activation. Deleting unused packages reclaims this
allowance.
Each user can upload a personal copy of the same plugin ID. A separate global
copy may coexist, and system-installed copies remain separate. Responses include
`resourceID` identifying the installation, `ownerUserID`, and `isOwner`. Use
`resourceID` in management, UI, and RPC paths so actions affect only that copy.
Legacy plugin-ID URLs prefer your personal copy, then a global copy; ambiguous
shared/admin lookups require an explicit resource ID.

Replacement defaults to your personal copy (or the global copy when uploading
with `allUsers=true`). To replace a particular installation, pass
`replace=true&resourceID=...`; its manifest ID must match. The GUI's **Plugin to
replace** selector makes this explicit. Moving a copy to a scope that already
contains that plugin returns a conflict without changing either installation.

Managed plugin routes use exact literal paths and methods in `Metadata.Routes`.
No host placeholder or GUI rebuild is required. The host dispatches
`/api/v2/plugins/{id}/rpc/{path}` to the corresponding declared `/{path}` route.
GET, POST, PUT, PATCH, and DELETE are supported; wildcard/parameterized route
declarations are not supported in uploaded packages. RPC receives the legacy API
path so existing route handlers can be reused. Jobs stop with the resource process;
resource plugins cannot replace the host's license state.

VM lifecycle hooks and root-side installation are not supported through GUI
uploads because lifecycle calls do not yet carry resource ownership. Install those
plugins with an administrator's server-side installer. The current Traffic
Observer GUI package prepares its CA and embedded Ansible assets in the Ludus
LXC during activation. Its explicit Install / repair action prepares a selected
range; opening or sharing its GUI does not prepare ranges automatically, and
this uploaded version does not install a new-machine CA lifecycle hook.

### Frontend bridge v1

The host injects `window.ludus` before plugin scripts run:

```javascript
const context = await ludus.context();
// { pluginID, rangeID, theme, bridgeVersion: 1 }
const status = await ludus.request('/status');
await ludus.request('/capture', { method: 'POST', body: { enabled: true } });
await ludus.download('/export', {
  query: { filter: 'tcp port 443' }, filename: 'capture.pcap'
});
if (await ludus.confirm('Delete this capture?')) {
  await ludus.request('/capture', { method: 'DELETE' });
}
await ludus.toast('Capture updated');
window.addEventListener('ludus:context', event => {
  // Clear old range data, then reload using event.detail.rangeID/theme.
});
```

`request` returns parsed JSON and rejects on errors. Query values must be strings,
numbers, or booleans. The host owns `rangeID` and `userID`; plugins cannot override
them or call another plugin's namespace. The iframe cannot access parent DOM,
cookies, storage, external scripts, frames, or direct network connections. Bundle
assets inline (or use data URLs for images/fonts). The bridge validates the sending
frame, limits concurrent calls to eight, and aborts requests when context changes.
Callbacks should discard stale results after a range change. No token is sent to
the plugin page. Server-side plugins are still trusted as described above.

Bridge requests are limited to 8 MiB. Browser downloads are limited to 64 MiB;
large PCAPs need narrower filters. The existing RPC response transport buffers
responses and does not stream multi-gigabyte captures. Streaming/chunked exports
require a future RPC capability; the 10 GB capture rotation setting is not a browser
download-size guarantee.

Server-installed plugins can embed the frontend directly in their executable:

```go
//go:embed ui/index.html
var frontend string

// Include these optional fields in the existing Metadata response:
// ID: "example-plugin",
// Description: "Inspect the selected range",
// Author: "Example author",
// UI: &pluginrpc.UIContribution{HTML: frontend, RangeScoped: true},
```

This optional metadata extension preserves RPC protocol v2 compatibility.
Existing binaries do not need rebuilding for unrelated host changes. A plugin
must be rebuilt once to add its ID/frontend metadata. Deploy the updated host and
GUI together to enable Resources → Plugins, then independently install compatible
plugin releases. Traffic Observer's old `/ui/traffic-observer/` bookmark redirects
to `/ui/plugins/?id=traffic-observer`.

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
