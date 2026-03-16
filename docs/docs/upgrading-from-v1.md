---
sidebar_position: 9
title: "2️⃣ Upgrading from Ludus 1.x"
---

# 2️⃣ Upgrading from Ludus 1.x to Ludus 2.x

Ludus 2.x replaces the internal SQLite database with [PocketBase](./administration/pocketbase), a more capable embedded database and application framework. When you upgrade a Ludus 1.x server to 2.x, your existing data (users, ranges, VMs, and access grants) is automatically migrated.

The biggest change is Ludus 2 breaks the strict 1-1 mapping of users to ranges. Users and ranges are now separate, and users can have access to multiple ranges without creating additional users.

## What Changes

| Component | Ludus 1.x | Ludus 2.x |
|---|---|---|
| Database | SQLite (`/opt/ludus/ludus.db`) | PocketBase (/opt/ludus/db/*) |
| Range configs | `/opt/ludus/users/<username>/range-config.yml` | `/opt/ludus/ranges/<rangeID>/range-config.yml` |
| API authentication | API keys only | API keys, JWT (web UI) |
| API base path | / | /api/v2 |
| VM to Wireguard traffic | Allowed by default | Blocked by default |

Your existing Proxmox VMs, templates, and network configuration are not modified by the upgrade. Only the Ludus API's internal data store changes.

## After the Upgrade

- **API keys** from Ludus 1.x continue to work. Users do not need new keys.
- **Range configs** are now stored under `/opt/ludus/ranges/<rangeID>/` instead of `/opt/ludus/users/<username>/`. The old files are not removed.
- **The web UI** at `https://<ludus-host>:8080` is now available for users to log in (if licensed) with their email and Proxmox password. Migrated users have the email address of `<proxmox-username>@ludus.internal` and can use their Proxmox password.
- **SSO** via OAuth2 providers can be configured in the [PocketBase admin panel](./administration/sso.md).
- The original SQLite database at `/opt/ludus/ludus.db` is preserved in the event you wish to downgrade or retry the migration
- If you wish to get callbacks to WireGuard clients from VMs, see [this page](./troubleshooting/callbacks-to-wireguard.md).

## How to Upgrade

1. Update Ludus as you normally would:

```shell
# terminal-command-ludus-root
curl -s https://ludus.cloud/install | bash
# Answer 'y' when prompted to update the server
```

Or manually:

```shell
# terminal-command-ludus-root
./ludus-server --update
```

2. The database migration runs automatically the next time the `ludus-admin` service starts. Monitor its progress with:

```shell
# terminal-command-ludus-root
journalctl -u ludus-admin -n 50 -f
```

You will see log lines like:

```
[INFO] Starting migration from SQLite to PocketBase...
[INFO] Migrated range: 2 (User: JD)
[INFO] Making root user a superuser in PocketBase - Password is the ROOT API key
[INFO] Successfully created PocketBase user with ID: rxm2onkmn8g548y
[INFO] Migrated user: JD
[INFO] Migrated VM: 104 (Range: 2)
[INFO] Migrated access: User JD -> Range 20 (Target User: GOADa38bf7)
[INFO] Migrated range config file for user GOADa38bf7
[INFO] Migration from SQLite to PocketBase completed successfully
```

3. Once the migration completes, Ludus 2.x is fully operational. Existing API keys continue to work.

## What the Migration Does

The automatic migration performs these steps inside a single database transaction:

1. **Ranges** — Each range from the SQLite `range_objects` table is created in PocketBase. Pipe-separated `allowedDomains` and `allowedIPs` strings are converted to proper arrays. Proxmox pool access is re-granted to each range owner and the `ludus_admins` group.
2. **Users** — Each user from `user_objects` is created in PocketBase. Passwords are read from `/opt/ludus/users/<username>/proxmox_password`. Proxmox API tokens are created for each user. The ROOT user is re-created as a PocketBase superuser.
3. **VMs** — VM records from `vm_objects` are linked to their corresponding PocketBase range records.
4. **Access grants** — The `range_access_objects` table (which tracked which users could access other users' ranges) is converted into PocketBase relationship records on each user.
5. **Range config files** — Each user's `range-config.yml` is copied from `/opt/ludus/users/<username>/range-config.yml` to `/opt/ludus/ranges/<rangeID>/range-config.yml`.

If any step fails, the entire transaction is rolled back and Ludus will exit. The original SQLite database (`/opt/ludus/ludus.db`) is never modified or deleted.

## The `.sqlite_db_migrated` File

After a successful migration, Ludus creates:

```
/opt/ludus/install/.sqlite_db_migrated
```

This empty sentinel file prevents the migration from running again on subsequent restarts. Ludus checks for both `/opt/ludus/ludus.db` and the absence of `.sqlite_db_migrated` before attempting a migration.

## Troubleshooting

### Checking migration status

To determine whether the migration has already run:

```shell
# terminal-command-ludus-root
ls -la /opt/ludus/install/.sqlite_db_migrated
```

If the file exists, the migration completed successfully.

### Viewing migration logs

Migration output is written to the `ludus-admin` journal:

```shell
# terminal-command-ludus-root
journalctl -u ludus-admin --no-pager | grep -i migrat
```

It may be helpful to enable [debug logging](developers/developer-tips#debug-logging) and trying the migration again (see below).

### Migration failed or was interrupted

If the migration fails, `ludus-admin` will log the error and exit. Because the migration runs in a transaction, a failure leaves PocketBase in a clean state (only the ROOT user exists).

To retry the migration:

1. Remove the sentinel file (if it was partially created):

```shell
# terminal-command-ludus-root
rm -f /opt/ludus/install/.sqlite_db_migrated
```

2. Restart the service:

```shell
# terminal-command-ludus-root
systemctl restart ludus-admin
```

3. Watch the logs for errors:

```shell
# terminal-command-ludus-root
journalctl -u ludus-admin -n 50 -f
```

### Common migration errors

**"Error reading proxmox password for user ..."**

The file `/opt/ludus/users/<username>/proxmox_password` is missing or unreadable. This user will be skipped during migration. You can manually create the user in Ludus 2.x after the migration completes.

**"Could not find range for target user ..."**

An access grant references a user whose range was not migrated (possibly because that user was already removed from Proxmox). This access grant will be skipped. You can re-create it with `ludus range access grant` after the migration.

**"Error creating proxmox API token for user ..."**

Ludus 2.x creates a Proxmox API token for each user during migration. This can fail if the user no longer exists in Proxmox PAM authentication. Verify the user exists with `pveum user list` and retry.

### Re-running the migration from scratch

If you need to completely redo the migration (for example, after fixing a Proxmox user issue):

1. Stop Ludus:

```shell
# terminal-command-ludus-root
systemctl stop ludus-admin ludus
```

2. Remove the PocketBase data directory and the sentinel file:

```shell
# terminal-command-ludus-root
rm -rf /opt/ludus/pb_data
# terminal-command-ludus-root
rm -f /opt/ludus/install/.sqlite_db_migrated
```

3. Start Ludus again. It will re-initialize PocketBase and re-run the migration:

```shell
# terminal-command-ludus-root
systemctl start ludus-admin
```

:::warning

The migration relies on `/opt/ludus/ludus.db` existing. Do not delete your Ludus 1.x database (`ludus.db`) if you are troubleshooting a migration.

:::

### Triggering migration via the CLI or API

Admins can also trigger the SQLite-to-PocketBase migration through the CLI or API:

```shell
# terminal-command-ludus-root
ludus migrate sqlite

# terminal-command-ludus-root
curl -sk -X POST https://127.0.0.1:8081/api/v2/migrate/sqlite \
  -H "X-API-Key: $(cat /opt/ludus/install/root-api-key)"
```

This calls the same migration function and is useful if you want to retry without restarting the service.

## API Changes (v1 → v2)

The Ludus 2.x API has been restructured. The biggest architectural change is that ranges are now independent objects identified by a `rangeID` rather than being tied 1:1 to a `userID`. Most range-related endpoints now accept an optional `rangeID` query parameter in addition to (or instead of) `userID`.

The API base path has also changed from `/` to `/api/v2`.

A second authentication method (JWT via `Authorization: Bearer <token>`) is now supported alongside the existing `X-API-KEY` header.

### Removed Endpoints

These endpoints no longer exist in v2. Clients using them must migrate to the listed alternatives.

| Removed Endpoint | Description | v2 Alternative |
|---|---|---|
| `GET /range/access` | List cross-range access settings | `GET /ranges/accessible` returns ranges the current user can access. `GET /ranges/{rangeID}/users` lists users with access to a specific range. |
| `POST /range/access` | Grant or revoke range access by posting `action`, `targetUserID`, `sourceUserID` | `POST /ranges/assign/{userID}/{rangeID}` to grant access. `DELETE /ranges/revoke/{userID}/{rangeID}` to revoke access. |
| `POST /user/passwordreset` | Reset a user's Proxmox password (admin only) | Use `POST /user/credentials` which now sets both Ludus and Proxmox passwords. |

### Breaking Changes to Existing Endpoints

#### `DELETE /range` — behavior changed

In v1 this endpoint deleted all VMs in a range. In v2 it deletes the range record itself from the database and Proxmox.

| | v1 | v2 |
|---|---|---|
| **Summary** | Stop and delete all range VMs | Delete a range from the database and Proxmox host |
| **Parameters** | `userID` (optional) | `rangeID` (optional), `userID` (optional), `force` (optional boolean) |
| **New response** | — | `409 Conflict` |

To delete only the VMs without removing the range record, use the new `DELETE /range/{rangeID}/vms` endpoint.

#### `GET /range` — response schema changed

The response object replaces `userID` with `rangeID` and adds metadata fields.

| Removed field | Added fields |
|---|---|
| `userID` | `rangeID`, `name`, `description`, `purpose` |

New optional query parameter: `rangeID`.

#### `POST /templates` — request body changed

The single-template `template` string field and `verbose` boolean have been replaced by a `templates` array.

| v1 body | v2 body |
|---|---|
| `template` (string, single template name) | `templates` (array of strings, e.g. `["debian-12-x64-server-template"]`) |
| `verbose` (boolean) | *removed* |
| `parallel` (integer) | `parallel` (integer, unchanged) |

Use `["all"]` as the `templates` value to build every template.

#### `POST /user` — request body changed

New required fields have been added and several read-only fields removed.

| v1 body | v2 body |
|---|---|
| `name` (required) | `name` (required, unchanged) |
| `userID` (required) | `userID` (required, unchanged) |
| `isAdmin` (required) | `isAdmin` (required, unchanged) |
| `proxmoxUsername` (read-only) | *removed* |
| `dateCreated` (read-only) | *removed* |
| `dateLastActive` (read-only) | *removed* |
| — | `email` (**required**, new) |
| — | `password` (new) |

#### `DELETE /user/{userID}` — new parameter

In v1 deleting a user implicitly removed their single associated range. In v2 ranges are independent objects, so a new optional `deleteDefaultRange` (boolean) query parameter controls whether the user's default range is also deleted. If omitted or `false`, the range is left intact and can be reassigned to another user.

#### `GET /` (version check) — response schema changed

The `200` response now includes a `version` field alongside `result`. A `401 Unauthorized` response has been added.

### `rangeID` Parameter Added to Many Endpoints

The following endpoints now accept an optional `rangeID` query parameter, reflecting the fact that ranges are no longer tied 1:1 to users:

- `GET /range`
- `GET /range/ansibleinventory`
- `GET /range/config`
- `GET /range/etchosts`
- `GET /range/logs`
- `GET /range/rdpconfigs`
- `PUT /range/config`
- `PUT /range/poweroff`
- `PUT /range/poweron`
- `POST /range/abort`
- `POST /range/deploy`
- `POST /snapshots/create`
- `POST /snapshots/remove`
- `POST /snapshots/rollback`
- `GET /snapshots/list`
- `POST /testing/allow`
- `POST /testing/deny`
- `POST /testing/update`
- `PUT /testing/start`
- `PUT /testing/stop`

If `rangeID` is not provided, the API uses the authenticated user's default range.

### New Endpoints in v2

These endpoints are new in Ludus 2.x and have no v1 equivalent.

#### Blueprints

| Endpoint | Description |
|---|---|
| `GET /blueprints` | List accessible blueprints |
| `GET /blueprints/{blueprintID}/config` | Get blueprint config |
| `GET /blueprints/{blueprintID}/access/users` | List users with access to a blueprint |
| `GET /blueprints/{blueprintID}/access/groups` | List groups with access to a blueprint |
| `POST /blueprints/from-range` | Create a blueprint from an existing range |
| `POST /blueprints/{blueprintID}/apply` | Apply a blueprint to a range |
| `POST /blueprints/{blueprintID}/copy` | Copy a blueprint |
| `POST /blueprints/{blueprintID}/share/users` | Share a blueprint with specific users |
| `POST /blueprints/{blueprintID}/share/groups` | Share a blueprint with groups |
| `PUT /blueprints/{blueprintID}/config` | Update a blueprint's config |
| `DELETE /blueprints/{blueprintID}` | Delete a blueprint |
| `DELETE /blueprints/{blueprintID}/share/users` | Unshare a blueprint from users |
| `DELETE /blueprints/{blueprintID}/share/groups` | Unshare a blueprint from groups |

#### Groups

| Endpoint | Description |
|---|---|
| `GET /groups` | List all groups |
| `GET /groups/{groupName}/users` | List group members |
| `GET /groups/{groupName}/ranges` | List group ranges |
| `POST /groups` | Create a new group |
| `POST /groups/{groupName}/users` | Add users to a group |
| `POST /groups/{groupName}/ranges` | Add ranges to a group |
| `DELETE /groups/{groupName}` | Delete a group |
| `DELETE /groups/{groupName}/users` | Remove users from a group |
| `DELETE /groups/{groupName}/ranges` | Remove ranges from a group |

#### Ranges

| Endpoint | Description |
|---|---|
| `POST /ranges/create` | Create a new range (ranges are no longer auto-created with users) |
| `GET /ranges/accessible` | List ranges the current user can access |
| `GET /ranges/{rangeID}/users` | List users with access to a range (admin only) |
| `POST /ranges/assign/{userID}/{rangeID}` | Assign a range to a user (admin only) |
| `DELETE /ranges/revoke/{userID}/{rangeID}` | Revoke range access from a user (admin only) |
| `DELETE /range/{rangeID}/vms` | Stop and delete all VMs in a range (without deleting the range) |

#### VMs

| Endpoint | Description |
|---|---|
| `DELETE /vm/{vmID}` | Destroy a single VM |
| `GET /vm/console/ticket` | Get a console WebSocket ticket |
| `GET /vm/console/view` | Connect to a VM console via WebSocket |

#### User & Identity

| Endpoint | Description |
|---|---|
| `GET /whoami` | Return identity of the authenticated user |
| `GET /user/default-range` | Get the user's default range ID |
| `POST /user/default-range` | Set the user's default range ID |
| `GET /user/memberships` | Get the user's group memberships |

#### Ansible

| Endpoint | Description |
|---|---|
| `GET /ansible/subscription-roles` | List available subscription (enterprise) roles |
| `POST /ansible/subscription-roles` | Install subscription roles |
| `POST /ansible/role/vars` | Get variables for one or more Ansible roles |
| `PATCH /ansible/role/scope` | Move or copy roles between global and local scopes |

#### Infrastructure & Migration

| Endpoint | Description |
|---|---|
| `GET /diagnostics` | Run host diagnostics |
| `GET /license` | Retrieve the Ludus license |
| `POST /migrate/sdn` | Migrate to SDN networking |
| `GET /migrate/sdn/status` | Check SDN migration status |
| `POST /migrate/sqlite` | Trigger SQLite-to-PocketBase migration |
