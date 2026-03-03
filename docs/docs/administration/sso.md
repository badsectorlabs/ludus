---
sidebar_position: 3
title: "🆔 SSO"
---

# 🆔 SSO

## Configure an OAuth2 Provider

The Ludus Web UI supports SSO, powered by PocketBase.

To configure SSO, you must first [enable the PocketBase UI](./pocketbase).

Click the gear icon next to the `users` table header

![users settings](/img/sso/users-settings.png)

Click options, expand the OAuth2 field, toggle the `Enable` toggle, and click `+ Add provider`

![steps to add a provider](/img/sso/add-provider.png)

Click on your provider, and configure the values as required. Each provider will have their own setup steps.

:::tip

Some OAuth2 providers require a domain to use them as a provider. You may need to set up DNS properly to use OAuth2

:::

Once you have your provider configured, click `Set provider config` and double check that OAuth2 is `Enabled` and click `Save changes`.

![saving the provider](/img/sso/google-configured.png)

Now users will be presented with a `Login with...` button on the login page for Ludus.

:::warning

Any user that can authenticate to your OAuth2 provider can authenticate to Ludus. On first login to Ludus a default range, PAM user, and proxmox token will be generated for the user.

:::

Users that log in via SSO are standard users with no access beyond their default range. Admins should add them to groups, share ranges and blueprints with them, or otherwise grant them access to the resources they need.

You can disable the pocketbase web interface by running the following commands

```shell-session
#terminal-command-ludus-root
unset-environment LUDUS_ENABLE_SUPERADMIN
#terminal-command-ludus-root
systemctl restart ludus
```

