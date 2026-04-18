---
sidebar_position: 1
title: "🧑‍💻 Ludus CLI"
---

# 🧑‍💻 Ludus CLI

The Ludus CLI is the primary method of interacting with a Ludus server.
It uses the Ludus REST API to perform actions for the user, and has helpful wrappers designed to aid troubleshooting issues.

The Ludus CLI uses the pattern `ludus COMMAND ARG --FLAG`. Each command has its own help that can be accessed with `ludus [command] --help`.

## Configuration

All global flags can also be set with environment variables in the format `LUDUS_FLAG` where FLAG is the flag long form string in all upper case.

```
local:~$ LUDUS_URL=https://192.168.1.103:8080
local:~$ ludus version --verbose
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:102 Using config file: /Users/user/.config/ludus/config.yml
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:106 --- Configuration from cli and read from file ---
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:108 	url = https://10.98.108.2:8080
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:108 	proxy =
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:108 	verify = %!s(bool=false)
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:108 	user =
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:108 	verbose = %!s(bool=true)
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:108 	json = %!s(bool=false)
[DEBUG] 2024/02/09 15:38:31 ludus/cmd.initConfig:root.go:123 ---
```

The ludus CLI looks for a configuration file at `$HOME/.config/ludus/config.yml`, and if found, uses its values during execution.
The configuration file values are the lowest precedence, followed by ENV variables, and finally command line flags.

An example configuration file is shown below. You only need to set the values you wish to change.
The API key cannot be set in the config file and must be set in the system keyring or environment variable `LUDUS_API_KEY`.

```yaml title="config.yml"
url: https://192.168.1.103:8080
json: false
verify: false
proxy: http://127.0.0.0.1:8000
user: JD
```

## Ludus

An application to control Ludus

Ludus is a CLI application to control a Ludus server
This application can manage users as well as ranges.

Ludus is a project to enable teams to quickly and
safely deploy test environments (ranges) to test tools and
techniques against representative virtual machines.

```
Usage:
  ludus [command]

Available Commands:
  ansible      Perform actions related to ansible roles and collections
  antisandbox  Install and enable anti-sandbox for VMs (enterprise)
  apikey       Store your Ludus API key in the system keyring
  blueprint    Perform actions related to blueprints
  diagnostics  Get system diagnostics from the Ludus server
  groups       Perform actions related to groups
  kms          Manage Windows license tasks (enterprise only)
  migrate      Migration commands
  power        Control the power state of range VMs
  range        Perform actions on your range
  snapshots    Manage snapshots for VMs
  templates    List, build, add, or get the status of templates
  testing      Control the testing state of the range
  update       Update the Ludus client or server
  users        Perform actions related to users
  version      Prints the version of this ludus binary
  vm           Perform actions on VMs

Flags:
        --config string   config file (default is $HOME/.config/ludus/config.yml)
        --json            format output as json
        --proxy string    HTTP(S) Proxy URL
    -r, --range string    A range ID to operate on (uses default range if not specified)
        --url string      Server Host URL (default "https://198.51.100.1:8080")
    -u, --user string     A user ID to impersonate (only available to admins)
        --verbose         verbose client output
        --verify          verify the HTTPS certificate of the Ludus server
```

## Ansible

Perform actions related to ansible roles and collections

```
Usage:
  ludus ansible [command]

Available Commands:
  collection          Perform actions related to ansible collections
  role                Perform actions related to ansible roles
  subscription-roles  Perform actions related to subscription Ansible roles
```

### Ansible Collection

Perform actions related to ansible collections

```
Usage:
  ludus ansible collection [command]

Available Commands:
  add   Add an ansible collection to the ludus host
  list  List available user Ansible collections on the Ludus host
```

#### Ansible Collection Add

Add an ansible collection to the ludus host

Specify a collection name (to pull from galaxy.ansible.com), or a URL to a tar.gz collection artifact

```
Usage:
  ludus ansible collection add [flags]

Flags:
    -f, --force            force the collection to be added
        --version string   the collection version to install
```

#### Ansible Collection List

List available user Ansible collections on the Ludus host

Get the name and version of available ansible collections on the Ludus host installed by the user (default Ansible collections are not shown)

```
Usage:
  ludus ansible collection list [flags]
```

### Ansible Role

Perform actions related to ansible roles

```
Usage:
  ludus ansible role [command]

Available Commands:
  add    Add an ansible role to the ludus host
  list   List available Ansible roles on the Ludus host
  rm     Remove an ansible role from the ludus host
  scope  Move or copy roles between global and local scopes
```

#### Ansible Role Add

Add an ansible role to the ludus host

