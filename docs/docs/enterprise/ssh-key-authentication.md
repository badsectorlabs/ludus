---
sidebar_position: 7
title: "SSH Key Authentication"
---

# SSH key authentication and provisioning accounts

SSH key authentication requires the Enterprise plugin. The existing `defaults.use_cert_auth` setting selects ordinary SSH public/private keys on Linux, macOS, and Windows—not WinRM client certificates or CA-signed OpenSSH certificates. Add this to an existing range configuration, then run a full `ludus range deploy`:

```yaml
defaults:
  use_cert_auth: true
```

Omitted authentication settings inherit the server defaults, even when the range defines a partial `defaults` object. An explicit `use_cert_auth: false` overrides a server-level `true` and retains ordinary Windows WinRM password transport.

By default, Ludus keeps each template's provisioning account, installs the range's SSH public key, and replaces the template password after verifying a fresh administrative key login. Non-root Linux and macOS accounts receive passwordless sudo. Windows initially uses password-based HTTPS WinRM to install and configure native OpenSSH, then switches to PowerShell over SSH on port 22. Later deployments and testing-mode operations use the SSH key, not the provisioning password.

To provision directly as `root` on Linux and macOS and `Administrator` on Windows:

```yaml
defaults:
  use_cert_auth: true
  cert_auth_linux_user: root
  cert_auth_macos_user: root
  cert_auth_windows_user: Administrator
  cert_auth_create_accounts: true
```

`cert_auth_linux_user` selects the Linux SSH provisioning account, including the Linux router. `cert_auth_macos_user` selects the macOS SSH account, and `cert_auth_windows_user` selects the Windows account. These settings are independent: macOS never falls back to `cert_auth_linux_user`. When neither the range nor server defaults specify an OS's username, Ludus retains its template account (`localuser` on Windows and macOS). Windows usernames must be unqualified SAM account names: local accounts on standalone/member hosts and domain accounts on domain controllers.

`cert_auth_create_accounts` defaults to `false`. Without it, a selected custom account must already exist, be enabled, and be usable for login. With it, Ludus can create a missing account or enable a disabled one, including `Administrator`. Selected accounts receive administrative provisioning privileges.

When a custom account replaces the template account, Ludus verifies a fresh SSH key login before removing the old account and its home/profile. Windows keeps the template key authorized during staging and removes that authorization only after verified retirement. Windows may reboot to end old sessions before deleting a loaded profile. UID-zero Linux and macOS accounts and the Windows RID-500 administrator are never deleted.

Keep Windows provisioning and template account names separate from `defaults.ad_domain_admin` and `defaults.ad_domain_user`. Domain roles still need those accounts and their configured passwords. Windows refuses to retire identities used by services; migrate those services explicitly before changing their accounts.

Before retiring shared domain credentials, every managed Windows VM must pass SSH bootstrap. Run the initial bootstrap without `--limit`; already-finalized ranges can use limited key-only redeployments. A replicated `[LudusSSH:<key sha256>]` suffix in the provisioning account's Description records password retirement across DCs. Preserve that suffix. Domain promotion and additional-DC provisioning refresh and verify the account's SSH identity after reboot. If promotion re-enables the retired RID-500 administrator, Ludus disables it again after verifying the selected key identity, without rotating either account's domain password.

:::caution

Template passwords will no longer work for SSH, WinRM, console login, or RDP. Windows template autologon is disabled, and template-account RDP files are no longer generated. Clearing `use_cert_auth` does **not** restore deleted accounts or passwords.

Preserve `/opt/ludus/ranges/<rangeID>/machine-credentials` and its sibling `.machine-credentials-initialized` marker in your server backups. Ludus does not silently regenerate missing or corrupt SSH keys for provisioned VMs.

:::

## Downloading a range's SSH keys

An authorized range user can download the existing credentials with the CLI (Enterprise):

