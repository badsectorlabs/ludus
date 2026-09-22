#!/usr/bin/env bash
# DAB runs as root. Never put its rootfs/cache/dependencies in the CI checkout.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd -P)
: "${LUDUS_VERSION:?LUDUS_VERSION must be set}"
[[ $LUDUS_VERSION =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]] || {
  echo 'Invalid LUDUS_VERSION' >&2; exit 1;
}
owner=$(stat -c '%u:%g' "$repo")
work=$(mktemp -d /var/tmp/ludus-lxc-build.XXXXXXXX)

cleanup() {
  # DAB can leave bind mounts after a failed build. Unmount via DAB first;
  # never recursively remove a workspace with mounts still beneath it.
  if [[ -d $work/ludus-server/lxc/rootfs ]]; then
    (cd "$work/ludus-server/lxc" && dab clean) || echo 'DAB cleanup failed; checking mounts' >&2
  fi
  local mounts mount
  if ! mounts=$(findmnt -rn -o TARGET); then
    echo "Cannot check mounts; retaining $work" >&2
    return
  fi
  while IFS= read -r mount; do
    case "$mount" in
      "$work"|"$work"/*) echo "Mount remains; retaining $work" >&2; return ;;
    esac
  done <<< "$mounts"
  rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$work/ludus-server/lxc" "$work/binaries"
for input in Makefile dab.conf build.sh migrate-host.sh python-requirements.txt files; do
  cp -a "$repo/ludus-server/lxc/$input" "$work/ludus-server/lxc/"
done
cp -a "$repo/ludus-server/ansible" "$repo/ludus-server/packer" "$work/ludus-server/"
cp "$repo/binaries/ludus-server" "$work/binaries/"

# Keep even an inherited cache override out of the checkout.
DAB_CACHE_DIR="$work/ludus-server/cache" bash "$work/ludus-server/lxc/build.sh"
artifact="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
for file in "$artifact" "$artifact.sha256"; do
  # install replaces the destination rather than following an old symlink.
  install -m 0644 -o "${owner%:*}" -g "${owner#*:}" "$work/$file" "$repo/$file"
done
echo "Published runner-owned artifact: $artifact"