Specify a role name (to pull from galaxy.ansible.com), a URL, or a local path to a role directory

```
Usage:
  ludus ansible role add <rolename | roleurl | -d directory> [flags]

Flags:
    -d, --directory string   the path to the local directory of the role to install
    -f, --force              force the role to be added
    -g, --global             install the role for all users
        --version string     the role version to install
```

#### Ansible Role List

List available Ansible roles on the Ludus host

Get the name and version of available ansible roles on the Ludus host

```
Usage:
  ludus ansible role list [flags]
```

#### Ansible Role RM

Remove an ansible role from the ludus host

Specify a role name to remove from the ludus host

```
Usage:
  ludus ansible role rm <rolename> [flags]

Flags:
    -d, --directory string   the path to the local directory of the role to install
    -f, --force              force the role to be added
    -g, --global             install the role for all users
        --version string     the role version to install
```

#### Ansible Role Scope

Move or copy roles between global and local scopes

Move or copy one or more Ansible roles between global (all users) and local (current user) installation scopes. Provide scope as 'global' or 'local', followed by a comma-separated list of role names.

```
Usage:
  ludus ansible role scope <global|local> <rolename>[,<rolename>,...] [flags]

Flags:
    -c, --copy   copy the role instead of moving it (keeps the source)
```

### Ansible Subscription Roles

Perform actions related to subscription Ansible roles

```
Usage:
  ludus ansible subscription-roles [command]

Available Commands:
  install  Install subscription Ansible roles
  list     List available subscription Ansible roles
```

#### Ansible Subscription Roles Install

Install subscription Ansible roles

Install one or more subscription Ansible roles using a comma-separated list of role names

```
Usage:
  ludus ansible subscription-roles install [flags]

Flags:
    -f, --force          force installation even if role already exists
    -g, --global         install the roles globally for all users
    -n, --names string   comma-separated list of subscription role names to install (required)
```

#### Ansible Subscription Roles List

List available subscription Ansible roles

Get the list of available subscription Ansible roles from the Ludus subscription service

```
Usage:
  ludus ansible subscription-roles list [flags]
```

## Antisandbox

Install and enable anti-sandbox for VMs (enterprise)

```
Usage:
  ludus antisandbox [command]

Available Commands:
  enable            Enable anti-sandbox for a VM or multiple VMs (enterprise)
  install-custom    Install the custom QEMU and OVMF packages for anti-sandbox features (enterprise)
  install-standard  Install the standard QEMU and OVMF packages (enterprise)
  status            Get the status of anti-sandbox package installation (enterprise)
```

### Antisandbox Enable

Enable anti-sandbox for a VM or multiple VMs (enterprise)

```
Usage:
  ludus antisandbox enable

Flags:
        --drop-files                    drop random pdf, doc, ppt, and xlsx files on the desktop and downloads folder of the VMs
        --no-prompt                     skip the confirmation prompt
        --org string                    The RegisteredOrganization value to use for the VMs
        --owner string                  The RegisteredOwner value to use for the VMs
        --persist                       persist SystemBiosVersion and CPU the changes to the VMs via a non-obvious scheduled task
        --processor-identifier string   The Identifier value to use for the VMs (e.g. Intel64 Family 6 Model 142 Stepping 10)
        --processor-name string         The ProcessorNameString value to use for the VMs (e.g. Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz)
        --processor-speed string        The ~Mhz value to use for the VMs in MHz (e.g. 2600)
        --processor-vendor string       The VendorIdentifier value to use for the VMs (e.g. GenuineIntel or AuthenticAMD)
        --product string                The Product value to use for the SMBIOS information (e.g. Latitude 7420)
        --system-bios-version string    The SystemBiosVersion value to use for the VMs (e.g. 1.18.0)
        --vendor string                 The Vendor value to use for the SMBIOS information (Dell, HP, Lenovo, or Google)
    -n, --vmids string                  A VM ID or name (104) or multiple VM IDs or names (104,105) to enable anti-sandbox on
```

### Antisandbox Install Custom

Install the custom QEMU and OVMF packages for anti-sandbox features (enterprise)

```
Usage:
  ludus antisandbox install-custom
```

### Antisandbox Install Standard

Install the standard QEMU and OVMF packages (enterprise)

```
Usage:
  ludus antisandbox install-standard
```

### Antisandbox Status

Get the status of anti-sandbox package installation (enterprise)

```
Usage:
  ludus antisandbox status
```

## Apikey

Store your Ludus API key in the system keyring

This command stores the Ludus API key in the system
keyring. This is implemented differently on different OS's but
is more secure than writing unencrypted to a file.

