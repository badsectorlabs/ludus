#!/usr/bin/env bash

# Configure an already-registered GitLab Runner to use the Ludus custom
# executor scripts. This preserves the runner token and registration metadata.

set -euo pipefail

CONFIG_FILE="${GITLAB_RUNNER_CONFIG:-/etc/gitlab-runner/config.toml}"
LUDUS_DIR="${LUDUS_DIR:-/opt/ludus}"
CONCURRENT="${GITLAB_RUNNER_CONCURRENT:-12}"

if [[ $(id -u) -ne 0 ]]; then
    echo "Error: this script must run as root" >&2
    exit 1
fi

if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Error: GitLab Runner config not found at $CONFIG_FILE" >&2
    exit 1
fi

install -d -o gitlab-runner -g gitlab-runner /home/gitlab-runner/builds /home/gitlab-runner/cache
chmod +x "$LUDUS_DIR"/ci/*.sh

python3 - "$CONFIG_FILE" "$CONCURRENT" <<'PY'
from __future__ import annotations

import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
concurrent = sys.argv[2]
lines = path.read_text().splitlines()

custom_block = [
    "  [runners.custom]",
    '    prepare_exec = "/opt/ludus/ci/prepare.sh"',
    '    run_exec = "/opt/ludus/ci/run.sh"',
    '    cleanup_exec = "/opt/ludus/ci/cleanup.sh"',
]

out: list[str] = []
in_runner = False
in_custom = False
custom_written = False
builds_seen = False
cache_seen = False
executor_seen = False
runner_seen = False


def write_missing_runner_keys() -> None:
    global builds_seen, cache_seen, executor_seen
    if not executor_seen:
        out.append('  executor = "custom"')
        executor_seen = True
    if not builds_seen:
        out.append('  builds_dir = "/home/gitlab-runner/builds"')
        builds_seen = True
    if not cache_seen:
        out.append('  cache_dir = "/home/gitlab-runner/cache"')
        cache_seen = True


def write_custom_block() -> None:
    global custom_written
    if not custom_written:
        out.extend(custom_block)
        custom_written = True


for line in lines:
    if re.match(r"^\s*concurrent\s*=", line):
        out.append(f"concurrent = {concurrent}")
        continue

    if re.match(r"^\s*\[\[runners\]\]\s*$", line):
        if in_runner:
            write_missing_runner_keys()
            write_custom_block()
        in_runner = True
        in_custom = False
        custom_written = False
        builds_seen = False
        cache_seen = False
        executor_seen = False
        runner_seen = True
        out.append(line)
        continue

    if in_runner:
        if re.match(r"^\s*\[runners\.custom\]\s*$", line):
            write_missing_runner_keys()
            write_custom_block()
            in_custom = True
            continue

        if in_custom:
            if re.match(r"^\s*\[", line):
                in_custom = False
            else:
                continue

        if re.match(r"^\s*\[", line):
            write_missing_runner_keys()
            if not re.match(r"^\s*\[runners\.", line):
                write_custom_block()
                in_runner = False

        if in_runner and re.match(r"^\s*executor\s*=", line):
            out.append('  executor = "custom"')
            executor_seen = True
            continue

        if in_runner and re.match(r"^\s*builds_dir\s*=", line):
            out.append('  builds_dir = "/home/gitlab-runner/builds"')
            builds_seen = True
            continue

        if in_runner and re.match(r"^\s*cache_dir\s*=", line):
            out.append('  cache_dir = "/home/gitlab-runner/cache"')
            cache_seen = True
            continue

    out.append(line)

if in_runner:
    write_missing_runner_keys()
    write_custom_block()

if not runner_seen:
    raise SystemExit("No [[runners]] section found in GitLab Runner config")

path.write_text("\n".join(out) + "\n")
PY

gitlab-runner restart
gitlab-runner verify
