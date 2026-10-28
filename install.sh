#!/bin/bash
# Client bootstrap and verified handoff only. Server operations live in ludus-installer.
set -eu
set -o pipefail

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1";
  elif command -v wget >/dev/null 2>&1; then wget -qO- "$1";
  else fail 'Neither curl nor wget is available.'; fi
}
verify_checksum() {
  local manifest=$1 artifact=$2 name=$3 digest entry rest expected='' count=0 actual
  while read -r digest entry rest; do
    entry=${entry#\*}
    if [[ "${entry##*/}" == "$name" ]]; then expected=$digest; count=$((count + 1)); fi
  done < "$manifest"
  [[ $count == 1 && ${#expected} == 64 ]] || fail "Expected one SHA-256 entry for $name"
  case "$expected" in *[!a-fA-F0-9]*) fail "Invalid SHA-256 entry for $name";; esac
  if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$artifact"); actual=${actual%% *};
  elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$artifact"); actual=${actual%% *};
  elif command -v sha256 >/dev/null 2>&1; then actual=$(sha256 -q "$artifact");
  else fail 'A SHA-256 checksum utility is required.'; fi
  expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
  [[ "$actual" == "$expected" ]] || fail "Checksum mismatch for $name"
}
latest_version() {
  local page=1 response tags tag
  if [[ $channel == beta ]]; then
    fetch "${release_base}/latest.txt" | tr -d '\r\n '
    return
  fi
  while :; do
    response=$(fetch "https://gitlab.com/api/v4/projects/54052321/repository/tags?per_page=100&page=${page}")
    tags=$(printf '%s' "$response" | grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*"' || true)
    [[ -n $tags ]] || fail 'Unable to discover a stable release.'
    while IFS= read -r tag; do
      tag=${tag#*:}; tag=${tag#*\"}; tag=${tag%\"}
      case "$tag" in *-beta*) ;; *) printf '%s\n' "$tag"; return;; esac
    done <<EOF
$tags
EOF
    page=$((page + 1))
  done
}
supported_host() {
  [[ $os == linux && $arch == amd64 ]] || return 1
  command -v pveversion >/dev/null 2>&1 || return 1
  local version major
  version=$(pveversion 2>/dev/null) || return 1
  case "$version" in pve-manager/*) ;; *) return 1;; esac
  major=${version#*/}; major=${major%%.*}
  case "$major" in ''|*[!0-9]*) return 1;; esac
  [[ $major -ge 8 ]] && command -v pvesh >/dev/null 2>&1 && command -v pct >/dev/null 2>&1
}
ask() {
  local answer
  printf '%s [y/N]: ' "$1" >&2
  if ! read -r answer </dev/tty; then return 1; fi
  [[ $answer == y || $answer == Y ]]
}
install_client() {
  local name="ludus-client_${os}-${arch}" url checksum="ludus_${version}_checksums.txt" completion_dir completion_shell
  if [[ $channel == beta ]]; then url="${release_base}/${version}/${name}";
  else url="https://gitlab.com/api/v4/projects/54052321/packages/generic/ludus/${version}/${name}-${version}"; fi
  fetch "$url" > "$temporary/$name"
  fetch "${url%/*}/${checksum}" > "$temporary/$checksum"
  verify_checksum "$temporary/$checksum" "$temporary/$name" "$name"
  [[ $os != macOS ]] || xattr -d com.apple.quarantine "$temporary/$name" 2>/dev/null || true
  if mkdir -p "$prefix" 2>/dev/null && [[ -w $prefix ]]; then
    install -m 0755 "$temporary/$name" "$prefix/ludus"
  else
    command -v sudo >/dev/null 2>&1 || fail 'Installing the client into this prefix requires sudo.'
    sudo sh -c 'mkdir -p "$1" && install -m 0755 "$2" "$1/ludus"' _ "$prefix" "$temporary/$name"
  fi
  printf '[+] Ludus client installed to %s/ludus\n' "$prefix"
  [[ $no_prompt != 1 ]] || return 0
  case "${SHELL:-}" in
    */zsh) completion_shell=zsh; completion_dir="${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions"; name=_ludus;;
    */bash)
      completion_shell=bash; name=ludus
      if [[ ${EUID:-$(id -u)} == 0 ]]; then
        completion_dir=$(pkg-config --variable=completionsdir bash-completion 2>/dev/null || true)
        completion_dir=${completion_dir:-/usr/share/bash-completion/completions}
      else completion_dir="${BASH_COMPLETION_USER_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion}/completions"; fi;;
    *) return 0;;
  esac
  if [[ ! -f "$completion_dir/$name" ]] && ask 'Install Ludus shell completions?'; then
    mkdir -p "$completion_dir"
    "$prefix/ludus" completion "$completion_shell" > "$completion_dir/$name"
    if [[ $completion_shell == zsh ]]; then
      printf 'Add to .zshrc: fpath+=("%s"); autoload -U compinit; compinit\n' "$completion_dir"
    elif [[ ${EUID:-$(id -u)} == 0 ]]; then
      # Root shells (including Proxmox's) do not load bash-completion by default.
      if ! grep -qs '^[^#]*bash_completion' "$HOME/.bashrc" /etc/bash.bashrc; then
        cat >> "$HOME/.bashrc" <<'EOF'