```
Usage:
  ludus apikey [flags]
```

## Completion

Generate the autocompletion script for ludus for the specified shell.
See each sub-command's help for details on how to use the generated script.

On macOS (assuming brew is installed) you will need to add the following to `~/.zshrc`:

```
fpath=(/opt/homebrew/share/zsh/site-functions/ $fpath)
autoload -U compinit; compinit
```

and then run `ludus completion zsh > $(brew --prefix)/share/zsh/site-functions/_ludus`.
Any new shells will have ludus completion loaded.

```
Usage:
  ludus completion [command]

Available Commands:
  bash        Generate the autocompletion script for bash
  fish        Generate the autocompletion script for fish
  powershell  Generate the autocompletion script for powershell
  zsh         Generate the autocompletion script for zsh
```

## Blueprint

Perform actions related to blueprints

```
Usage:
  ludus blueprint [command]

Available Commands:
  access   Inspect blueprint access by users or groups
  apply    Apply a blueprint configuration to a range
  config   Get blueprint configuration content
  create   Create a blueprint from a range or existing blueprint
  list     List all blueprints you can access
  rm       Delete a blueprint you own
  share    Share a blueprint with groups or users
  unshare  Remove blueprint sharing from groups or users
```

### Blueprint Access

Inspect blueprint access by users or groups

```
Usage:
  ludus blueprint access [command]

Available Commands:
  groups  List shared groups and their users for a blueprint
  users   List users with access to a blueprint
```

#### Blueprint Access Groups

List shared groups and their users for a blueprint

```
Usage:
  ludus blueprint access groups
```

#### Blueprint Access Users

List users with access to a blueprint

```
Usage:
  ludus blueprint access users
```

### Blueprint Apply

Apply a blueprint configuration to a range

```
Usage:
  ludus blueprint apply

Flags:
        --force                 force apply even when testing is enabled on the target range
    -t, --target-range string   target rangeID to apply the blueprint to (optional)
```

### Blueprint Config

Get blueprint configuration content

```
Usage:
  ludus blueprint config [command]

Available Commands:
  get  Get the raw configuration for a blueprint
```

#### Blueprint Config Get

Get the raw configuration for a blueprint

```
Usage:
  ludus blueprint config get
```

### Blueprint Create

Create a blueprint from a range or existing blueprint

```
Usage:
  ludus blueprint create

Flags:
    -d, --description string      description for the new blueprint (optional)
    -b, --from-blueprint string   source blueprintID to copy from (optional)
    -s, --from-range string       source rangeID to create the blueprint from (optional)
        --id string               blueprint ID for the new blueprint (required for --from-range, optional for --from-blueprint)
    -n, --name string             name for the new blueprint (optional)
```

### Blueprint List

List all blueprints you can access

```
Usage:
  ludus blueprint list
```

### Blueprint RM

Delete a blueprint you own

```
Usage:
  ludus blueprint rm

Flags:
        --no-prompt   skip the confirmation prompt
```

### Blueprint Share

Share a blueprint with groups or users

```
Usage:
  ludus blueprint share [command]

Available Commands:
  group  Share a blueprint with one or more groups
  user   Share a blueprint with one or more users
```

#### Blueprint Share Group

Share a blueprint with one or more groups

```
Usage:
  ludus blueprint share group
```

#### Blueprint Share User

Share a blueprint with one or more users

```
Usage:
  ludus blueprint share user
```

### Blueprint Unshare

Remove blueprint sharing from groups or users

```
Usage:
  ludus blueprint unshare [command]

Available Commands:
  group  Unshare a blueprint from one or more groups
  user   Unshare a blueprint from one or more users
```

#### Blueprint Unshare Group

Unshare a blueprint from one or more groups

```
Usage:
  ludus blueprint unshare group
```

#### Blueprint Unshare User

Unshare a blueprint from one or more users

```
Usage:
  ludus blueprint unshare user
```

## Diagnostics

Get system diagnostics from the Ludus server

Get system diagnostics from the Ludus server including:
- CPU information (model and cores)
- Storage pool information (size, used, free, percentage)
- Performance metrics from pveperf (CPU, regex, disk, network)

```
Usage:
  ludus diagnostics [flags]
```

## Groups

Perform actions related to groups

```
Usage:
  ludus groups [command]

Available Commands:
  add      Add users or ranges to groups
  create   Create a new group
  delete   Delete a group
  list     List all groups
  members  List group members
  ranges   List group accessible ranges
  remove   Remove users or ranges from groups
```

### Groups Add

Add users or ranges to groups

Add users to groups or grant group access to ranges.

