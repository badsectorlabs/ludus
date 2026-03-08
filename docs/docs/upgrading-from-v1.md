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

Your existing Proxmox VMs, templates, and network configuration are not modified by the upgrade. Only the Ludus API's internal data store changes.

## After the Upgrade

- **API keys** from Ludus 1.x continue to work. Users do not need new keys.
- **Range configs** are now stored under `/opt/ludus/ranges/<rangeID>/` instead of `/opt/ludus/users/<username>/`. The old files are not removed.
- **The web UI** at `https://<ludus-host>:8080` is now available for users to log in (if licensed) with their email and Proxmox password. Migrated users have the email address of `<proxmox-username>@ludus.internal` and can use their Proxmox password.
- **SSO** via OAuth2 providers can be configured in the [PocketBase admin panel](./administration/sso).
- The original SQLite database at `/opt/ludus/ludus.db` is preserved in the event you wish to downgrade or retry the migration

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
