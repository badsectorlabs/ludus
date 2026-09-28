#!/bin/bash
# Beta entry point: all client, LXC, migration and update logic lives in install.sh.
set -eu
export LUDUS_RELEASE_CHANNEL=beta
export R2_BUCKET_BASE_URL="${R2_BUCKET_BASE_URL:-https://beta-files.ludus.cloud}"

# A piped script has no sibling. Never trust an install.sh in the caller's cwd.
case "${0##*/}" in
  bash|zsh|sh|-bash|-zsh|-sh) ;;
  *)
    if [[ -f "$0" ]]; then
      installer_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
      if [[ -f "${installer_dir}/install.sh" ]]; then
        exec bash "${installer_dir}/install.sh" "$@"
      fi
    fi
    ;;
esac

beta_fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1"
  else
    echo 'Error: Neither curl nor wget is available.' >&2
    return 1
  fi
}

# Resolve once so a concurrent release cannot give client and server different tags.
version="${LUDUS_VERSION:-}"
template=''
args=("$@")
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) version="${2:?--version requires a tag}"; shift 2 ;;
    --template-file) template="${2:?--template-file requires a path}"; shift 2 ;;
    --airgapped|--iso-directory|--license-file)
      echo "Error: $1 requires the separately supplied install-offline.sh." >&2
      exit 1 ;;
    *) shift ;;
  esac
done
if [[ -z "${version}" && -n "${template}" ]]; then
  case "${template##*/}" in
    ludus-*-debian13-amd64.tar.zst)
      version="${template##*/}"
      version="${version#ludus-}"
      version="${version%-debian13-amd64.tar.zst}"
      ;;
  esac
fi
if [[ -z "${version}" ]]; then
  version=$(beta_fetch "${R2_BUCKET_BASE_URL%/}/latest.txt")
  version=$(printf '%s' "${version}" | tr -d '\r\n ')
fi
if [[ "${version}" != *-beta* || "${version}" == */* ]]; then
  echo 'Error: The beta channel requires a version containing -beta.' >&2
  exit 1
fi
installer_tmp=$(mktemp -d -t ludus-beta.XXXXXX)
trap 'rm -rf "${installer_tmp}"' EXIT
beta_fetch "${R2_BUCKET_BASE_URL%/}/${version}/install.sh" > "${installer_tmp}/install.sh"
bash "${installer_tmp}/install.sh" "${args[@]}" --version "${version}"