```
Usage:
  ludus groups add [command]

Available Commands:
  range  Grant group access to range(s)
  user   Add user(s) to a group
```

#### Groups Add Range

Grant group access to range(s)

Grant a group access to one or more ranges. For multiple ranges, provide comma-separated rangeIDs (e.g., "range1,range2,range3").

```
Usage:
  ludus groups add range [rangeID(s)] [groupName] [flags]
```

#### Groups Add User

Add user(s) to a group

Add one or more users to a group. For multiple users, provide comma-separated userIDs (e.g., "user1,user2,user3").

```
Usage:
  ludus groups add user [userID(s)] [groupName] [flags]

Flags:
    -m, --manager   whether the user(s) should be manager(s) of the group
```

### Groups Create

Create a new group

Create a new group with the specified name.

```
Usage:
  ludus groups create [name] [flags]

Flags:
        --description string   Description of the group
```

### Groups Delete

Delete a group

Delete a group and clean up all memberships and range access.

```
Usage:
  ludus groups delete [groupName] [flags]
```

### Groups List

List all groups

List all groups in the system.

```
Usage:
  ludus groups list [flags]
```

### Groups Members

List group members

List all users who are members and managers of the specified group.

```
Usage:
  ludus groups members [groupName] [flags]
```

### Groups Ranges

List group accessible ranges

List all ranges that the specified group has access to.

```
Usage:
  ludus groups ranges [groupName] [flags]
```

### Groups Remove

Remove users or ranges from groups

Remove users from groups or revoke group access to ranges.

```
Usage:
  ludus groups remove [command]

Available Commands:
  range  Revoke group access from range(s)
  user   Remove user(s) from a group
```

#### Groups Remove Range

Revoke group access from range(s)

Revoke a group's access to one or more ranges. For multiple ranges, provide comma-separated rangeIDs (e.g., "range1,range2,range3").

```
Usage:
  ludus groups remove range [rangeID(s)] [groupName] [flags]
```

#### Groups Remove User

Remove user(s) from a group

Remove one or more users from a group. For multiple users, provide comma-separated userIDs (e.g., "user1,user2,user3").

```
Usage:
  ludus groups remove user [userID(s)] [groupName] [flags]
```

## Kms

Manage Windows license tasks (enterprise only)

```
Usage:
  ludus kms [command]

Available Commands:
  install  Install a Key Management Service (KMS) server on the Ludus host at 192.0.2.1
  license  License Windows VMs using KMS
```

### Kms Install

Install a Key Management Service (KMS) server on the Ludus host at 192.0.2.1

```
Usage:
  ludus kms install
```

### Kms License

License Windows VMs using KMS

License one or more Windows VMs using the KMS server. Provide VM IDs as a comma-separated list.

```
Usage:
  ludus kms license [flags]

Flags:
    -p, --product-key string   The volume license product key to license the VMs with (default: determine from Windows version)
    -n, --vmids string         A VM ID (104) or multiple VM IDs (104,105) to license
```

## Migrate

Migration commands

Commands for migrating Ludus data and infrastructure

```
Usage:
  ludus migrate [command]

Available Commands:
  sdn     SDN migration commands
  sqlite  Migrate data from SQLite to PocketBase
```

### Migrate Sdn

SDN migration commands

Commands for migrating from bridge-based networking to SDN VNets

```
Usage:
  ludus migrate sdn [command]

Available Commands:
  run     Run SDN migration
  status  Check SDN migration status
```

#### Migrate Sdn Run

Run SDN migration

Migrate Ludus from bridge-based networking to SDN VNets.

This is recommended for:
- Proxmox deployments that have joined a cluster

After migration:
- Range VNets will be created as SDN VNets (r1, r2, etc.)
- The NAT network will use the 'ludus-nat' VNet
- Old vmbr interfaces can be manually removed after verification

In cluster mode, the SDN zone must be pre-configured with correct VXLAN peer IPs.

```
Usage:
  ludus migrate sdn run [flags]

Flags:
        --no-prompt   Skip confirmation prompt
```

#### Migrate Sdn Status

Check SDN migration status

Check the current status of SDN migration.

This command displays:
- Whether SDN zone exists
- Whether NAT VNet exists
- Cluster mode status
- Whether migration is needed
- Current SDN zone name

```
Usage:
  ludus migrate sdn status [flags]
```

### Migrate Sqlite

Migrate data from SQLite to PocketBase

Migrate data from SQLite database to PocketBase database.

This command will migrate all data from the old SQLite database to the new PocketBase database
if the following conditions are met:
1. SQLite database file exists at /opt/ludus/ludus.db
2. PocketBase database only contains the ROOT user