# enable bash completion in interactive shells
if ! shopt -oq posix; then
  if [ -f /usr/share/bash-completion/bash_completion ]; then
    . /usr/share/bash-completion/bash_completion
  elif [ -f /etc/bash_completion ]; then
    . /etc/bash_completion
  fi
fi
EOF
      fi
      printf 'Bash completions will be enabled in new shells.\n'
    else printf 'Enable completions in .bashrc with: source "%s/ludus"\n' "$completion_dir"; fi
  fi
}
handoff() {
  local binary="${LUDUS_INSTALLER_BINARY:-}" base manifest name value
  if [[ -n $binary ]]; then
    [[ -f $binary && -x $binary ]] || fail 'LUDUS_INSTALLER_BINARY must name a trusted executable.'
    case "$binary" in /*) ;; *) binary="$PWD/$binary";; esac
  else
    base="${LUDUS_R2_BASE:-https://lxc.ludus.cloud}"
    manifest=checksums.txt
    if [[ $channel == beta ]]; then base=$release_base; manifest="ludus_${version}_checksums.txt"; fi
    binary="$temporary/ludus-installer"
    fetch "${base%/}/${version}/ludus-installer" > "$binary"
    fetch "${base%/}/${version}/${manifest}" > "$temporary/installer-checksums.txt"
    verify_checksum "$temporary/installer-checksums.txt" "$binary" ludus-installer
    chmod 0700 "$binary"
    [[ "$("$binary" --build-version)" == "$version" ]] || fail 'Installer build version does not match the selected release.'
  fi
  # Preserve only documented defaults, not the caller's whole privileged environment.
  handoff_env=("LUDUS_RELEASE_CHANNEL=$channel" "R2_BUCKET_BASE_URL=$release_base")
  for name in LUDUS_INSTALLER_BINARY LUDUS_R2_BASE TEMPLATE_FILE NO_PROMPT MIGRATE_HOST SKIP_VERIFICATION VERIFY_PROXMOX_TLS \
    VMID LXC_HOSTNAME STORAGE LXC_ROOTFS_SIZE LXC_BRIDGE LXC_VLAN_TAG ETH0_IP ETH0_GW LXC_NAMESERVER \
    ENDPOINTS TOKEN_ID TOKEN_SECRET WG_EP WG_PORT NO_WG_FORWARD VM_STORAGE VM_STORAGE_FORMAT ISO_STORAGE LICENSE \
    CA_CERTIFICATE ENTERPRISE_PLUGIN CHECKSUM_FILE CHECKSUM_SIGNATURE CHECKSUM_PUBLIC_KEY IMPORT_DB \
    LUDUS_NAT_IP LUDUS_NAT_GATEWAY LUDUS_API_PORT LUDUS_ADMIN_PORT; do
    if value=$(printenv "$name"); then handoff_env+=("$name=$value"); fi
  done
  if [[ ${EUID:-$(id -u)} == 0 ]]; then
    env "${handoff_env[@]}" "$binary" ${server_args[@]+"${server_args[@]}"} --version "$version"
  else
    command -v sudo >/dev/null 2>&1 || fail 'Server installation requires root or sudo.'
    sudo env "${handoff_env[@]}" "$binary" ${server_args[@]+"${server_args[@]}"} --version "$version"
  fi
}

channel=${LUDUS_RELEASE_CHANNEL:-stable}
release_base=${R2_BUCKET_BASE_URL:-https://beta-files.ludus.cloud}; release_base=${release_base%/}
prefix=${PREFIX:-/usr/local/bin}
version=${LUDUS_VERSION:-}
template=${TEMPLATE_FILE:-}
server_only=${SERVER_ONLY:-0}
no_prompt=${NO_PROMPT:-0}
server_args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      cat <<'EOF'
Ludus client bootstrap and Proxmox server installer
Usage: install.sh [-p|--prefix DIR] [--server-only] [--version TAG] [server options]
The client is installed first; servers are offered only on Linux/amd64 Proxmox 8+.
--server-only, --migrate-host and --template-file skip client installation.
--no-prompt uses supplied flags/environment defaults without interactive prompts.
Server options (all handled by the Go installer):
  --template-file PATH  --migrate-host  --import-db PATH
  --vmid N  --hostname NAME  --storage NAME  --rootfs-size GIB
  --bridge NAME  --vlan-tag N  --ip STATIC_CIDR  --gw IP  --nameserver IP
  --endpoints "URL ..."  --token-id ID  --token-secret SECRET  --verify-proxmox-tls
  --wg-endpoint HOST  --wg-port N  --no-wg-forward  --vm-storage NAME  --vm-storage-format raw|qcow2
  --iso-storage NAME  --license KEY  --enterprise-plugin PACKAGE.zip  --ca-certificate PATH
  --checksum-file PATH  --checksum-signature PATH  --checksum-public-key PATH
  --skip-verification
Offline installation requires the Ludus Enterprise install-offline.sh. LUDUS_INSTALLER_BINARY explicitly
trusts a local development executable instead of downloading the release installer.
EOF
      exit 0;;
    -p|--prefix) prefix=${2:?--prefix requires a directory}; shift 2;;
    --version) version=${2:?--version requires a tag}; shift 2;;
    --server-only) server_only=1; shift;;
    --migrate-host) server_only=1; server_args+=("$1"); shift;;
    --template-file) template=${2:?--template-file requires a path}; server_only=1; server_args+=("$1" "$2"); shift 2;;
    --no-prompt) no_prompt=1; server_args+=("$1"); shift;;
    --skip-verification|--verify-proxmox-tls|--no-wg-forward|--no-wg-forward=*) server_args+=("$1"); shift;;
    --airgapped|--offline|--iso-directory|--license-file|--bginfo-file) fail "$1 requires the Ludus Enterprise install-offline.sh";;
    --vm-storage|--vm-storage-format|--iso-storage|--ca-certificate|--enterprise-plugin|--checksum-file|--checksum-signature|--checksum-public-key|--token-id|--token-secret|--vmid|--hostname|--storage|--rootfs-size|--bridge|--vlan-tag|--ip|--gw|--nameserver|--endpoints|--import-db|--wg-endpoint|--wg-port|--license)
      [[ $# -ge 2 ]] || fail "$1 requires a value"
      server_args+=("$1" "$2"); shift 2;;
    *) fail "Unknown option: $1";;
  esac
done
[[ -z $template && ${MIGRATE_HOST:-0} != 1 ]] || server_only=1
case "$channel" in stable|beta) ;; *) fail "Unknown release channel: $channel";; esac
if [[ -z $version && -n $template ]]; then
  case "${template##*/}" in ludus-*-debian13-amd64.tar.zst) version=${template##*/}; version=${version#ludus-}; version=${version%-debian13-amd64.tar.zst};; esac
fi
os=$(uname); arch=$(uname -m)
case "$os" in Darwin) os=macOS;; Linux|*BusyBox*) os=linux;; FreeBSD) ;; *) fail "Unsupported client OS: $os";; esac
case "$arch" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; armv7l|armv8l|armv9l) arch=arm;; i686) arch=386;; *) fail "Unsupported architecture: $arch";; esac
if [[ $server_only == 1 ]]; then supported_host || fail '--server-only requires an amd64 Linux Proxmox 8+ host.'; fi
[[ -n $version ]] || version=$(latest_version)
case "$version" in ''|*[!A-Za-z0-9._+-]*) fail 'Invalid release version.';; esac
[[ $channel != beta || $version == *-beta* ]] || fail 'The beta channel requires a version containing -beta.'
temporary=$(mktemp -d -t ludus-bootstrap.XXXXXX)
trap 'rm -rf "$temporary"' EXIT
if [[ $server_only == 1 ]]; then handoff; exit $?; fi
install_client
if supported_host; then
  if [[ $no_prompt == 1 ]] || ask 'Proxmox detected. Install or upgrade the Ludus server?'; then handoff; fi
fi
