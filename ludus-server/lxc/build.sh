#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

: "${LUDUS_VERSION:?LUDUS_VERSION must be set}"
: "${PACKER_VERSION:=1.11.2}"
: "${PACKER_PROXMOX_VERSION:=1.2.1}"
: "${PACKER_ANSIBLE_VERSION:=1.1.1}"
: "${BLOCKY_VERSION:=0.30.0}"
: "${BLOCKY_LINUX_X86_64_SHA256:=1641ec6821abd39ff61cf47f343f518c33a1973c64ad6c0deb030b0f02d9ef30}"
: "${BGINFO_SHA256:=599b391980a5c9cbadd6c70ba3d5a5258db8b9d87c68b3fe587d9dc84effdf63}"
: "${LUDUS_SOURCE_BSL_URL:=https://github.com/badsectorlabs/ludus-source-bsl.git}"
: "${LUDUS_SOURCE_BSL_REF:=main}"
: "${LUDUS_SOURCE_BSL_ARCHIVE:=}"
: "${DAB_CACHE_DIR:=}"
: "${DAB_WGET_TIMEOUT:=60}"
: "${DAB_WGET_TRIES:=3}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $1" >&2
    exit 1
  fi
}

require_command curl
require_command unzip
require_command python3
require_command ansible-galaxy
require_command git
require_command install
require_command mktemp
require_command sha256sum
require_command tar

mkdir -p deps
if [[ ! -f deps/packer ]]; then
  curl -fsSL "https://releases.hashicorp.com/packer/${PACKER_VERSION}/packer_${PACKER_VERSION}_linux_amd64.zip" -o /tmp/packer.zip
  unzip -o /tmp/packer.zip -d deps/