The migration includes:
- Users (excluding ROOT)
- Ranges with default values for new fields (name, description, purpose)
- VMs
- Range access permissions (converted from RangeAccessObject to UserRangeAccess)

After successful migration, the SQLite database will be backed up with a timestamp.

```
Usage:
  ludus migrate sqlite [flags]
```

## Power

Control the power state of range VMs

```
Usage:
  ludus power [command]

Available Commands:
  off  Power off all range VMs
  on   Power on all range VMs
```

### Power Off

Power off all range VMs

```
Usage:
  ludus power off

Flags:
    -n, --name string   A VM ID (100) or name (JS-win10-21h2-enterprise-x64-1) or names/IDs separated by commas or 'all'
```

### Power On

Power on all range VMs

```
Usage:
  ludus power on

Flags:
    -n, --name string   A VM ID (100) or name (JS-win10-21h2-enterprise-x64-1) or names/IDs separated by commas or 'all'
```

## Range

Perform actions on your range

```
Usage:
  ludus range [command]

Available Commands:
  abort          Kill the ansible process deploying a range
  accessible     List all ranges accessible to the current user
  assign         Assign a range to a user (admin only)
  auto-shutdown  Get, set, or reset the auto-shutdown timeout (enterprise)
  config         Get or set a range configuration
  create      Create a new range
  default     Get or set the default range ID for a user
  deploy      Deploy a range, running specific tags if specified
  errors      Parse the latest deploy logs from your range and print any non-ignored fatal errors
  etc-hosts   Get an /etc/hosts formatted file for all hosts in the range
  gettags     Get the ansible tags available for use with deploy
  inventory   Get the ansible inventory file for a range
  list        List details about your range (alias: status)
  logs        Get the latest deploy logs from your range
  rdp         Get a zip of RDP configuration files for all Windows hosts in a range
  revoke      Revoke range access from a user (admin only)
  rm          Destroy all VMs in your range (keeps range)
  rm-range    Delete your range object from database and optionally destroy all VMs
  taskoutput  Get the output of a task by name from the latest deploy logs
  users       List users with access to a range (admin only)
```

### Range Abort

Kill the ansible process deploying a range

```
Usage:
  ludus range abort
```

### Range Accessible

List all ranges accessible to the current user

List all ranges that the current user can access, including direct assignments and group-based access.

```
Usage:
  ludus range accessible [flags]
```

### Range Assign

Assign a range to a user (admin only)

Assign an existing range to a user, granting them direct access. Admin privileges required.

```
Usage:
  ludus range assign [userID] [rangeID] [flags]
```

### Range Auto-Shutdown

Get, set, or reset the auto-shutdown timeout for a range (enterprise)

```
Usage:
  ludus range auto-shutdown [command]

Available Commands:
  get    Get the current auto-shutdown configuration
  reset  Reset the per-range override to the server default
  set    Set a per-range auto-shutdown timeout
```

#### Range Auto-Shutdown Get

Get the current auto-shutdown configuration for a range, showing the server default, per-range override, and effective timeout

```
Usage:
  ludus range auto-shutdown get
```

#### Range Auto-Shutdown Set

Set the per-range auto-shutdown timeout

```
Usage:
  ludus range auto-shutdown set [flags]

Flags:
    -t, --timeout string   inactivity timeout duration (e.g. '4h', '30m', '0' to disable)
```

#### Range Auto-Shutdown Reset

Clear the per-range override so the range falls back to the server default

```
Usage:
  ludus range auto-shutdown reset
```

### Range Config

Get or set a range configuration

```
Usage:
  ludus range config [command]

Available Commands:
  edit  Edit the range configuration in an editor
  get   Get the current Ludus range configuration for a user
  set   Set the configuration for a range
```

#### Range Config Edit

Edit the range configuration in an editor

Edit the range configuration either in a built-in editor or an external editor specified by --editor

```
Usage:
  ludus range config edit [flags]

Flags:
    -e, --editor string           external editor to use (e.g., vim, nano, code)
    -f, --file string             path to a file to read in for editing (default: get config from server)
        --force                   force the configuration to be updated, even with testing enabled
    -t, --temp-file-path string   temporary file path for external editor (default "/tmp/ludus-config.yml")
```

#### Range Config Get

Get the current Ludus range configuration for a user

Provide the 'example' argument to get an example range configuration

```
Usage:
  ludus range config get [example] [flags]
```

#### Range Config Set

Set the configuration for a range

```
Usage:
  ludus range config set

Flags:
    -f, --file string   the range configuration file
        --force         force the configuration to be updated, even with testing enabled
```

