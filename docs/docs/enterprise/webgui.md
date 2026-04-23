---
sidebar_position: 7
title: "Frontend WebGUI"
---

# Frontend WebUI

:::note[🏛️ `Only available to Ludus Pro and Ludus Enterprise users`]
:::

## What is the Ludus frontend WebUI ?

The purpose of the frontend WebUI is to provide users to manage their own cyberrange. This provides users an easy to use environment without having to give the user direct access to the machine or the need to install the Ludus CLI client.
Functionality is the same as the Ludus CLI, users can manage their Ranges, Blueprints, Ansible Roles (If enabled in /opt/ludus/config.yml: `prevent_user_ansible_add: true` ) and Templates.


## Overview of the WebUI ?

When the user is added by an admin and received his account details, the user can visit the WebUI which can be found at https://<ludus-host>:8080/ui. 
Which then will show the following screen:

![A screenshot showing the login page](/img/enterprise/WebUI-login.jpg)

After logging in with the details you will land on the Homepage, which has various options to manage your Cyberrange.

### Ranges

![A screenshot showing the default homepage](/img/enterprise/WebUI-homepage.jpg)

The page starts default on your range overview, with the range represented as a tile. When you press the tile the Range window will open, where you can visually build your range from the available templates

![A screenshot showing the Range Page](/img/enterprise/WebUI-Range.jpg)

Pressing the top left arrow will bring you back to the home page.

More information about range options can also be found [here](../configuration) or take a look over at the many community provided [Environment Guides](../category/environment-guides)

### Blueprints

![A screenshot showing the Blueprints Page](/img/enterprise/WebUI-BluePrints.jpg)

Here, you can see which Blueprints there are made available to you and you cna also create your own new blueprints based of a deployed range by pressing the `+ Blueprint` which will open a new window.

![A screenshot showing the Blueprints Create Box](/img/enterprise/WebUI-BluePrints2.jpg)

More information about Blueprints can be found [here](../using-ludus/blueprints)

### Ansible 

![A screenshot showing the Ansible (Roles) Overview](/img/enterprise/WebUI-Ansible.jpg)

The Ansible page shows which roles have been made available for you to use for the creation of your ranges. If enabled this is also the page where you can add ansible roles to your user by pressing the `+ Roles`

![A screenshot showing the Ansible (Roles) add Box](/img/enterprise/WebUI-Ansible2.jpg)

The roles can uploaded from a local folder, downloaded from the Ansible Galaxy repository or selected from the Private Roles which are made available for the Pro/Enterprise Users.

More Information about Roles can be found [here](../using-ludus/roles)

### Templates

![A screenshot showing the Templates Overview](/img/enterprise/WebUI-Templates.jpg)

The Templates page shows which templates are currently built and that you can use for your ranges. When adding a new template the template build logs can be monitored by pressing the `>_ Logs` button, right next to it you'll find the `+ Templates` which allows you to upload new templates.

![A screenshot showing the Templates Add Box](/img/enterprise/WebUI-Templates2.jpg)

More information about Templates can be found [here](../using-ludus/templates)