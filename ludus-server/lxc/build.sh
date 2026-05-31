#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

: "${LUDUS_VERSION:?LUDUS_VERSION must be set}"
: "${PACKER_VERSION:=1.11.2}"
: "${PACKER_PROXMOX_VERSION:=1.2.1}"
: "${PACKER_ANSIBLE_VERSION:=1.1.1}"

mkdir -p deps
if [[ ! -f deps/packer ]]; then
  curl -fsSL "https://releases.hashicorp.com/packer/${PACKER_VERSION}/packer_${PACKER_VERSION}_linux_amd64.zip" -o /tmp/packer.zip
  unzip -o /tmp/packer.zip -d deps/
fi
for plugin in proxmox:${PACKER_PROXMOX_VERSION} ansible:${PACKER_ANSIBLE_VERSION}; do
  name=${plugin%%:*}; ver=${plugin##*:}
  if [[ ! -f deps/packer-plugin-${name} ]]; then
    curl -fsSL "https://github.com/hashicorp/packer-plugin-${name}/releases/download/v${ver}/packer-plugin-${name}_v${ver}_x5.0_linux_amd64.zip" -o /tmp/pp.zip
    unzip -o /tmp/pp.zip -d deps/
    mv deps/packer-plugin-${name}_* deps/packer-plugin-${name}
  fi
done

if [[ ! -f ../../binaries/ludus-server ]]; then
  echo "ERROR: binaries/ludus-server not found (run 'build all' first)" >&2
  exit 1
fi

# Substitute version into dab.conf before make parses BASEDIR.
# Work on a fresh copy from git so re-runs are idempotent.
git checkout -- dab.conf 2>/dev/null || true
sed -i "s/__LUDUS_VERSION__/${LUDUS_VERSION}/" dab.conf

make clean || true
make LUDUS_VERSION="${LUDUS_VERSION}"

OUT="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
mv ludus_*.tar.zst "../../${OUT}" 2>/dev/null || mv *.tar.zst "../../${OUT}"
sha256sum "../../${OUT}" > "../../${OUT}.sha256"
echo "Built: ${OUT}"