### Range Create

Create a new range

Create a new range with a name and pool name. Description, purpose, and userID are optional.

```
Usage:
  ludus range create [flags]

Flags:
    -d, --description string   Description of the range
    -n, --name string          Name of the range
    -o, --purpose string       Purpose of the range
        --range-number int     Specific range number to assign (optional)
        --users string         Comma-separated list of User IDs to assign the range to (optional). By default the current user is assigned. To assign no users to the range, use --users 'none'
```

### Range Default

Get or set the default range ID for a user

```
Usage:
  ludus range default [command]

Available Commands:
  get  Get the default range ID for a user
  set  Set the default range ID for a user
```

#### Range Default Get

Get the default range ID for a user

```
Usage:
  ludus range default get
```

#### Range Default Set

Set the default range ID for a user

```
Usage:
  ludus range default set
```

### Range Deploy

Deploy a range, running specific tags if specified

```
Usage:
  ludus range deploy

Flags:
        --force               force the deployment if testing is enabled (default: false)
    -l, --limit string        limit the deploy to VM that match the specified pattern
        --only-roles string   limit the user defined roles to be run to this comma separated list of roles
    -t, --tags string         the ansible tags to run for this deploy (default: all)
    -v, --verbose-ansible     enable verbose output from ansible during the deploy (default: false)
```

### Range Errors

Parse the latest deploy logs from your range and print any non-ignored fatal errors

```
Usage:
  ludus range errors
```

### Range Etc Hosts

Get an /etc/hosts formatted file for all hosts in the range

```
Usage:
  ludus range etc-hosts
```

### Range Gettags

Get the ansible tags available for use with deploy

```
Usage:
  ludus range gettags
```

### Range Inventory

Get the ansible inventory file for a range

```
Usage:
  ludus range inventory

Flags:
        --all    return inventory for all ranges this user has access to (useful for admin users)
```

### Range List

List details about your range (alias: status)

```
Usage:
  ludus range list
```

### Range Logs

Get the latest deploy logs from your range

```
Usage:
  ludus range logs

Flags:
    -f, --follow        continuously poll the log and print new lines as they are written
        --history       show log history entries
        --id string     view a specific historical log entry by ID
    -t, --tail int      number of lines of the log from the end to print
```

Ludus now creates a running history entry when a deploy starts, then finalizes that entry when the deploy completes. Use `--history` to list entries and `--id` to view a specific one.

`ludus range logs -f` follows the current deployment log only. The `--id` flag cannot be combined with `-f` for range logs.

When a history entry is still running, its `end` field is the Go `time.Time` zero value and the CLI displays `Still running...` in place of an end timestamp.

```
ludus range logs --history
+-----------------+---------+---------------------+---------------------+
|       ID        | STATUS  |        START        |         END         |
+-----------------+---------+---------------------+---------------------+
| njec0ungvnc5ctk | failure | 2026-03-27 22:43:21 | 2026-03-27 22:44:16 |
| ab12c3defg45678 | success | 2026-03-26 14:10:05 | 2026-03-26 14:32:41 |
+-----------------+---------+---------------------+---------------------+
```

```
ludus range logs --history --id njec0ungvnc5ctk
```

By default the server keeps the last 100 logs per range. This can be changed with `max_log_history` in `/opt/ludus/config.yml` (see [Admin Notes](../administration/admin)).

### Range Rdp

Get a zip of RDP configuration files for all Windows hosts in a range

The RDP zip file will contain two configs for each Windows box:
one for the domainadmin user, and another for the domainuser user

```
Usage:
  ludus range rdp [flags]

Flags:
    -o, --output string   the output file path (default "rdp.zip")
```

### Range Revoke

Revoke range access from a user (admin only)

Revoke a user's direct access to a range. Admin privileges required.

```
Usage:
  ludus range revoke [userID] [rangeID] [flags]

Flags:
        --force   force the access action even if the target router is inaccessible
```

### Range RM-Range

Delete your range object from database and optionally destroy all VMs

Delete your range object from the database and destroy all VMs. Use --force to delete all VMs.

```
Usage:
  ludus range rm-range [flags]

Flags:
        --force       force deletion of range even if it has VMs
        --no-prompt   skip the confirmation prompt
```

### Range RM

Destroy all VMs in your range (keeps range)

Destroy all VMs in your range but keep the range object in the database. Use this to start fresh with your range configuration.

```
Usage:
  ludus range rm [flags]

Flags:
        --no-prompt   skip the confirmation prompt
```

### Range Taskoutput

Get the output of a task by name from the latest deploy logs

