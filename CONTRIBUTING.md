# Contributing Guide #

Thank you for your interest in contributing to Ludus. This is a
generic guide that details how to contribute to Ludus in a way that
is efficient for everyone. If you want a specific documentation for
different parts of the platform, please refer to `docs/` directory.


## Reporting Bugs ##

We are using [GitLab Issues](https://gitlab.com/badsectorlabs/ludus/-/issues)
for our public bugs. We keep a close eye on this and try to make it
clear when we have an internal fix in progress. Before filing a new
task, try to make sure your problem doesn't already exist.

If you found a bug, please report it, if possible with:

- a detailed explanation of steps to reproduce the error
- verbose output from the Ludus CLI

If you found a bug that you consider better discuss in private (for
example: security bugs), please submit a [confidential issue](https://docs.gitlab.com/ee/user/project/issues/confidential_issues.html).

**We don't have formal bug bounty program for security reports; this
is an open source application and your contribution will be recognized
in the changelog.**


## Pull requests ##

If you want propose a change or bug fix with the Pull-Request system
firstly you should carefully read the **DCO** section and format your
commits accordingly.

If you intend to fix a bug it's fine to submit a pull request right
away but we still recommend to file an issue detailing what you're
fixing. This is helpful in case we don't accept that specific fix but
want to keep track of the issue.

If you want to implement or start working in a new feature, please
open a **question** / **discussion** issue for it. No pull-request
will be accepted without previous chat about the changes,
independently if it is a new feature, already planned feature or small
quick win.

If possible, please test all changes locally in your own CI environment.
Instructions on how to set up CI can be found [here](https://docs.ludus.cloud/docs/cicd).

## Commit Guidelines ##

We have very precise rules over how our git commit messages can be formatted.

The commit message format is:

```
<type> <subject>

[body]

[footer]
```

Where type is:

- fix: 🐛 a commit that fixes a bug
- feat: ✨ a commit with new feature
- refactor: 🔨 a commit that introduces a refactor
- style: 💄 a commit with cosmetic changes
- docs: 📚 a commit that improves or adds documentation
- wip: 🚧: a wip commit
- perf: ⚡ a commit with performance improvements
- revert: ⏪ a commit that reverts changes
- test: 🚨 a commit that adds missing tests or corrects existing tests
- chore: 🧹 a commit with other changes that don't modify src or test files
- build: 📦 a commit with changes that affect the build system or external dependencies
- ci: 🤖 a commit that changes our CI configuration files and scripts

We encourage you to use a tool like [koji](https://github.com/its-danny/koji) to enforce these standards.
The koji config below can be used:

```
emoji = true
breaking_changes = true
issues = true

[[commit_types]]
name = "feat"
emoji = "✨"
description = "A new feature"

[[commit_types]]
name = "fix"
emoji = "🐛"
description = "A bug fix"

[[commit_types]]
name = "docs"
emoji = "📚"
description = "Documentation only changes"

[[commit_types]]
name = "style"
emoji = "💄"
description = "Changes that do not affect the meaning of the code"

[[commit_types]]
name = "refactor"
emoji = "🔨"
description = "A code change that neither fixes a bug nor adds a feature"

[[commit_types]]
name = "perf"
emoji = "⚡"
description = "A code change that improves performance"

[[commit_types]]
name = "test"
emoji = "🚨"
description = "Adding missing tests or correcting existing tests"

[[commit_types]]
name = "build"
emoji = "📦"
description = "Changes that affect the build system or external dependencies"

[[commit_types]]
name = "ci"
emoji = "🤖"
description = "Changes to our CI configuration files and scripts"

[[commit_types]]
name = "chore"
emoji = "🧹"
description = "Other changes that don't modify src or test files"

[[commit_types]]
name = "revert"
emoji = "⏪"
description = "Reverts a previous commit"

[[commit_types]]
name = "wip"
emoji = "🚧"
description = "A work in progress commit"
```

More info:
 - https://www.conventionalcommits.org/en/v1.0.0/#summary
 - https://github.com/its-danny/koji

Each commit should have:

- A concise subject using imperative mood.
- The subject should have capitalized the first letter, without period
  at the end and no larger than 65 characters.
- A blank line between the subject line and the body.

Examples of good commit messages:

- `fix: 🐛 fix the case where parallel > number of templates and 'template build' is called more than once before templates are done building`
- `refactor: 🔨 clean up parallel builds`
- `fix(config): 🐛 force the user to define all defaults if any defaults are defined as it completely replaces the server's default dict object`
- `docs: 📚 center image in readme`
- `ci: 🤖 fix baseUrl setting for gitlab pages deployment`

## Developer's Certificate of Origin (DCO) ##

By submitting code you agree to and can certify the below:

```
    Developer's Certificate of Origin 1.1

    By making a contribution to this project, I certify that:

    (a) The contribution was created in whole or in part by me and I
        have the right to submit it under the open source license
        indicated in the file; or

    (b) The contribution is based upon previous work that, to the best
        of my knowledge, is covered under an appropriate open source
        license and I have the right under that license to submit that
        work with modifications, whether created in whole or in part
        by me, under the same open source license (unless I am
        permitted to submit under a different license), as indicated
        in the file; or

    (c) The contribution was provided directly to me by some other
        person who certified (a), (b) or (c) and I have not modified
        it.

    (d) I understand and agree that this project and the contribution
        are public and that a record of the contribution (including all
        personal information I submit with it, including my sign-off) is
        maintained indefinitely and may be redistributed consistent with
        this project or the open source license(s) involved.
```

All your code patches should
contain a sign-off at the end of the patch/commit description body. It
can be automatically added on adding `-s` parameter to `git commit`.

This is an example:

```
    Signed-off-by: Erik [Bad Sector Labs] <555113-badsectorlabs@users.noreply.gitlab.com>
```

To do this in combination with koji, this alias may be helpful:

```
alias ko="koji -c ~/.config/koji/config.toml && git commit --amend --no-edit -s"
```