fi
for plugin in proxmox:${PACKER_PROXMOX_VERSION} ansible:${PACKER_ANSIBLE_VERSION}; do
  name=${plugin%%:*}; ver=${plugin##*:}
  rm -f "deps/packer-plugin-${name}"
  if ! compgen -G "deps/packer-plugin-${name}_v${ver}_*" >/dev/null; then
    curl -fsSL "https://github.com/hashicorp/packer-plugin-${name}/releases/download/v${ver}/packer-plugin-${name}_v${ver}_x5.0_linux_amd64.zip" -o /tmp/pp.zip
    unzip -o /tmp/pp.zip -d deps/
  fi
done

stage_packer_plugin() {
  local name=$1
  local source=$2
  local version=$3
  local binary
  binary=$(find deps -maxdepth 1 -type f -name "packer-plugin-${name}_v${version}_*" | sort | head -n1)
  if [[ -z "${binary}" ]]; then
    echo "ERROR: packer-plugin-${name} v${version} not found in deps" >&2
    exit 1
  fi

  local dest="deps/packer-plugins/${source}"
  mkdir -p "${dest}"
  cp "${binary}" "${dest}/"
  sha256sum "${dest}/$(basename "${binary}")" | awk '{print $1}' > "${dest}/$(basename "${binary}")_SHA256SUM"
}

rm -rf deps/packer-plugins
stage_packer_plugin proxmox github.com/hashicorp/proxmox "${PACKER_PROXMOX_VERSION}"
stage_packer_plugin ansible github.com/hashicorp/ansible "${PACKER_ANSIBLE_VERSION}"

BLOCKY_ARCHIVE="blocky_v${BLOCKY_VERSION}_Linux_x86_64.tar.gz"
BLOCKY_DEPS_DIR="deps/blocky/${BLOCKY_VERSION}"
if [[ ! -x "${BLOCKY_DEPS_DIR}/blocky" ]]; then
  mkdir -p "${BLOCKY_DEPS_DIR}"
  curl -fsSL "https://github.com/0xERR0R/blocky/releases/download/v${BLOCKY_VERSION}/${BLOCKY_ARCHIVE}" \
    -o "/tmp/${BLOCKY_ARCHIVE}"
  printf '%s  %s\n' "${BLOCKY_LINUX_X86_64_SHA256}" "/tmp/${BLOCKY_ARCHIVE}" | sha256sum --check --status
  tar -xzf "/tmp/${BLOCKY_ARCHIVE}" -C "${BLOCKY_DEPS_DIR}" blocky
fi

if [[ ! -f deps/bginfo.exe ]]; then
  curl -fsSL "https://live.sysinternals.com/bginfo.exe" -o deps/bginfo.exe
fi
printf '%s  %s\n' "${BGINFO_SHA256}" "deps/bginfo.exe" | sha256sum --check --status

mkdir -p deps/python-wheels
python3 -m pip download --only-binary=:all: --dest deps/python-wheels -r python-requirements.txt

rm -rf deps/collections deps/roles
mkdir -p deps/collections deps/roles
ansible-galaxy collection install -r ../ansible/requirements.yml -p deps/collections --force
ansible-galaxy role install -r ../ansible/requirements.yml -p deps/roles --force

# Bundle the first-party source and all of its pinned submodules so an
# air-gapped server can register its catalog without cloning from GitHub.
rm -rf deps/ludus-source-bsl
if [[ -n $LUDUS_SOURCE_BSL_ARCHIVE ]]; then
  [[ -f $LUDUS_SOURCE_BSL_ARCHIVE && -r $LUDUS_SOURCE_BSL_ARCHIVE && -s $LUDUS_SOURCE_BSL_ARCHIVE ]] || {
    echo "ERROR: LUDUS_SOURCE_BSL_ARCHIVE is not a readable, non-empty file: $LUDUS_SOURCE_BSL_ARCHIVE" >&2
    exit 1
  }
  tar -tzf "$LUDUS_SOURCE_BSL_ARCHIVE" >/dev/null
  install -m 0644 "$LUDUS_SOURCE_BSL_ARCHIVE" deps/ludus-source-bsl.tar.gz
  echo "Using local ludus-source-bsl archive: $LUDUS_SOURCE_BSL_ARCHIVE"
else
  rm -f deps/ludus-source-bsl.tar.gz
  git clone --depth 1 --branch "${LUDUS_SOURCE_BSL_REF}" \
    --recurse-submodules --shallow-submodules -- \
    "${LUDUS_SOURCE_BSL_URL}" deps/ludus-source-bsl
  tar --exclude='*/.git' --exclude='*/.git/*' \
    --format=ustar -czf deps/ludus-source-bsl.tar.gz -C deps ludus-source-bsl
fi

if [[ ! -f ../../binaries/ludus-server ]]; then
  echo "ERROR: binaries/ludus-server not found (run 'build all' first)" >&2
  exit 1
fi

# Substitute version into dab.conf before make parses BASEDIR.
sed -i "s/^Version: .*/Version: ${LUDUS_VERSION}/" dab.conf
if [[ -n $DAB_CACHE_DIR ]]; then
  [[ $DAB_CACHE_DIR =~ ^/[A-Za-z0-9._/-]+$ && $DAB_CACHE_DIR != *"/../"* && $DAB_CACHE_DIR != */.. ]] || {
    echo "ERROR: DAB_CACHE_DIR must be a simple absolute path without '..': $DAB_CACHE_DIR" >&2
    exit 1
  }
  install -d -m 0755 "$DAB_CACHE_DIR"
  sed -i "s|^CacheDir: .*|CacheDir: ${DAB_CACHE_DIR}|" dab.conf
fi
[[ $DAB_WGET_TIMEOUT =~ ^[1-9][0-9]*$ ]] || {
  echo "ERROR: DAB_WGET_TIMEOUT must be a positive integer" >&2
  exit 1
}
[[ $DAB_WGET_TRIES =~ ^[1-9][0-9]*$ ]] || {
  echo "ERROR: DAB_WGET_TRIES must be a positive integer" >&2
  exit 1
}

DAB_WGETRC=$(mktemp)
cleanup() {
  rm -f "$DAB_WGETRC"
}
trap cleanup EXIT INT TERM
printf 'timeout = %s\ntries = %s\nretry_connrefused = on\n' \
  "$DAB_WGET_TIMEOUT" "$DAB_WGET_TRIES" >"$DAB_WGETRC"

make clean || true
WGETRC="$DAB_WGETRC" make LUDUS_VERSION="${LUDUS_VERSION}" BLOCKY_VERSION="${BLOCKY_VERSION}"

OUT="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
mv ludus_*.tar.zst "../../${OUT}" 2>/dev/null || mv *.tar.zst "../../${OUT}"
(cd ../.. && sha256sum "${OUT}" > "${OUT}.sha256")
echo "Built: ${OUT}"