```
Usage:
  ludus range taskoutput
```

### Range Users

List users with access to a range (admin only)

List all users who have access to a specific range, including direct and group-based access. Admin privileges required.

```
Usage:
  ludus range users [rangeID] [flags]
```

## Snapshots

Manage snapshots for VMs

```
Usage:
  ludus snapshots [command]

Available Commands:
  create  Create a snapshot for VMs
  list    List snapshots for VMs
  revert  revert VM(s) to a snapshot
  rm      rm a snapshot
```

### Snapshots Create

Create a snapshot for VMs

```
Usage:
  ludus snapshots create

Flags:
    -d, --description string   Description of the snapshot
        --noRAM                Don't include RAM in the snapshot
    -n, --vmids string         A VM ID (104) or multiple VM IDs (104,105) to create snapshots for (default: all VMs in the range)
```

### Snapshots List

List snapshots for VMs

```
Usage:
  ludus snapshots list

Flags:
    -n, --vmids string   A VM ID (104) or multiple VM IDs or names (104,105) to list snapshots for (default: all VMs in the range)
```

### Snapshots Revert

revert VM(s) to a snapshot

```
Usage:
  ludus snapshots revert

Flags:
    -n, --vmids string   A VM ID (104) or multiple VM IDs (104,105) to rollback snapshots for (default: all VMs in the range)
```

### Snapshots RM

rm a snapshot

```
Usage:
  ludus snapshots rm

Flags:
    -n, --vmids string   A VM ID (104) or multiple VM IDs (104,105) to remove snapshots from (default: all VMs in the range)
```

## Templates

List, build, add, or get the status of templates

```
Usage:
  ludus templates [command]

Available Commands:
  abort   Kill any running packer processes for the given user (default: calling user)
  add     Add a template directory to ludus
  build   Build templates
  list    List all templates
  logs    Get the latest packer logs
  rm      Remove a template for the given user (default: calling user)
  status  Get templates being built
```

### Templates Abort

Kill any running packer processes for the given user (default: calling user)

Finds any running packer processes with the given user's username and kills them. It uses a SIGINT signal, which should cause packer to clean up the running VMs

```
Usage:
  ludus templates abort [flags]
```

### Templates Add

Add a template directory to ludus

Add a specified directory to ludus as a template. Windows templates should include an Autounattend.xml file in the root of their directory

```
Usage:
  ludus templates add [flags]

Flags:
    -d, --directory string   the path to the local directory of the template to add to ludus
    -f, --force              remove the template directory if it exists on ludus before adding
```

### Templates Build

Build templates

Build a template or all un-built templates

```
Usage:
  ludus templates build [flags]

Flags:
    -n, --names strings   template names to build separated by commas (use 'all' to build all templates)
    -p, --parallel int    build templates in parallel (speeds things up). Specify what number of templates to build at a time (default 1)
```

### Templates List

List all templates

```
Usage:
  ludus templates list
```

### Templates Logs

Get the latest packer logs

```
Usage:
  ludus templates logs

Flags:
    -f, --follow           continuously poll the log and print new lines as they are written
        --history          show log history entries
        --id string        view a specific historical log entry by ID
    -t, --tail int         number of lines of the log from the end to print
    -v, --verbose-packer   print all lines from the packer log
```

Like range logs, template builds create running history entries at build start and finalize them when the build ends. Use `--history` to list entries and `--id` to view one. When building multiple templates in parallel, each template build creates its own history entry.

`ludus templates logs -f` follows the latest active template build at the time of the request.

To follow a specific running template build, use:

```
ludus templates logs --id <logID> -f
```

If that build has already completed, the command falls back to showing the archived history content.

### Templates RM

Remove a template for the given user (default: calling user)

Removes any built VM template for the given name as well as the template directory. Will not remove built-in template directories that ship with Ludus.

```
Usage:
  ludus templates rm [flags]

Flags:
    -n, --name string   the name of the template to remove
```

### Templates Status

Get templates being built

Show the templates currently being built by packer

```
Usage:
  ludus templates status [flags]
```

## Testing

Control the testing state of the range

```
Usage:
  ludus testing [command]

Available Commands:
  allow   allow a domain, IP, or file containing domains and IPs during testing
  deny    deny a previously allowed domain, IP, or file containing domains and IPs during testing
  start   Snapshot all testing VMs and block all outbound connections and DNS from testing VMs
  status  Get the current testing status as well as any allowed domains and IPs (alias: list)
  stop    Revert all testing VMs and enable all outbound connections and DNS from testing VMs
  update  Perform a Windows update on a VM or group of VMs
```

### Testing Allow

