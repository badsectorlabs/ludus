---
sidebar_position: 11
---

# CI/CD

## Requirements

:::warning

This will nest a full Ludus install for every pipeline in an existing Ludus server. 
Going more than 1 layer deep of nested virtualization is not supported.

:::

To set up a CI/CD runner for Ludus development you must meet the following requirements:

1. A functional, fast, Ludus server with at least 32GB of free RAM, 250GB of free disk space, and 8 cores available (can over-provision cores if necessary)
2. The `debian-12-x64-server-template` must be built
3. Root access to the Ludus server
4. A Gitlab account with the ability to create a runner token (gitlab.com or self-hosted)
5. Network access from the Ludus server to the Gitlab instance/gitlab.com

## Setup

To setup the CI/CD runner and template follow these steps:

1. Create a Gitlab runner with the tag `ludus-proxmox-runner`. Do not check `Run untagged jobs`.

2. Copy the Gitlab runner token

3. Review the settings in `/opt/ludus/ci/setup.sh` to ensure they match your environment (i.e. `PROXMOX_VM_STORAGE_POOL`)

4. Run `/opt/ludus/ci/setup.sh` with appropriate env variables as root on the Ludus server:

```
PROXMOX_USERNAME=root@pam PROXMOX_PASSWORD=password /opt/ludus/ci/setup.sh
```

5. When the playbook finishes running, you will see a `debian-12-x64-server-ludus-ci-template` template in the Proxmox web UI and `ludus templates list` (admins only).

6. Review the settings in `/opt/ludus/ci/base.sh`, specifically the `PROXMOX_NODE` setting and modify it as necessary.

Now that CI is setup and configured, any commits that are pushed to the Ludus project will build and test as appropriate.

## Tags

The CI system is set up to run the appropriate tests depending on what part of the code base has been modified.
However, sometimes you want to override the defaults.
To manually control the CI pipeline, you can add "tags" to the final commit message before a push.
To use these, simply include one or more of the "tag" strings in your commit message, including the brackets.

The available tags are listed below:

- `[skip ci]` - this tag skips all CI jobs
- `[skip build]` - skips the documentation build and the binary build stages
- `[build docs]` - force a documentation build
- `[full build]` - run every step of the CI pipeline, no matter how small the change to the code base
- `[manual]` - only run the documentation build (if docs have changed) and binary build, then push the code to an already running CI VM (typically used with the `[VMID-XYZ]` tag)
- `[VMID-XYZ]` - run jobs on the specified VM where `XYZ` is the numeric VMID of the CI/CD VM.
- `[client tests]` - test basic client commands that do not deploy templates or ranges
- `[template tests]` - run a template build and wait for all templates to complete building
- `[range tests]` - run a range deploy and wait for it to succeed. This uses the default range config.
- `[post-deploy tests]` - run tests related to testing mode, allowing and denying domains and IPs, as well as powering on and off a VM

## Releases

Any time a version tag is created in Gitlab, two additional CI jobs are added to the pipeline: `upload` and `release`.
These jobs are manually triggered (you must click the play button in the pipeline) and upload the compiled binaries to the package registry as well as create the actual release. If you use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) (perhaps created with [koji](https://github.com/its-danny/koji)), then [git-cliff](https://github.com/orhun/git-cliff) will automatically generate a change log for the release.