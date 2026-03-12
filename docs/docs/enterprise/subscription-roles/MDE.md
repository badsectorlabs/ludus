# Microsoft Defender for Endpoint (MDE)

Installs [Microsoft Defender for Endpoint](https://learn.microsoft.com/en-us/defender-endpoint/microsoft-defender-endpoint) (formally Advanced Threat Protection - ATP) on Windows hosts (10/11 and 2016, 2019, 2022)

:::warning
    You must add your own `WindowsDefenderATPLocalOnboardingScript.cmd` file to the `files` directory of this role on the Ludus host!
:::

# Using this role - Onboarding

1. Go to [Onboarding settings](https://security.microsoft.com/securitysettings/endpoints/onboarding)

2. Download the onboarding package (Windows 10/11 is the same as Windows server 2019/2022). For Windows server 2016 you must download the `md4ws.msi` file using the same method and move it to the files directory of this role.

![How to download onboarding package](/img/roles/MDE/onboarding_download.png)

3. Unzip `GatewayWindowsDefenderATPOnboardingPackage.zip` and move the `WindowsDefenderATPLocalOnboardingScript.cmd` to the files directory of this role

`/opt/ludus/users/<username>/.ansible/roles/ludus_MDE/files`

or if installed globally at

`/opt/ludus/resources/global-roles/ludus_MDE/files`


# Using this role - Offboarding

1. Go to [Offboarding settings](https://security.microsoft.com/securitysettings/endpoints/offboarding)

2. Download the onboarding package (Windows 10/11 is the same as Windows server 2019/2022)

![How to download onboarding package](/img/roles/MDE/offboarding_download.png)

3. Unzip `WindowsDefenderATPOffboardingPackage_valid_until_*.zip` and move the `WindowsDefenderATPOffboardingScript_valid_until_*.cmd` to the files directory of this role

`/opt/ludus/users/<username>/.ansible/roles/ludus_MDE/files`

or if installed globally at

`/opt/ludus/resources/global-roles/ludus_MDE/files`

4. Add the role to your ludus configuration (see example-config.yml) with the role_var `ludus_MDE_action: offboard` and update the config with `ludus range config set -f config.yml`

5. Deploy the range with `ludus range deploy -t user-defined-roles`


## Role Variables

Available variables are listed below, along with default values:

```yaml
# Specify the action for the role to take (default: onboard, options: [onboard, offboard])
ludus_MDE_action: onboard
# Specify a tag to apply to the machine using the registry (default: none)
# Note: Only one tag can be applied to a machine using this method and it must be < 200 characters
ludus_MDE_tag:
```


## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64-1"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 6
    cpus: 4
    windows:
      sysprep: true
    domain:
      fqdn: ludus.domain
      role: primary-dc
    roles:
      - ludus_MDE
    role_vars:
      ludus_MDE_action: onboard
      ludus_MDE_tag: Ludus
```