allow a domain, IP, or file containing domains and IPs during testing

If providing a file, domains and IPs will be extracted with
regex that require them to start at the beginning of a line.

```
Usage:
  ludus testing allow [flags]

Flags:
    -d, --domain string   A domain or comma separated list of domains (and HTTPS certificate CRL domains) to allow. Resolved on Ludus.
    -f, --file string     A file containing domains and/or IPs to allow
    -i, --ip string       An IP or comma separated list of IPs to allow
```

### Testing Deny

deny a previously allowed domain, IP, or file containing domains and IPs during testing

If providing a file, domains and IPs will be extracted with
regex that require them to start at the beginning of a line.

```
Usage:
  ludus testing deny [flags]

Flags:
    -d, --domain string   A domain or comma separated list of domains to deny.
    -f, --file string     A file containing domains and/or IPs to deny
    -i, --ip string       An IP or comma separated list of IPs to deny
```

### Testing Start

Snapshot all testing VMs and block all outbound connections and DNS from testing VMs

```
Usage:
  ludus testing start
```

### Testing Status

Get the current testing status as well as any allowed domains and IPs (alias: list)

```
Usage:
  ludus testing status
```

### Testing Stop

Revert all testing VMs and enable all outbound connections and DNS from testing VMs

```
Usage:
  ludus testing stop

Flags:
        --force   force ludus to exit testing mode even if one or more snapshot reverts fails
```

### Testing Update

Perform a Windows update on a VM or group of VMs

```
Usage:
  ludus testing update

Flags:
    -n, --name string   A VM name (JD-win10-21h2-enterprise-x64-1) or group name (JD_windows_endpoints) to update with Windows Update
```

## Update

Update the Ludus client or server

```
Usage:
  ludus update [command]

Available Commands:
  client  Update this Ludus client
```

### Update Client

Update this Ludus client

This command checks for an update to this Ludus client
and installs it if available.

```
Usage:
  ludus update client [flags]
```

## Users

Perform actions related to users

```
Usage:
  ludus users [command]

Available Commands:
  add        Add a user to Ludus
  apikey     Get a new Ludus apikey for a user
  creds      Perform actions related to Proxmox credentials
  list       List information about a user (alias: status)
  rm         Remove a user from Ludus
  wireguard  Get the Ludus wireguard configuration for a user
```

### Users Add

Add a user to Ludus

```
Usage:
  ludus users add

Flags:
    -a, --admin             set this flag to make the user an admin of Ludus
    -e, --email string      the email for the user
    -n, --name string       the name of the user (typically 'first last')
    -p, --password string   the password for the user (must be at least 8 characters long, omit to prompt and generate a random password)
    -i, --userid string     the UserID of the new user (2-20 chars, typically capitalized initials)
```

### Users Apikey

Get a new Ludus apikey for a user

```
Usage:
  ludus users apikey

Flags:
        --no-prompt   skip the confirmation prompt
        --value       output only the API key value without table formatting
```

### Users Creds

Perform actions related to Proxmox credentials

```
Usage:
  ludus users creds [command]

Available Commands:
  get  Get Proxmox credentials for a user
  set  Set the proxmox password for a Ludus user
```

#### Users Creds Get

Get Proxmox credentials for a user

```
Usage:
  ludus users creds get
```

#### Users Creds Set

Set the proxmox password for a Ludus user

```
Usage:
  ludus users creds set

Flags:
    -p, --password string   the proxmox password of the user
    -i, --userid string     the UserID of the user (default: the user ID of the API key)
```

### Users List

List information about a user (alias: status)

Optionally supply the value "all" to retrieve
	information about all users.

```
Usage:
  ludus users list [all] [flags]
```

### Users RM

Remove a user from Ludus

```
Usage:
  ludus users rm

Flags:
        --delete-range    also delete the user's default range and any VMs it contains
    -i, --userid string   the UserID of the user to remove
```

### Users Wireguard

Get the Ludus wireguard configuration for a user

```
Usage:
  ludus users wireguard
```

## Version

Prints the version of this ludus binary

Prints the version of this ludus binary.
The version includes the SemVer version and the first
8 character of the git commit hash of the commit
this binary was built from.

```
Usage:
  ludus version [flags]
```

## VM

Perform actions on VMs

```
Usage:
  ludus vm [command]

Available Commands:
  destroy  Destroy a VM
```

### VM Destroy

Destroy a VM

Destroy a VM by its Proxmox ID. The VM will be stopped if running and then permanently deleted.

```
Usage:
  ludus vm destroy [flags]

Flags:
        --no-prompt   skip the confirmation prompt
```