```bash
ludus range machine-credentials -r MYRANGE -o machine-credentials.zip
```

Omit `--range/-r` to use your default range. The default output filename is `machine-credentials.zip`; use `--output/-o` to change it. On Unix-like systems, the CLI saves the archive with owner-only read/write permissions (`0600`).

To download directly through the API:

```bash
umask 077
curl --fail --cacert /path/to/ludus-server-ca.pem \
  -H "X-API-KEY: $LUDUS_API_KEY" \
  "$LUDUS_URL/api/v2/range/machine-credentials?rangeID=MYRANGE" \
  -o machine-credentials.zip
```

Use your server's trusted CA, or omit `--cacert` if its certificate is already trusted by your system. URL-encode the range ID when necessary. The archive contains `README.txt`, `ssh/ludus_ed25519`, and `ssh/ludus_ed25519.pub` only. It does not create or rotate credentials. Treat the archive as administrative access to that range.

## Compatibility

Windows requires 64-bit Windows PowerShell 5.1 or later, native OpenSSH for Windows 7.9 or later, the NetSecurity and ScheduledTasks modules, port 22, and the standard `ProgramData\ssh\sshd_config` service configuration. Ludus configures PowerShell as OpenSSH's default shell. A missing OpenSSH server is installed through the Windows capability mechanism, which requires access to Windows Update or a configured Features on Demand source. A template's disabled Windows Update service is temporarily enabled for installation, then restored. Older inbox OpenSSH 7.7 builds are not accepted; preinstall a compatible version in those templates.

The installer/update provisions an isolated Ansible core 2.18 runtime for ranges with `defaults.use_cert_auth: true` and managed Windows hosts. Its controller requires Python 3.11–3.13; Linux and macOS guests in those mixed ranges require Python 3.8 or later. Password-mode and Linux/macOS-only ranges retain the existing Ansible 2.16 runtime, including its legacy Python 2 guest support. An explicit `LUDUS_ANSIBLE_BINARY` override takes precedence and must provide a Windows-SSH-compatible runtime.

Failure to provision this optional runtime produces a warning and does not stop installation or update. Ranges that opt into Windows SSH still require a working runtime; fix the reported dependency error and rerun `ludus-server --update` before deploying them.

Windows public-key sessions use S4U tokens without reusable outbound credentials. SSH account/server staging and core DC and GPO operations run locally as SYSTEM. Fresh key-login and administrative-token checks run as the selected provisioning account, not SYSTEM. Custom roles that access remote AD services or network resources may need explicit module credentials or `runas` credentials; an SSH key is not a delegated domain password. See [Ansible's Windows SSH guidance](https://docs.ansible.com/ansible/latest/os_guide/windows_ssh.html) and [Microsoft's LocalSystem account guidance](https://learn.microsoft.com/en-us/windows/win32/ad/the-localsystem-account).

Bootstrap suppresses SYSTEM elevation passwords only within its own tasks. Domain-join tasks and custom roles can supply `ansible_become_password` or `ansible_runas_pass` without re-enabling SSH password authentication.

macOS templates must have Remote Login enabled and Python 3 available.

On macOS, credential retirement refuses to change a SecureToken account while FileVault is enabled, and stops if FileVault is still encrypting/decrypting or its state cannot be determined. Use a fully decrypted disposable template or provisioning accounts that are not needed for disk unlock; preserve a separately verified unlock/recovery method. Automatic retirement of a replaced macOS template account also requires its standard `/Users/<username>` home directory.

Request Windows sysprep before the first SSH-key deployment. Adding sysprep to an already key-provisioned VM requires recreating that VM because generalization changes its local identity.

Existing WinRM client-certificate deployments are not automatically converted. Use the previous management access to enroll the existing range SSH key first, or recreate the VMs from templates. Old WinRM credential files are neither consumed nor exported. Once a replacement provisioning account has removed the template account, changing to another username likewise requires an explicit migration or VM recreation.



