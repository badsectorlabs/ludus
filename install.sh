#!/bin/bash - 
#===============================================================================
#
#          FILE: install.sh
# 
#         USAGE: curl https://ludus.cloud/install | bash
#                curl https://ludus.cloud/install | zsh
#                 OR
#                wget -qO- https://ludus.cloud.install | bash
#                wget -qO- https://ludus.cloud.install | zsh
# 
#   DESCRIPTION: Ludus server Installer Script.
#
#                This script installs the Ludus client into /usr/local/bin.
#                This script optionally installs the Ludus server on amd64 Linux hosts.
#
#       OPTIONS: -p, --prefix "${INSTALL_PREFIX}"
#                      Prefix to install the Ludus client into.  Defaults to /usr/local/bin
#                      Usage: curl https://ludus.cloud/install | bash -s -- -p ~/.local/bin
#  REQUIREMENTS: bash, uname, tar/unzip, curl/wget, grep, sudo (if not run
#                as root), install, mktemp, sha256sum/shasum/sha256
#
#          BUGS: Please report.
#
#         NOTES: Homepage: https://ludus.cloud
#                  Issues: https://gitlab.com/badsectorlabs/ludus/-/issues
#
#===============================================================================
set -o nounset                              # Treat unset variables as an error

#-------------------------------------------------------------------------------
# DEFAULTS
#-------------------------------------------------------------------------------
PROJECT_ID=54052321
PREFIX="${PREFIX:-}"
AIRGAPPED_INSTALL=0
AIRGAPPED_ISO_FILENAMES=(
  debian-13.7.0-amd64-netinst.iso
  debian-11.7.0-amd64-netinst.iso
  debian-12.14.0-amd64-netinst.iso
  kali-linux-2026.1-installer-netinst-amd64.iso
  22621.525.220925-0207.ni_release_svc_refresh_CLIENTENTERPRISEEVAL_OEMRET_x64FRE_en-us.iso
  SERVER_EVAL_x64FRE_en-us.iso
  virtio-win-0.1.240.iso
  virtio-win-0.1.229.iso
)

if [[ -z "${PREFIX}" ]]; then
  INSTALL_PREFIX="/usr/local/bin"
fi

#-------------------------------------------------------------------------------
# FUNCTIONS
#-------------------------------------------------------------------------------
#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  print_banner
#   DESCRIPTION:  Prints a banner
#    PARAMETERS:  none
#       RETURNS:  0
#-------------------------------------------------------------------------------
print_banner() {
  cat <<-'EOF'
====================================
 _      _   _  ____   _   _  ____  
| |    | | | ||  _ \ | | | |/ ___\ 
| |    | | | || | | || | | |\___ \ 
| |___ | |_| || |_| || |_| | ___) |
|____/  \___/ |____/  \___/  \___/ 

====================================
EOF
}


#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  command_exists
#   DESCRIPTION:  Checks if a command is available
#    PARAMETERS:  $1 = Command to check
#       RETURNS:  0 = command is available
#                 1 = command is not available
#-------------------------------------------------------------------------------
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

legacy_host_install_exists() {
  [[ -f /opt/ludus/config.yml ]] \
    && [[ ! -f /opt/ludus/install/.bootstrap-complete ]] \
    && [[ ! -f /etc/ludus-lxc.json ]] \
    && [[ ! -f /etc/systemd/system/ludus-lxc-forwarding.service ]] \
    && command_exists pveversion
}

lxc_host_install_exists() {
  [[ -f /etc/ludus-lxc.json ]] \
    || [[ -f /etc/systemd/system/ludus-lxc-forwarding.service ]]
}

run_ludus_server_install() {
  if [[ ${EUID} == 0 ]]; then
    ludus_install_server
    return
  fi
  command_exists sudo || {
    print_message "[!] Server installation requires root and sudo is unavailable" "error"
    return 1
  }

  local installer installer_tmp
  local -a args
  case "${0##*/}" in
    bash|zsh|sh)
      installer_tmp=$(make_tempdir "ludus-installer") || return 1
      installer="${installer_tmp}/install.sh"
      download_file "https://ludus.cloud/install" "${installer_tmp}" install.sh || return 1
      chmod 0700 "${installer}"
      ;;
    *) installer="$0" ;;
  esac

  args=(--version "${LUDUS_VERSION}")
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    args+=(--migrate-host)
  else
    args+=(--server-only)
  fi
  [[ ${NO_PROMPT:-0} != 1 ]] || args+=(--no-prompt)
  [[ -z ${TEMPLATE_FILE:-} ]] || args+=(--template-file "${TEMPLATE_FILE}")
  [[ ${AIRGAPPED_INSTALL:-0} != 1 ]] || args+=(--airgapped)
  [[ -z ${VM_STORAGE:-} ]] || args+=(--vm-storage "${VM_STORAGE}")
  [[ -z ${VM_STORAGE_FORMAT:-} ]] || args+=(--vm-storage-format "${VM_STORAGE_FORMAT}")
  [[ -z ${ISO_STORAGE:-} ]] || args+=(--iso-storage "${ISO_STORAGE}")
  [[ -z ${ISO_DIRECTORY:-} ]] || args+=(--iso-directory "${ISO_DIRECTORY}")
  [[ -z ${CA_CERTIFICATE:-} ]] || args+=(--ca-certificate "${CA_CERTIFICATE}")
  [[ -z ${ENTERPRISE_PLUGIN:-} ]] || args+=(--enterprise-plugin "${ENTERPRISE_PLUGIN}")
  [[ -z ${LICENSE_FILE:-} ]] || args+=(--license-file "${LICENSE_FILE}")
  [[ -z ${CHECKSUM_FILE:-} ]] || args+=(--checksum-file "${CHECKSUM_FILE}")
  [[ -z ${CHECKSUM_SIGNATURE:-} ]] || args+=(--checksum-signature "${CHECKSUM_SIGNATURE}")
  [[ -z ${CHECKSUM_PUBLIC_KEY:-} ]] || args+=(--checksum-public-key "${CHECKSUM_PUBLIC_KEY}")
  [[ ${SKIP_VERIFICATION:-0} != 1 ]] || args+=(--skip-verification)
  [[ -z ${TOKEN_ID:-} ]] || args+=(--token-id "${TOKEN_ID}")
  [[ -z ${TOKEN_SECRET:-} ]] || args+=(--token-secret "${TOKEN_SECRET}")
  [[ -z ${VMID:-} ]] || args+=(--vmid "${VMID}")
  [[ -z ${LXC_HOSTNAME:-} ]] || args+=(--hostname "${LXC_HOSTNAME}")
  [[ -z ${STORAGE:-} ]] || args+=(--storage "${STORAGE}")
  [[ -z ${LXC_ROOTFS_SIZE:-} ]] || args+=(--rootfs-size "${LXC_ROOTFS_SIZE}")
  [[ -z ${LXC_BRIDGE:-} ]] || args+=(--bridge "${LXC_BRIDGE}")
  [[ -z ${LXC_VLAN_TAG:-} ]] || args+=(--vlan-tag "${LXC_VLAN_TAG}")
  [[ -z ${ETH0_IP:-} ]] || args+=(--ip "${ETH0_IP}")
  [[ -z ${ETH0_GW:-} ]] || args+=(--gw "${ETH0_GW}")
  [[ -z ${LXC_NAMESERVER:-} ]] || args+=(--nameserver "${LXC_NAMESERVER}")
  [[ -z ${ENDPOINTS:-} ]] || args+=(--endpoints "${ENDPOINTS}")
  [[ ${VERIFY_PROXMOX_TLS:-0} != 1 ]] || args+=(--verify-proxmox-tls)
  [[ -z ${IMPORT_DB:-} ]] || args+=(--import-db "${IMPORT_DB}")
  [[ -z ${WG_EP:-} ]] || args+=(--wg-endpoint "${WG_EP}")
  [[ -z ${WG_PORT:-} ]] || args+=(--wg-port "${WG_PORT}")
  [[ -z ${LICENSE:-} ]] || args+=(--license "${LICENSE}")

  print_message "[+] Asking for sudo once to perform the server migration" "warn"
  sudo "${installer}" "${args[@]}"
}


#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  print_help
#   DESCRIPTION:  Prints out a help message
#    PARAMETERS:  none
#       RETURNS:  0
#-------------------------------------------------------------------------------
print_help() {
  local help_header
  local help_message

  help_header="Ludus Installer Script"
  help_message="Usage:
  -p, --prefix INSTALL_PREFIX
      Prefix to install the Ludus client into.  Directory must already exist.
      Default = /usr/local/bin

  Server (LXC) install flags — only used on a Proxmox host:
  --server-only          Skip client download/install and only install the server LXC
  --version VER          Ludus version to install (default: latest release tag)
  --template-file PATH   Use a local LXC template tarball (implies --server-only)
  --airgapped            Use only signed local installation media (implies --server-only)
  --vm-storage NAME      Storage for built VM disks (default: local)
  --vm-storage-format FORMAT
                         VM disk format: qcow2 or raw (default: qcow2)
  --iso-storage NAME     Shared storage for template ISOs (required with --airgapped; default: local otherwise)
  --iso-directory DIR    Directory containing all pinned template ISOs (implies --airgapped)
  --ca-certificate PATH  Copy a site-supplied PEM CA certificate into LXC and template trust stores
  --enterprise-plugin PATH
                         Install a local Enterprise plugin into both services
  --license-file PATH    Install a signed offline Enterprise license file
  --checksum-file PATH   SHA-256 manifest covering supplied release artifacts
  --checksum-signature PATH
                         Detached signature for --checksum-file
  --checksum-public-key PATH
                         Trusted PEM public key for signature verification
  --skip-verification    Bypass signed manifest and artifact checksum verification
  --token-id ID          Proxmox API token ID (e.g. root@pam!ludus)
  --token-secret SECRET  Proxmox API token secret
  --no-prompt            Non-interactive; use defaults / supplied flags
  --vmid N               VMID for the Ludus LXC (default: cluster nextid)
  --hostname NAME        LXC hostname (default: ludus)
  --storage NAME         Storage for the LXC rootfs (default: local-lvm)
  --rootfs-size GIB      LXC rootfs size in GiB, minimum 20 (default: 20)
  --bridge NAME          Proxmox bridge for LXC eth0 (default: vmbr0)
  --vlan-tag N           Optional VLAN tag for LXC eth0 (1-4094)
  --ip CIDR|dhcp         LXC eth0 address (default: dhcp)
  --gw IP                LXC eth0 gateway (required if --ip is not dhcp)
  --nameserver IP        DNS resolver assigned to the LXC
  --endpoints \"URL ...\"  Space-separated Proxmox API endpoints
  --verify-proxmox-tls   Validate Proxmox endpoint certificates from the LXC
  --wg-endpoint HOST     WireGuard endpoint clients will dial
  --wg-port N            WireGuard UDP listen/client port (default: 51820)
  --license KEY          License key (default: community)
  --migrate-host         Migrate the existing host install, preserving state and endpoints
  --import-db TARBALL    Import a complete --export-state archive before bootstrap

  -h, --help
      Prints this helpful message and exit."

  echo "${help_header}"
  echo ""
  echo "${help_message}"
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  print_message
#   DESCRIPTION:  Prints a message all fancy like
#    PARAMETERS:  $1 = Message to print
#                 $2 = Severity. info, ok, error, warn
#       RETURNS:  Formatted Message to stdout
#-------------------------------------------------------------------------------
print_message() {
  local message
  local severity
  local red
  local green
  local yellow
  local nc

  message="${1}"
  severity="${2}"
  red='\e[0;31m'
  green='\e[0;32m'
  yellow='\e[1;33m'
  nc='\e[0m'

  case "${severity}" in
    "info" ) printf "${nc}${message}${nc}\n";;
      "ok" ) printf "${green}${message}${nc}\n";;
   "error" ) printf "${red}${message}${nc}\n";;
    "warn" ) printf "${yellow}${message}${nc}\n";;
  esac


}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  make_tempdir
#   DESCRIPTION:  Makes a temp dir using mktemp if available
#    PARAMETERS:  $1 = Directory template
#       RETURNS:  0 = Created temp dir. Also prints temp file path to stdout
#                 1 = Failed to create temp dir
#                 20 = Failed to find mktemp
#-------------------------------------------------------------------------------
make_tempdir() {
  local template
  local tempdir
  local tempdir_rcode

  template="${1}.XXXXXX"

  if command -v mktemp >/dev/null 2>&1; then
    tempdir="$(mktemp -d -t "${template}")"
    tempdir_rcode="${?}"
    if [[ "${tempdir_rcode}" == "0" ]]; then
      echo "${tempdir}"
      return 0
    else
      return 1
    fi
  else
    return 20
  fi
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  determine_os
#   DESCRIPTION:  Attempts to determine host os using uname
#    PARAMETERS:  none
#       RETURNS:  0 = OS Detected. Also prints detected os to stdout
#                 1 = Unknown OS
#                 20 = 'uname' not found in path
#-------------------------------------------------------------------------------
determine_os() {
  local uname_out

  if command -v uname >/dev/null 2>&1; then
    uname_out="$(uname)"
    if [[ "${uname_out}" == "" ]]; then
      return 1
    else
      echo "${uname_out}"
      return 0
    fi
  else
    return 20
  fi
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  determine_arch
#   DESCRIPTION:  Attempt to determine architecture of host
#    PARAMETERS:  none
#       RETURNS:  0 = Arch Detected. Also prints detected arch to stdout
#                 1 = Unknown arch
#                 20 = 'uname' not found in path
#-------------------------------------------------------------------------------
determine_arch() {
  local uname_out

  if command -v uname >/dev/null 2>&1; then
    uname_out="$(uname -m)"
    if [[ "${uname_out}" == "" ]]; then
      return 1
    else
      echo "${uname_out}"
      return 0
    fi
  else
    return 20
  fi
}


#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  download_file
#   DESCRIPTION:  Downloads a file into the specified directory.  Attempts to
#                 use curl, then wget.  If neither is found, fail.
#    PARAMETERS:  $1 = url of file to download
#                 $2 = location to download file into on host system
#       RETURNS:  If curl or wget found, returns the return code of curl or wget
#                 20 = Could not find curl and wget
#-------------------------------------------------------------------------------
download_file() {
  local url
  local dir
  local filename
  local rcode

  url="${1}"
  dir="${2}"
  filename="${3}"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${url}" -o "${dir}/${filename}"
    rcode="${?}"
  elif command -v wget >/dev/null 2>&1; then
    wget --quiet  "${url}" -O "${dir}/${filename}"
    rcode="${?}"
  else
    rcode="20"
  fi
  
  return "${rcode}"
}

fetch_latest_tag() {
  local tag

  if command_exists curl; then
    tag=$(curl -s "https://gitlab.com/api/v4/projects/$PROJECT_ID/repository/tags" | grep -o '"name":"[^"]*' | cut -d'"' -f4 | head -n1)
  elif command_exists wget; then
    tag=$(wget -qO- "https://gitlab.com/api/v4/projects/$PROJECT_ID/repository/tags" | grep -o '"name":"[^"]*' | cut -d'"' -f4 | head -n1)
  else
    return 20
  fi

  if [[ -z "${tag}" ]]; then
    return 1
  fi
  echo "${tag}"
}

infer_ludus_version_from_template() {
  local name
  local version

  name=$(basename "${1}")
  case "${name}" in
    ludus-*-debian13-amd64.tar.zst)
      version="${name#ludus-}"
      version="${version%-debian13-amd64.tar.zst}"
      echo "${version}"
      ;;
    *)
      return 1
      ;;
  esac
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  checksum_check
#   DESCRIPTION:  Attempt to verify checksum of downloaded file to ensure
#                 integrity.  Tries multiple tools before failing.
#    PARAMETERS:  $1 = path to checksum file
#                 $2 = location of file to check
#                 $3 = working directory
#       RETURNS:  0 = checksum verified
#                 1 = checksum verification failed
#                 20 = failed to determine tool to use to check checksum
#                 30 = failed to change into tmp dir
#                 31 = failed to change back into working dir
#-------------------------------------------------------------------------------
checksum_check() {
  local checksum_file
  local file
  local dir
  local rcode
  local shasum_1
  local shasum_2
  local shasum_c

  checksum_file="${1}"
  file="${2}"
  dir="${3}"

  cd "${dir}" || return 30
  if command -v sha256sum >/dev/null 2>&1; then
    ## Not all sha256sum versions seem to have --ignore-missing, so filter the checksum file
    ## to only include the file we downloaded.
    grep "$(basename "${file}")" "${checksum_file}" > filtered_checksum.txt
    shasum_c="$(sha256sum -c "filtered_checksum.txt")"
    rcode="${?}"
  elif command -v shasum >/dev/null 2>&1; then
    ## With shasum on FreeBSD, we don't get to --ignore-missing, so filter the checksum file
    ## to only include the file we downloaded.
    grep "$(basename "${file}")" "${checksum_file}" > filtered_checksum.txt
    shasum_c="$(shasum -a 256 -c "filtered_checksum.txt")"
    rcode="${?}"
  elif command -v sha256 >/dev/null 2>&1; then
    ## With sha256 on FreeBSD, we don't get to --ignore-missing, so filter the checksum file
    ## to only include the file we downloaded.
    ## Also sha256 -c option seems to fail, so fall back to an if statement
    grep "$(basename "${file}")" "${checksum_file}" > filtered_checksum.txt
    shasum_1="$(sha256 -q "${file}")"
    shasum_2="$(awk '{print $1}' filtered_checksum.txt)"
    if [[ "${shasum_1}" == "${shasum_2}" ]]; then
      rcode="0"
    else
      rcode="1"
    fi
    shasum_c="Expected: ${shasum_1}, Got: ${shasum_2}"
  else
    return 20
  fi
  cd - >/dev/null 2>&1 || return 31
  
  if [[ "${rcode}" -gt "0" ]]; then
    echo "${shasum_c}"
  fi
  return "${rcode}"
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  create_prefix
#   DESCRIPTION:  Creates the install prefix (and any parent directories). If
#                 EUID not 0, then attempt to use sudo.
#    PARAMETERS:  $1 = prefix
#       RETURNS:  Return code of the tool used to make the directory
#                 0 = Created the directory
#                 >0 = Failed to create directory  
#                 20 = Could not find mkdir command
#                 21 = Could not find sudo command
#-------------------------------------------------------------------------------
create_prefix() {
  local prefix
  local rcode

  prefix="${1}"

  if command -v mkdir >/dev/null 2>&1; then
    if [[ "${EUID}" == "0" ]]; then
      mkdir -p "${prefix}"
      rcode="${?}"
    else
      # first try to create the directory
      mkdir -p "${prefix}" >/dev/null 2>&1
      rcode="${?}"
      if [[ "${rcode}" -gt "0" ]]; then
        # if that fails, try to use sudo
        if command -v sudo >/dev/null 2>&1; then
          print_message "[+] Asking for sudo password to create directory: ${prefix}" "warn"
          sudo mkdir -p "${prefix}"
          rcode="${?}"
        else
          rcode="21"
        fi
      fi
    fi
  else
    rcode="20"
  fi

  return "${rcode}"
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  install_file_linux
#   DESCRIPTION:  Installs a file into a location using 'install'.  If EUID not
#                 0, then attempt to use sudo.
#    PARAMETERS:  $1 = file to install
#                 $2 = location to install file into
#       RETURNS:  0 = File Installed
#                 1 = File not installed
#                 20 = Could not find install command
#                 21 = Could not find sudo command
#-------------------------------------------------------------------------------
install_file_linux() {
  local file
  local prefix
  local rcode

  file="${1}"
  prefix="${2}"

  if command -v install >/dev/null 2>&1; then
    if [[ "${EUID}" == "0" ]]; then
      install -C -b -S '_old' -m 755 -t "${prefix}" "${file}"
      rcode="${?}"
    else
      # First try to install the file
      install -C -b -S '_old' -m 755 -t "${prefix}" "${file}" >/dev/null 2>&1
      rcode="${?}"
      if [[ "${rcode}" -gt "0" ]]; then
        # If that fails, try to use sudo
        if command -v sudo >/dev/null 2>&1; then
          print_message "[+] Asking for sudo password to install file: ${file} to directory: ${prefix}" "warn"
          sudo install -C -b -S '_old' -m 755 -t "${prefix}" "${file}"
          rcode="${?}"
        else
          rcode="21"
        fi
      fi
    fi
  else
    rcode="20"
  fi

  return "${rcode}"
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  install_file_freebsd
#   DESCRIPTION:  Installs a file into a location using 'install'.  If EUID not
#                 0, then attempt to use sudo.
#    PARAMETERS:  $1 = file to install
#                 $2 = location to install file into
#       RETURNS:  0 = File Installed
#                 1 = File not installed
#                 20 = Could not find install command
#                 21 = Could not find sudo command
#-------------------------------------------------------------------------------
install_file_freebsd() {
  local file
  local prefix
  local rcode

  file="${1}"
  prefix="${2}"

  if command -v install >/dev/null 2>&1; then
    if [[ "${EUID}" == "0" ]]; then
      install -C -b -B '_old' -m 755 "${file}" "${prefix}"
      rcode="${?}"
    else
      install -C -b -B '_old' -m 755 "${file}" "${prefix}" >/dev/null 2>&1
      rcode="${?}"
      if [[ "${rcode}" -gt "0" ]]; then
        if command -v sudo >/dev/null 2>&1; then
          print_message "[+] Asking for sudo password to install file: ${file} to directory: ${prefix}" "warn"
          sudo install -C -b -B '_old' -m 755 "${file}" "${prefix}"
          rcode="${?}"
        else
          rcode="21"
        fi
      fi
    fi
  else
    rcode="20"
  fi

  return "${rcode}"
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  install_completions
#   DESCRIPTION:  Installs completion files for bash and zsh
#    PARAMETERS:  none; must be called after install
#       RETURNS:  0 = All good
#                 1 = Something went wrong
#-------------------------------------------------------------------------------
install_completions() {
  case "$SHELL" in
    */zsh)
      mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions"
      ludus completion zsh > "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions/_ludus"
      print_message "[+] Installed zsh completion file" "ok"
      if [[ ! "$fpath" == *"${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions"* ]]; then
        print_message "[+] To enable, add the following to your .zshrc:" "info"
        echo
        print_message "fpath+=\"${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions\"" "info"
        print_message "autoload -U compinit && compinit" "info"
        echo
      fi
      ;;

    */bash)
      if [[ "$EUID" -eq 0 ]]; then
        completionsdir=$(pkg-config --variable=completionsdir bash-completion 2>/dev/null || echo "/usr/share/bash-completion/completions")
        mkdir -p "$completionsdir"
        ludus completion bash > "$completionsdir/ludus"
        print_message "[+] Installed bash completion file for all users in $completionsdir" "ok"
      else
        user_completions_dir="${BASH_COMPLETION_USER_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion}/completions"
        mkdir -p "$user_completions_dir"
        ludus completion bash > "$user_completions_dir/ludus"
        print_message "[+] Installed bash completion file in $user_completions_dir" "ok"
        print_message "[+] To enable, add the following to your .bashrc:" "info"
        echo
        print_message "source \"$user_completions_dir/ludus\"" "info"
        echo
      fi
      ;;

    *)
      print_message "[+] Unsupported shell: $SHELL" "error"
      return 1
      ;;
  esac

  # If the shell is bash, check the user's .bashrc file to make sure completions are enabled
  if [[ "$SHELL" == "/bin/bash" ]]; then
    if [[ ! -f "${HOME}/.bashrc" ]]; then
      print_message "[+] .bashrc file not found. Skipping completions check." "info"
    else
      # Add the completions script to the .bashrc file if it's not already there
      if [[ "${EUID}" == "0" ]] && { ! grep -q 'enable bash completion in interactive shells' "${HOME}/.bashrc" && ! grep -q 'enable programmable completion features' "${HOME}/.bashrc"; }; then 
        cat > "${HOME}/.bashrc" <<EOF
# enable bash completion in interactive shells
if ! shopt -oq posix; then
  if [ -f /usr/share/bash-completion/bash_completion ]; then
    . /usr/share/bash-completion/bash_completion
  elif [ -f /etc/bash_completion ]; then
    . /etc/bash_completion
  fi
fi
EOF
        print_message "[+] Installed bash completion commands to .bashrc" "info"
        print_message "[+] Bash completions will be enabled when you spawn a new shell" "info"
      else
        print_message "[+] Bash completion commands already in .bashrc" "info"
      fi
    fi
  fi
  return 0
}


# Verify every local artifact before token creation or cluster mutation.
verify_offline_inputs() {
  local input
  local signed_inputs=(
    "${TEMPLATE_FILE:-}"
    "${ENTERPRISE_PLUGIN:-}"
    "${LICENSE_FILE:-}"
    "${IMPORT_DB:-}"
  )
  if [[ "${AIRGAPPED_INSTALL:-0}" == "1" ]]; then
    local iso_name
    for iso_name in "${AIRGAPPED_ISO_FILENAMES[@]}"; do
      signed_inputs+=("${ISO_DIRECTORY%/}/${iso_name}")
    done
  fi

  local readable_inputs=("${signed_inputs[@]}" "${CA_CERTIFICATE:-}")
  for input in "${readable_inputs[@]}"; do
    [[ -z "${input}" ]] && continue
    if [[ ! -f "${input}" || ! -r "${input}" || ! -s "${input}" ]]; then
      print_message "[!] Local input must be a readable, non-empty file: ${input}" "error"
      exit 1
    fi
  done

  if [[ -n "${LICENSE_FILE:-}" && -z "${ENTERPRISE_PLUGIN:-}" ]]; then
    print_message "[!] --enterprise-plugin is required with --license-file" "error"
    exit 1
  fi
  if [[ -n "${LICENSE_FILE:-}" && -z "${LICENSE:-}" ]]; then
    print_message "[!] --license KEY is required with --license-file" "error"
    exit 1
  fi

  if [[ "${SKIP_VERIFICATION:-0}" == "1" ]]; then
    print_message "[!] WARNING: signed release artifact verification is disabled" "warn"
    return
  fi

  if [[ "${AIRGAPPED_INSTALL:-0}" == "1" && -z "${CHECKSUM_FILE:-}" ]]; then
    print_message "[!] Air-gapped installation requires --checksum-file, --checksum-signature, and --checksum-public-key" "error"
    exit 1
  fi
  if [[ -z "${CHECKSUM_FILE:-}" ]]; then
    if [[ -n "${CHECKSUM_SIGNATURE:-}" || -n "${CHECKSUM_PUBLIC_KEY:-}" ]]; then
      print_message "[!] --checksum-signature and --checksum-public-key require --checksum-file" "error"
      exit 1
    fi
    return
  fi

  for input in "${CHECKSUM_FILE}" "${CHECKSUM_SIGNATURE:-}" "${CHECKSUM_PUBLIC_KEY:-}"; do
    if [[ -z "${input}" || ! -f "${input}" || ! -r "${input}" || ! -s "${input}" ]]; then
      print_message "[!] Signed checksum verification requires readable manifest, signature, and public key files" "error"
      exit 1
    fi
  done

  command_exists openssl || { print_message "[!] openssl is required for signed checksum verification" "error"; exit 1; }
  if ! openssl dgst -sha256 -verify "${CHECKSUM_PUBLIC_KEY}" \
      -signature "${CHECKSUM_SIGNATURE}" "${CHECKSUM_FILE}" >/dev/null 2>&1; then
    print_message "[!] Artifact checksum manifest signature is invalid" "error"
    exit 1
  fi

  for input in "${signed_inputs[@]}"; do
    [[ -z "${input}" ]] && continue
    if ! python3 - "${CHECKSUM_FILE}" "${input}" <<'PY'
import hashlib
import hmac
import pathlib
import sys

manifest = pathlib.Path(sys.argv[1])
artifact = pathlib.Path(sys.argv[2])
matches = []
for raw_line in manifest.read_text(encoding="utf-8").splitlines():
    fields = raw_line.split()
    if len(fields) < 2:
        continue
    digest = fields[0].lower()
    name = fields[1].lstrip("*")
    if pathlib.PurePosixPath(name).name == artifact.name:
        matches.append((digest, name))

if len(matches) != 1:
    raise SystemExit(f"expected one checksum entry for {artifact.name}, found {len(matches)}")

expected, _ = matches[0]
if len(expected) != 64 or any(c not in "0123456789abcdef" for c in expected):
    raise SystemExit(f"invalid SHA-256 entry for {artifact.name}")

hasher = hashlib.sha256()
with artifact.open("rb") as handle:
    for chunk in iter(lambda: handle.read(1024 * 1024), b""):
        hasher.update(chunk)
if not hmac.compare_digest(hasher.hexdigest(), expected):
    raise SystemExit(f"checksum mismatch for {artifact.name}")
PY
    then
      print_message "[!] Artifact checksum verification failed for ${input}" "error"
      exit 1
    fi
  done
  print_message "[+] Signed release artifact manifest verified" "ok"
}

# Return the signed manifest's SHA-256 digest for a verified local artifact.
manifest_sha256_for() {
  python3 - "${CHECKSUM_FILE}" "$1" <<'PY'
import pathlib
import sys

artifact = pathlib.Path(sys.argv[2])
matches = []
for raw_line in pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").splitlines():
    fields = raw_line.split()
    if len(fields) >= 2 and pathlib.PurePosixPath(fields[1].lstrip("*")).name == artifact.name:
        matches.append(fields[0].lower())
if len(matches) != 1:
    raise SystemExit(f"expected one checksum entry for {artifact.name}, found {len(matches)}")
print(matches[0])
PY
}

# Validate shared ISO storage and publish local air-gapped ISOs.
prepare_airgapped_isos() {
  local node storage_config storage_status iso_name iso_path digest digest_source volid content_json existing_path actual_digest
  node=$(hostname)

  if ! storage_config=$(pvesh get "/storage/${ISO_STORAGE}" --output-format json 2>/dev/null); then
    print_message "[!] ISO storage does not exist: ${ISO_STORAGE}" "error"
    exit 1
  fi
  if ! python3 -c 'import json,sys; d=json.load(sys.stdin); content=d.get("content", ""); content=",".join(content) if isinstance(content,list) else content; raise SystemExit(0 if d.get("shared") in (1,True,"1") and "iso" in content.split(",") else 1)' <<<"${storage_config}"; then
    print_message "[!] ISO storage ${ISO_STORAGE} must be shared and support iso content" "error"
    exit 1
  fi
  if ! storage_status=$(pvesh get "/nodes/${node}/storage/${ISO_STORAGE}/status" --output-format json 2>/dev/null) \
      || ! python3 -c 'import json,sys; d=json.load(sys.stdin); raise SystemExit(0 if d.get("active") in (1,True,"1") and d.get("enabled",1) in (1,True,"1") else 1)' <<<"${storage_status}"; then
    print_message "[!] ISO storage ${ISO_STORAGE} is not active on ${node}" "error"
    exit 1
  fi

  content_json=$(pvesh get "/nodes/${node}/storage/${ISO_STORAGE}/content" --content iso --output-format json) \
    || { print_message "[!] Unable to list ISO storage ${ISO_STORAGE}" "error"; exit 1; }
  for iso_name in "${AIRGAPPED_ISO_FILENAMES[@]}"; do
    iso_path="${ISO_DIRECTORY%/}/${iso_name}"
    if [[ "${SKIP_VERIFICATION:-0}" == "1" ]]; then
      digest=$(sha256sum "${iso_path}" | cut -d' ' -f1) \
        || { print_message "[!] Unable to checksum local ISO ${iso_name}" "error"; exit 1; }
      digest_source="local source ISO"
    else
      digest=$(manifest_sha256_for "${iso_path}") \
        || { print_message "[!] Unable to read signed checksum for ${iso_name}" "error"; exit 1; }
      digest_source="signed manifest"
    fi
    volid="${ISO_STORAGE}:iso/${iso_name}"
    if python3 -c 'import json,sys; volid=sys.argv[1]; raise SystemExit(0 if any(item.get("volid")==volid for item in json.load(sys.stdin)) else 1)' "${volid}" <<<"${content_json}"; then
      existing_path=$(pvesm path "${volid}") \
        || { print_message "[!] Unable to resolve existing ISO ${volid}" "error"; exit 1; }
      actual_digest=$(sha256sum "${existing_path}" | cut -d' ' -f1) \
        || { print_message "[!] Unable to checksum existing ISO ${volid}" "error"; exit 1; }
      if [[ "${actual_digest}" != "${digest}" ]]; then
        print_message "[!] Existing ISO ${volid} does not match the ${digest_source}" "error"
        exit 1
      fi
      print_message "[+] ISO ${volid} already matches the ${digest_source}" "ok"
      continue
    fi
    print_message "[+] Uploading ISO ${iso_name} to ${ISO_STORAGE}" "info"
    local TMP_UPLOAD_FILE_NAME
    TMP_UPLOAD_FILE_NAME="/var/tmp/pveupload-$(head -c3 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c6)"
    cp "${iso_path}" "${TMP_UPLOAD_FILE_NAME}"
    pvesh create "/nodes/${node}/storage/${ISO_STORAGE}/upload" \
        --content iso --filename "${iso_name}" --checksum "${digest}" --checksum-algorithm sha256 --tmpfilename "${TMP_UPLOAD_FILE_NAME}" \
      || { print_message "[!] Failed to upload ISO ${iso_name}" "error"; exit 1; }
  done
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  ludus_install_server
#   DESCRIPTION:  Installs the Ludus server as an LXC container on a Proxmox
#                 host: generates/validates an API token, bootstraps the SDN
#                 zone + NAT VNet, fetches the LXC appliance template, creates
#                 and configures the container, and waits for first-boot.
#    PARAMETERS:  none — reads global flag vars (TOKEN_ID, VMID, ENDPOINTS, ...)
#                 LUDUS_VERSION must be set by the caller.
#       RETURNS:  0 = Server LXC running and bootstrapped
#                 exits non-zero on failure
#-------------------------------------------------------------------------------
ludus_install_server() {
  set -Eeuo pipefail
  umask 077
  [[ ${EUID} == 0 ]] || { print_message "[!] Server installation must run as root" "error"; exit 1; }
  LUDUS_NAT_IP=${LUDUS_NAT_IP:-192.0.2.253}
  LUDUS_NAT_GATEWAY=${LUDUS_NAT_GATEWAY:-192.0.2.254}
  LUDUS_API_PORT=${LUDUS_API_PORT:-8080}
  LUDUS_ADMIN_PORT=${LUDUS_ADMIN_PORT:-8081}
  # ---- 0. Preflight ------------------------------------------------------------
  command_exists pveversion || { print_message "[!] Not a Proxmox host (pveversion not found)" "error"; exit 1; }
  command_exists curl       || { print_message "[!] curl is required" "error"; exit 1; }
  command_exists python3    || { print_message "[!] python3 is required" "error"; exit 1; }

  local PVE_VER
  PVE_VER=$(pveversion | cut -d/ -f2 | cut -d. -f1)
  if [[ "${PVE_VER}" -lt 8 ]]; then
    print_message "[!] Proxmox 8.0+ is required (found ${PVE_VER}.x)" "error"
    exit 1
  fi
  if [[ "${AIRGAPPED_INSTALL}" == "1" && -z "${TEMPLATE_FILE:-}" ]]; then
    print_message "[!] --airgapped requires a signed local --template-file" "error"
    exit 1
  fi
  if legacy_host_install_exists && [[ ${MIGRATE_HOST:-0} != 1 ]]; then
    MIGRATE_HOST=1
    print_message "[+] Existing host-installed Ludus detected; upgrading it to the LXC runtime" "info"
  fi
  VMID=${VMID:-$(pvesh get /cluster/nextid --output-format json | python3 -c 'import json,sys; print(json.load(sys.stdin))')}
  [[ $VMID =~ ^[1-9][0-9]*$ ]] || { print_message "[!] Invalid LXC VMID" "error"; exit 1; }
  if pvesh get /cluster/resources --type vm --output-format json | python3 -c 'import json,sys; sys.exit(0 if any(int(v["vmid"])==int(sys.argv[1]) for v in json.load(sys.stdin)) else 1)' "$VMID"; then
    print_message "[!] VMID ${VMID} already exists; no credentials or guest state were changed" "error"
    exit 1
  fi

  if [[ "${NO_PROMPT:-0}" != "1" && -z "${CA_CERTIFICATE:-}" ]]; then
    local CA_CERTIFICATE_INPUT
    read -r -p "[?] CA certificate for template trust stores (optional path) [none]: " CA_CERTIFICATE_INPUT </dev/tty
    [[ -n "${CA_CERTIFICATE_INPUT}" ]] && CA_CERTIFICATE="${CA_CERTIFICATE_INPUT}"
  fi
  if [[ -n "${CA_CERTIFICATE:-}" ]]; then
    command_exists openssl || { print_message "[!] openssl is required to validate --ca-certificate" "error"; exit 1; }
    if ! openssl x509 -in "${CA_CERTIFICATE}" -noout >/dev/null 2>&1; then
      print_message "[!] CA certificate is not a valid PEM X.509 certificate: ${CA_CERTIFICATE}" "error"
      exit 1
    fi
  fi
  if [[ "${AIRGAPPED_INSTALL:-0}" == "1" ]]; then
    if [[ -z "${ISO_DIRECTORY:-}" || ! -d "${ISO_DIRECTORY}" || ! -r "${ISO_DIRECTORY}" ]]; then
      print_message "[!] --airgapped requires a readable --iso-directory" "error"
      exit 1
    fi
    command_exists pvesh || { print_message "[!] pvesh is required for air-gapped ISO upload" "error"; exit 1; }
    command_exists pvesm || { print_message "[!] pvesm is required for air-gapped ISO verification" "error"; exit 1; }
    command_exists sha256sum || { print_message "[!] sha256sum is required for air-gapped ISO verification" "error"; exit 1; }
    if [[ -z "${ISO_STORAGE:-}" && "${NO_PROMPT:-0}" != "1" ]]; then
      read -r -p "[?] Shared ISO storage pool (required): " ISO_STORAGE </dev/tty
    fi
    if [[ -z "${ISO_STORAGE:-}" ]]; then
      print_message "[!] --airgapped requires an explicit shared --iso-storage pool" "error"
      exit 1
    fi
  fi

  verify_offline_inputs
  if [[ "${AIRGAPPED_INSTALL:-0}" == "1" ]]; then
    prepare_airgapped_isos
  fi

  # ---- 4. Template -------------------------------------------------------------
  local TMPL_NAME TMPL_CACHE R2_BASE
  TMPL_NAME="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
  TMPL_CACHE="/var/lib/vz/template/cache/${TMPL_NAME}"
  install -d -m 0755 /var/lib/vz/template/cache
  if [[ -n "${TEMPLATE_FILE:-}" ]]; then
    print_message "[+] Using local template ${TEMPLATE_FILE}" "info"
    [[ "${TEMPLATE_FILE}" -ef "${TMPL_CACHE}" ]] || cp "${TEMPLATE_FILE}" "${TMPL_CACHE}"
  elif [[ ! -f "${TMPL_CACHE}" ]]; then
    print_message "[+] Downloading LXC template ${TMPL_NAME} ..." "info"
    R2_BASE="${LUDUS_R2_BASE:-https://lxc.ludus.cloud}"
    curl -fL "${R2_BASE}/ludus-lxc/${LUDUS_VERSION}/${TMPL_NAME}" -o "${TMPL_CACHE}"
    curl -fsSL "${R2_BASE}/ludus-lxc/${LUDUS_VERSION}/checksums.txt" -o /tmp/ludus-checksums.txt
    ( cd /var/lib/vz/template/cache && sha256sum -c /tmp/ludus-checksums.txt --ignore-missing ) \
      || { print_message "[!] Template checksum verification failed" "error"; exit 1; }
  else
    print_message "[+] Template ${TMPL_NAME} already present in cache" "info"
  fi

  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    if ! command_exists conntrack; then
      [[ ${AIRGAPPED_INSTALL:-0} != 1 ]] || {
        print_message "[!] Air-gapped host migration requires the conntrack package to be installed first" "error"
        exit 1
      }
      command_exists apt-get || {
        print_message "[!] Automatic conntrack installation requires apt-get" "error"
        exit 1
      }
      print_message "[+] Installing the host conntrack package required for endpoint handoff ..." "info"
      apt-get update -qq
      DEBIAN_FRONTEND=noninteractive apt-get install -y conntrack
    fi
    install -d -m 0700 /var/lib/ludus-migration
    MIGRATION_DIR=$(mktemp -d /var/lib/ludus-migration/upgrade.XXXXXXXX)
    tar --zstd -tf "$TMPL_CACHE" >"$MIGRATION_DIR/template-files"
    local MEMBER TARGET
    for TARGET in opt/ludus/ludus-server opt/ludus/install/migrate-host.sh; do
      MEMBER=$(python3 -c 'import sys; names=[n.strip() for n in open(sys.argv[1]) if n.strip().removeprefix("./")==sys.argv[2]]; assert len(names)==1, "Appliance lacks host migration support"; print(names[0])' "$MIGRATION_DIR/template-files" "$TARGET")
      tar --zstd -xOf "$TMPL_CACHE" "$MEMBER" >"$MIGRATION_DIR/${TARGET##*/}"
      chmod 0700 "$MIGRATION_DIR/${TARGET##*/}"
    done
    source "$MIGRATION_DIR/migrate-host.sh"
    migration_prepare
  fi

  if [[ -z "${LANGUAGE+x}" ]]; then
    export LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8 LC_CTYPE=en_US.UTF-8
  fi

  # Tiny JSON helper — avoids a jq dependency on the PVE host
  _json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

  local NODE
  NODE=$(hostname)

  # Use an installation-specific token. Never rotate a token used by another
  # container, particularly when a retry later fails on an occupied VMID.
  if [[ -z "${TOKEN_ID:-}" && -z "${TOKEN_SECRET:-}" ]]; then
    local TOK_JSON
    print_message "[+] Generating API token root@pam!ludus-lxc-${VMID} ..." "info"
    TOK_JSON=$(pveum user token add root@pam "ludus-lxc-${VMID}" --privsep 0 --output-format json) \
      || { print_message "[!] Token already exists. Supply its secret or choose a new VMID; existing tokens are never revoked." "error"; exit 1; }
    TOKEN_ID=$(echo "${TOK_JSON}" | _json 'd["full-tokenid"]')
    TOKEN_SECRET=$(echo "${TOK_JSON}" | _json 'd["value"]')
  elif [[ -z "${TOKEN_ID:-}" || -z "${TOKEN_SECRET:-}" ]]; then
    print_message "[!] --token-id and --token-secret must be supplied together" "error"
    exit 1
  fi

  local AUTH EP_LOCAL
  AUTH="Authorization: PVEAPIToken=${TOKEN_ID}=${TOKEN_SECRET}"
  EP_LOCAL="https://localhost:8006"
  if ! curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/version" | grep -q version; then
    print_message "[!] Proxmox API token validation failed" "error"
    exit 1
  fi
  print_message "[+] Proxmox API token validated" "ok"

  # ---- 2. Gather config --------------------------------------------------------
  local CLUSTER_JSON DEFAULT_EPS DEFAULT_VMID DEFAULT_STORAGE
  CLUSTER_JSON=$(curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/status")
  DEFAULT_EPS=$(echo "${CLUSTER_JSON}" | _json '" ".join("https://"+n["ip"]+":8006" for n in d["data"] if n["type"]=="node" and n.get("ip"))' 2>/dev/null || echo "")
  [[ -z "${DEFAULT_EPS}" ]] && DEFAULT_EPS="https://$(hostname -I | awk '{print $1}'):8006"

  if [[ "${NO_PROMPT:-0}" != "1" ]]; then
    read -r -p "[?] Proxmox API endpoints (space-separated) [${DEFAULT_EPS}]: " ENDPOINTS </dev/tty
    ENDPOINTS=${ENDPOINTS:-${DEFAULT_EPS}}

    DEFAULT_VMID=${VMID}
    read -r -p "[?] LXC VMID [${DEFAULT_VMID}]: " VMID </dev/tty
    VMID=${VMID:-${DEFAULT_VMID}}

    DEFAULT_STORAGE=$(curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/nodes/${NODE}/storage?content=rootdir" | _json 'd["data"][0]["storage"]' 2>/dev/null || echo "local-lvm")
    read -r -p "[?] LXC rootfs storage [${DEFAULT_STORAGE}]: " STORAGE </dev/tty
    STORAGE=${STORAGE:-${DEFAULT_STORAGE}}

    read -r -p "[?] LXC hostname [${LXC_HOSTNAME:-ludus}]: " LXC_HOSTNAME_INPUT </dev/tty
    LXC_HOSTNAME=${LXC_HOSTNAME_INPUT:-${LXC_HOSTNAME:-ludus}}
    read -r -p "[?] LXC rootfs size in GiB [${LXC_ROOTFS_SIZE:-20}]: " LXC_ROOTFS_SIZE_INPUT </dev/tty
    LXC_ROOTFS_SIZE=${LXC_ROOTFS_SIZE_INPUT:-${LXC_ROOTFS_SIZE:-20}}
    read -r -p "[?] LXC management bridge [${LXC_BRIDGE:-vmbr0}]: " LXC_BRIDGE_INPUT </dev/tty
    LXC_BRIDGE=${LXC_BRIDGE_INPUT:-${LXC_BRIDGE:-vmbr0}}
    read -r -p "[?] LXC management VLAN tag [${LXC_VLAN_TAG:-none}]: " LXC_VLAN_TAG_INPUT </dev/tty
    LXC_VLAN_TAG=${LXC_VLAN_TAG_INPUT:-${LXC_VLAN_TAG:-}}
    [[ "${LXC_VLAN_TAG}" == "none" ]] && LXC_VLAN_TAG=""

    local ETH0_IP_INPUT
    read -r -p "[?] LXC eth0 IP (CIDR, or 'dhcp') [${ETH0_IP:-dhcp}]: " ETH0_IP_INPUT </dev/tty
    ETH0_IP=${ETH0_IP_INPUT:-${ETH0_IP:-dhcp}}
    if [[ "${ETH0_IP}" != "dhcp" ]]; then
      local ETH0_GW_INPUT
      read -r -p "[?] LXC eth0 gateway [${ETH0_GW:-}]: " ETH0_GW_INPUT </dev/tty
      ETH0_GW=${ETH0_GW_INPUT:-${ETH0_GW:-}}
    fi
    read -r -p "[?] LXC DNS resolver IP [inherit from Proxmox]: " LXC_NAMESERVER_INPUT </dev/tty
    LXC_NAMESERVER=${LXC_NAMESERVER_INPUT:-${LXC_NAMESERVER:-}}

    local WG_DEFAULT=""
    if [[ "${ETH0_IP}" != "dhcp" ]]; then
      WG_DEFAULT="${ETH0_IP%%/*}"
    fi
    read -r -p "[?] WireGuard endpoint (IP/host clients dial)${WG_DEFAULT:+ [${WG_DEFAULT}]}: " WG_EP </dev/tty
    WG_EP=${WG_EP:-${WG_DEFAULT}}
    if [[ -z "${WG_EP}" ]]; then
      print_message "[!] WireGuard endpoint is required" "error"
      exit 1
    fi
    read -r -p "[?] WireGuard UDP port [${WG_PORT:-51820}]: " WG_PORT_INPUT </dev/tty
    WG_PORT=${WG_PORT_INPUT:-${WG_PORT:-51820}}

    local VM_STORAGE_INPUT VM_STORAGE_FORMAT_INPUT ISO_STORAGE_INPUT
    read -r -p "[?] VM storage pool [${VM_STORAGE:-local}]: " VM_STORAGE_INPUT </dev/tty
    VM_STORAGE=${VM_STORAGE_INPUT:-${VM_STORAGE:-local}}
    read -r -p "[?] VM storage format [${VM_STORAGE_FORMAT:-qcow2}]: " VM_STORAGE_FORMAT_INPUT </dev/tty
    VM_STORAGE_FORMAT=${VM_STORAGE_FORMAT_INPUT:-${VM_STORAGE_FORMAT:-qcow2}}
    if [[ "${AIRGAPPED_INSTALL:-0}" != "1" ]]; then
      read -r -p "[?] ISO storage pool [${ISO_STORAGE:-local}]: " ISO_STORAGE_INPUT </dev/tty
      ISO_STORAGE=${ISO_STORAGE_INPUT:-${ISO_STORAGE:-local}}
    fi
    read -r -p "[?] License key [community]: " LICENSE </dev/tty
    LICENSE=${LICENSE:-community}
  else
    ENDPOINTS=${ENDPOINTS:-${DEFAULT_EPS}}
    VMID=${VMID:-$(curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/nextid" | _json 'd["data"]')}
    STORAGE=${STORAGE:-$(curl -fsk -H "${AUTH}" "${EP_LOCAL}/api2/json/nodes/${NODE}/storage?content=rootdir" | _json 'next((s["storage"] for s in d["data"] if s["storage"]=="local-lvm" and s.get("active")), next((s["storage"] for s in d["data"] if s.get("active")), ""))')}
    LXC_HOSTNAME=${LXC_HOSTNAME:-ludus}
    LXC_ROOTFS_SIZE=${LXC_ROOTFS_SIZE:-20}
    LXC_BRIDGE=${LXC_BRIDGE:-vmbr0}
    LXC_VLAN_TAG=${LXC_VLAN_TAG:-}
    ETH0_IP=${ETH0_IP:-dhcp}
    if [[ -z "${WG_EP:-}" ]]; then
      if [[ "${ETH0_IP}" != "dhcp" ]]; then
        WG_EP="${ETH0_IP%%/*}"
      else
        print_message "[!] --wg-endpoint is required in --no-prompt mode when eth0 uses DHCP" "error"
        exit 1
      fi
    fi
    WG_PORT=${WG_PORT:-51820}
    VM_STORAGE=${VM_STORAGE:-local}
    VM_STORAGE_FORMAT=${VM_STORAGE_FORMAT:-qcow2}
    ISO_STORAGE=${ISO_STORAGE:-local}
    LICENSE=${LICENSE:-community}
  fi

  if ! [[ "${VMID}" =~ ^[1-9][0-9]*$ ]]; then
    print_message "[!] LXC VMID must be a positive integer" "error"
    exit 1
  fi
  if ! [[ "${LXC_HOSTNAME}" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]]; then
    print_message "[!] LXC hostname is invalid: ${LXC_HOSTNAME}" "error"
    exit 1
  fi
  if ! [[ "${LXC_ROOTFS_SIZE}" =~ ^[0-9]+$ ]] || [[ "${LXC_ROOTFS_SIZE}" -lt 20 ]]; then
    print_message "[!] LXC rootfs size must be an integer of at least 20 GiB" "error"
    exit 1
  fi
  if ! [[ "${LXC_BRIDGE}" =~ ^[A-Za-z0-9_.:-]+$ ]] || ! ip link show "${LXC_BRIDGE}" >/dev/null 2>&1; then
    print_message "[!] LXC management bridge is invalid or unavailable on ${NODE}: ${LXC_BRIDGE}" "error"
    exit 1
  fi
  if [[ -n "${LXC_VLAN_TAG}" ]] \
      && { ! [[ "${LXC_VLAN_TAG}" =~ ^[0-9]+$ ]] \
        || [[ "${LXC_VLAN_TAG}" -lt 1 ]] || [[ "${LXC_VLAN_TAG}" -gt 4094 ]]; }; then
    print_message "[!] LXC VLAN tag must be between 1 and 4094" "error"
    exit 1
  fi
  if [[ "${ETH0_IP}" != "dhcp" && -z "${ETH0_GW:-}" ]]; then
    print_message "[!] --gw is required when --ip is static" "error"
    exit 1
  fi
  if [[ "${ETH0_IP}" != "dhcp" ]] \
      && ! python3 -c 'import ipaddress,sys; ipaddress.ip_interface(sys.argv[1]); ipaddress.ip_address(sys.argv[2])' "${ETH0_IP}" "${ETH0_GW}" 2>/dev/null; then
    print_message "[!] Static LXC IP or gateway is invalid" "error"
    exit 1
  fi
  if [[ -n "${LXC_NAMESERVER:-}" ]] \
      && ! python3 -c 'import ipaddress,sys; ipaddress.ip_address(sys.argv[1])' "${LXC_NAMESERVER}" 2>/dev/null; then
    print_message "[!] LXC DNS resolver must be an IP address" "error"
    exit 1
  fi
  case "${VM_STORAGE_FORMAT}" in
    qcow2|raw) ;;
    *) print_message "[!] VM storage format must be qcow2 or raw" "error"; exit 1 ;;
  esac
  if ! [[ "${WG_PORT}" =~ ^[0-9]+$ ]] || [[ "${WG_PORT}" -lt 1 ]] || [[ "${WG_PORT}" -gt 65535 ]]; then
    print_message "[!] WireGuard port must be between 1 and 65535" "error"
    exit 1
  fi
  curl -fsk -H "${AUTH}" "${EP_LOCAL}/api2/json/nodes/${NODE}/storage?content=rootdir" \
    | python3 -c 'import json,sys; sys.exit(0 if any(s["storage"]==sys.argv[1] and s.get("active") for s in json.load(sys.stdin)["data"]) else 1)' "$STORAGE" \
    || { print_message "[!] Storage ${STORAGE} is not available for container root filesystems" "error"; exit 1; }
  if pvesh get /cluster/resources --type vm --output-format json | python3 -c 'import json,sys; sys.exit(0 if any(int(v["vmid"])==int(sys.argv[1]) for v in json.load(sys.stdin)) else 1)' "$VMID"; then
    print_message "[!] VMID ${VMID} is occupied; refusing to modify its state" "error"
    exit 1
  fi
  # ---- 3. SDN bootstrap --------------------------------------------------------
  local NODE_COUNT ZONE_TYPE PEERS
  NODE_COUNT=$(echo "${CLUSTER_JSON}" | _json 'sum(1 for n in d["data"] if n["type"]=="node")')
  ZONE_TYPE=simple
  PEERS=""
  NAT_VNET_TAG=""
  if [[ "${NODE_COUNT}" -gt 1 ]]; then
    ZONE_TYPE=vxlan
    PEERS=$(echo "${CLUSTER_JSON}" | _json '",".join(n["ip"] for n in d["data"] if n["type"]=="node")')
    NAT_VNET_TAG=100000
  fi
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    migration_begin_sdn_stage
  fi
  print_message "[+] Creating SDN zone 'ludus' (${ZONE_TYPE}) ..." "info"
  if ! curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/sdn/zones/ludus" | grep -q '"zone"'; then
    # shellcheck disable=SC2086
    curl -sk -H "${AUTH}" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/zones" \
      --data-urlencode "zone=ludus" --data-urlencode "type=${ZONE_TYPE}" \
      --data-urlencode "ipam=pve" ${PEERS:+--data-urlencode "peers=${PEERS}"} >/dev/null
  fi
  if ! curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat" | grep -q '"vnet"'; then
    local VNET_CREATE_ARGS=(--data-urlencode "vnet=ludusnat" --data-urlencode "zone=ludus")
    [[ -n "${NAT_VNET_TAG}" ]] && VNET_CREATE_ARGS+=(--data-urlencode "tag=${NAT_VNET_TAG}")
    curl -sk -H "${AUTH}" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/vnets" \
      "${VNET_CREATE_ARGS[@]}" >/dev/null
  else
    local VNET_UPDATE_ARGS=(--data-urlencode "vlanaware=0")
    [[ -n "${NAT_VNET_TAG}" ]] && VNET_UPDATE_ARGS+=(--data-urlencode "tag=${NAT_VNET_TAG}")
    curl -sk -H "${AUTH}" -X PUT "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat" \
      "${VNET_UPDATE_ARGS[@]}" >/dev/null 2>&1 || true
  fi
  local SUBNET_ID
  SUBNET_ID=$(curl -fsk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat/subnets" | _json 'next((s["subnet"] for s in d["data"] if s["subnet"].endswith("-192.0.2.0-24")), "")')
  if [[ -n $SUBNET_ID ]]; then
    curl -fsk -H "${AUTH}" -X PUT "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat/subnets/${SUBNET_ID}" \
      --data-urlencode "gateway=${LUDUS_NAT_GATEWAY}" --data-urlencode "snat=1" >/dev/null
  else
    curl -fsk -H "${AUTH}" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat/subnets" \
      --data-urlencode "subnet=192.0.2.0/24" --data-urlencode "type=subnet" \
      --data-urlencode "gateway=${LUDUS_NAT_GATEWAY}" --data-urlencode "snat=1" >/dev/null
  fi
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    migration_stage_range_networks
  fi
  sysctl -w net.ipv4.ip_forward=1 >/dev/null
  install -d -m 0755 /etc/sysctl.d
  printf "net.ipv4.ip_forward=1\n" > /etc/sysctl.d/99-ludus-ip-forward.conf
  if [[ -f /etc/network/interfaces ]] \
    && ! grep -Eq '^[[:space:]]*(source[[:space:]]+/etc/network/interfaces\.d/(\*|sdn)|source-directory[[:space:]]+/etc/network/interfaces\.d/?)([[:space:]]|$)' /etc/network/interfaces; then
    printf "\nsource /etc/network/interfaces.d/sdn\n" >> /etc/network/interfaces
  fi
  curl -fsk -H "${AUTH}" -X PUT "${EP_LOCAL}/api2/json/cluster/sdn" >/dev/null
  local _i SDN_OK=0 SDN_ADDRESSES
  for _i in $(seq 1 60); do
    if SDN_ADDRESSES=$(ip -j -4 address show dev ludusnat 2>/dev/null) \
      && printf '%s\n' "$SDN_ADDRESSES" | python3 -c 'import json,sys; sys.exit(0 if any(a.get("local")==sys.argv[1] for n in json.load(sys.stdin) for a in n.get("addr_info",[])) else 1)' "$LUDUS_NAT_GATEWAY"; then
      SDN_OK=1
      break
    fi
    sleep 1
  done
  if [[ "${SDN_OK}" != "1" ]]; then
    print_message "[!] SDN apply did not activate ludusnat within 60s. Check: pvesh get /cluster/sdn" "error"
    exit 1
  fi
  print_message "[+] SDN zone/vnet applied" "ok"
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    migration_finish_sdn_stage
  fi
  # ---- 5. Create container -----------------------------------------------------
  local ETH0_CFG PROXMOX_INVALID_CERT
  ETH0_CFG="ip=${ETH0_IP}"
  [[ -n "${ETH0_GW:-}" ]] && ETH0_CFG="${ETH0_CFG},gw=${ETH0_GW}"
  [[ -n "${LXC_VLAN_TAG:-}" ]] && ETH0_CFG="${ETH0_CFG},tag=${LXC_VLAN_TAG}"
  local -a PCT_CREATE_ARGS=(
    --hostname "${LXC_HOSTNAME}"
    --unprivileged 1
    --features nesting=1,keyctl=1
    --cores 4
    --memory 4096
    --swap 512
    --rootfs "${STORAGE}:${LXC_ROOTFS_SIZE}"
    --net0 "name=eth0,bridge=${LXC_BRIDGE},${ETH0_CFG},firewall=0"
    --net1 "name=eth1,bridge=ludusnat,ip=${LUDUS_NAT_IP}/24"
    --onboot 1
    --startup order=99
  )
  [[ -n "${LXC_NAMESERVER:-}" ]] && PCT_CREATE_ARGS+=(--nameserver "${LXC_NAMESERVER}")
  print_message "[+] Creating LXC ${VMID} (rootfs on ${STORAGE}, eth0 on ${LXC_BRIDGE}) ..." "info"
  # lxc-usernsexec must traverse the rootfs directories created by pct.
  (umask 022; pct create "${VMID}" "local:vztmpl/${TMPL_NAME}" "${PCT_CREATE_ARGS[@]}") \
    || { print_message "[!] pct create failed; check the selected storage and /etc/vzdump.conf tmpdir" "error"; exit 1; }
  [[ ${MIGRATE_HOST:-0} != 1 ]] || touch "$MIGRATION_DIR/container-created"
  cat >> "/etc/pve/lxc/${VMID}.conf" <<EOF
lxc.cgroup2.devices.allow: c 10:200 rwm
lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file
EOF

  # Proxmox VE 9 only supports pct push for running containers. Start the
  # container, then stop Ludus before installing its configuration and local
  # artifacts so bootstrap cannot race partially-staged inputs.
  pct start "${VMID}" \
    || { print_message "[!] Failed to start LXC ${VMID}" "error"; exit 1; }
  sleep 2
  # The appliance uses ifupdown2. systemd-networkd sees its interfaces as
  # unmanaged and otherwise delays every boot for its full online timeout.
  pct exec "${VMID}" -- systemctl mask --now systemd-networkd-wait-online.service
  pct exec "${VMID}" -- systemctl stop ludus-admin ludus \
    || { print_message "[!] Failed to stop Ludus before artifact staging" "error"; exit 1; }


  # ---- 6. Configure ------------------------------------------------------------
  local CFG AIRGAPPED_CONFIG
  CFG=$(mktemp /tmp/ludus-config.XXXXXXXX.yml)
  AIRGAPPED_CONFIG=false
  [[ "${AIRGAPPED_INSTALL}" == "1" ]] && AIRGAPPED_CONFIG=true
  PROXMOX_INVALID_CERT=true
  [[ "${VERIFY_PROXMOX_TLS:-0}" == "1" ]] && PROXMOX_INVALID_CERT=false
  cat > "${CFG}" <<EOF
proxmox_endpoints:
$(for e in ${ENDPOINTS}; do echo "  - ${e}"; done)
proxmox_token_id: ${TOKEN_ID}
proxmox_token_secret: ${TOKEN_SECRET}
proxmox_node: ${NODE}
proxmox_user_realm: pve
proxmox_vm_storage_pool: ${VM_STORAGE}
proxmox_vm_storage_format: ${VM_STORAGE_FORMAT}
proxmox_iso_storage_pool: ${ISO_STORAGE}
proxmox_invalid_cert: ${PROXMOX_INVALID_CERT}
airgapped_install: ${AIRGAPPED_CONFIG}
ludus_nat_interface: ludusnat
ludus_nat_ip: ${LUDUS_NAT_IP}
ludus_nat_gateway: ${LUDUS_NAT_GATEWAY}
wireguard_endpoint: ${WG_EP}
wireguard_port: ${WG_PORT}
sdn_zone: ludus
license_key: ${LICENSE}
expose_admin_port: false
port: ${LUDUS_API_PORT}
admin_port: ${LUDUS_ADMIN_PORT}
data_directory: /opt/ludus/db
tls_cert_file: /opt/ludus/tls/server.crt
tls_key_file: /opt/ludus/tls/server.key
database_encryption_key: $(python3 -c 'import secrets; print(secrets.token_urlsafe(24))')
EOF
  chmod 0600 "${CFG}"
  pct push "${VMID}" "${CFG}" /opt/ludus/config.yml --perms 0600 --user 1001 --group 1001
  if [[ -n "${ENTERPRISE_PLUGIN:-}" ]]; then
    pct push "${VMID}" "${ENTERPRISE_PLUGIN}" /opt/ludus/plugins/enterprise/ludus-enterprise.so \
      --perms 0644 --user 1001 --group 1001
    pct push "${VMID}" "${ENTERPRISE_PLUGIN}" /opt/ludus/plugins/enterprise/admin/ludus-enterprise.so \
      --perms 0644 --user 0 --group 0
  fi
  if [[ -n "${LICENSE_FILE:-}" ]]; then
    pct push "${VMID}" "${LICENSE_FILE}" /opt/ludus/license.lic --perms 0640 --user 1001 --group 1001
  fi
  if [[ -n "${CA_CERTIFICATE:-}" ]]; then
    pct push "${VMID}" "${CA_CERTIFICATE}" /opt/ludus/install/injected-ca-certificate.crt \
      --perms 0644 --user 0 --group 0
    pct push "${VMID}" "${CA_CERTIFICATE}" /usr/local/share/ca-certificates/ludus-injected-ca.crt \
      --perms 0644 --user 0 --group 0
    pct exec "${VMID}" -- mkdir -p \
      /opt/ludus/packer/debian11/http \
      /opt/ludus/packer/debian12/http \
      /opt/ludus/packer/debian13/http \
      /opt/ludus/packer/kali/http
    pct push "${VMID}" "${CA_CERTIFICATE}" /opt/ludus/packer/debian11/http/ludus-injected-ca.crt \
      --perms 0644 --user 0 --group 0
    pct push "${VMID}" "${CA_CERTIFICATE}" /opt/ludus/packer/debian12/http/ludus-injected-ca.crt \
      --perms 0644 --user 0 --group 0
    pct push "${VMID}" "${CA_CERTIFICATE}" /opt/ludus/packer/debian13/http/ludus-injected-ca.crt \
      --perms 0644 --user 0 --group 0
    pct push "${VMID}" "${CA_CERTIFICATE}" /opt/ludus/packer/kali/http/ludus-injected-ca.crt \
      --perms 0644 --user 0 --group 0
  fi
  rm -f "${CFG}"

  if [[ -n "${CA_CERTIFICATE:-}" ]]; then
    pct exec "${VMID}" -- update-ca-certificates
  fi

  # Keep the old host services live while the appliance is downloaded, created,
  # bootstrapped, and configured. The final state export begins only after the
  # candidate is ready to accept it, keeping the client-visible cutover short.
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    pct exec "${VMID}" -- systemctl restart ludus-admin ludus
    print_message "[+] Staging the LXC runtime while the existing server remains online (up to 5m) ..." "info"
    for _i in $(seq 1 60); do
      if pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete 2>/dev/null \
        && pct exec "${VMID}" -- systemctl is-active --quiet ludus ludus-admin \
        && pct exec "${VMID}" -- curl -fkSs --max-time 5 "https://127.0.0.1:${LUDUS_API_PORT}/api/health" >/dev/null 2>&1; then
        break
      fi
      sleep 5
    done
    pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete \
      || { print_message "[!] Bootstrap did not complete; see /opt/ludus/install/install.log inside the container" "error"; exit 1; }
    pct exec "${VMID}" -- systemctl is-active --quiet ludus ludus-admin \
      || { print_message "[!] Staged Ludus API services did not become healthy" "error"; exit 1; }
    print_message "[+] Importing a live state snapshot while the existing server remains online ..." "info"
    migration_stage_candidate_state
    migration_cutover
  fi

  if [[ -n "${IMPORT_DB:-}" ]]; then
    print_message "[+] Importing DB/WireGuard state from ${IMPORT_DB} ..." "info"
    pct exec "${VMID}" -- systemctl stop ludus-admin ludus
    pct push "${VMID}" "${IMPORT_DB}" /tmp/ludus-import.tar.gz
    pct exec "${VMID}" -- /opt/ludus/ludus-server --import-state /tmp/ludus-import.tar.gz
    pct exec "${VMID}" -- rm /tmp/ludus-import.tar.gz
  fi

  pct exec "${VMID}" -- systemctl restart ludus-admin ludus
  print_message "[+] Waiting for first-boot bootstrap (up to 5m) ..." "info"
  for _i in $(seq 1 60); do
    if pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete 2>/dev/null; then
      break
    fi
    sleep 5
  done
  pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete \
    || { print_message "[!] Bootstrap did not complete; see /opt/ludus/install/install.log inside the container" "error"; exit 1; }
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    migration_move_nics
  fi
  local API_READY=0
  for _i in $(seq 1 60); do
    if pct exec "${VMID}" -- systemctl is-active --quiet ludus ludus-admin \
      && pct exec "${VMID}" -- curl -fkSs --max-time 5 "https://127.0.0.1:${LUDUS_API_PORT}/api/health" >/dev/null 2>&1 \
      && pct exec "${VMID}" -- bash -c 'printf "X-API-KEY: %s\n" "$(cat /opt/ludus/install/root-api-key)" | curl -fkSs --max-time 5 --header @- "https://127.0.0.1:$1/api/v2/user/all"' _ "${LUDUS_ADMIN_PORT}" >/dev/null 2>&1; then
      API_READY=1
      break
    fi
    sleep 2
  done
  [[ $API_READY == 1 ]] || { print_message "[!] Ludus API services did not become healthy" "error"; exit 1; }
  if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
    migration_forwarding start
    curl -fkSs --max-time 10 "https://127.0.0.1:${LUDUS_API_PORT}/api/health" >/dev/null \
      || { print_message "[!] Original API endpoint is not reachable after forwarding" "error"; exit 1; }
    migration_api_bridge drain
    migration_commit
  fi

  python3 - "${VMID}" "${LUDUS_VERSION}" <<'PY'
import json,pathlib,sys
path=pathlib.Path('/etc/ludus-lxc.json')
path.write_text(json.dumps({'vmid':int(sys.argv[1]),'version':sys.argv[2]},indent=2)+'\n')
path.chmod(0o600)
PY

  # Install only after migration commits: rollback keeps the legacy helper intact.
  local STATUS_HELPER
  STATUS_HELPER=$(mktemp)
  pct pull "${VMID}" /usr/local/bin/ludus-install-status "${STATUS_HELPER}"
  if [[ -f /usr/local/bin/ludus-install-status && ! -e /usr/local/bin/ludus-install-status.pre-lxc ]]; then
    cp -p /usr/local/bin/ludus-install-status /usr/local/bin/ludus-install-status.pre-lxc
  fi
  install -m 0755 "${STATUS_HELPER}" /usr/local/bin/ludus-install-status
  rm -f "${STATUS_HELPER}"

  # ---- 7. Output ---------------------------------------------------------------
  if pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete; then
    local LXC_IP
    LXC_IP=$(pct exec "${VMID}" -- hostname -I | awk '{print $1}')
    echo
    print_message "[+] Ludus is running in LXC ${VMID}" "ok"
    if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
      print_message "    API:        existing endpoint preserved" "info"
    else
      print_message "    API:        https://${LXC_IP}:${LUDUS_API_PORT}" "info"
    fi
    print_message "    Admin API:  https://127.0.0.1:${LUDUS_ADMIN_PORT} inside LXC ${VMID} only; use the public API for CLI commands" "info"
    print_message "    Status:     ludus-install-status (automatically enters LXC ${VMID})" "info"
    print_message "    WireGuard:  ${WG_EP}:${WG_PORT}" "info"
    echo
    if [[ ${MIGRATE_HOST:-0} == 1 ]]; then
      print_message "[+] Existing clients, API keys, and WireGuard configurations remain valid" "info"
    else
      print_message "[+] Next: install ludus-client and run 'ludus user add <name>'" "info"
    fi
  else
    print_message "[!] Bootstrap did not complete. Last 50 log lines:" "error"
    pct exec "${VMID}" -- tail -50 /opt/ludus/install/install.log 2>/dev/null || true
    exit 1
  fi
}


#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  main
#   DESCRIPTION:  Does everything
#    PARAMETERS:  1 = prefix
#       RETURNS:  0 = All good
#                 1 = Something went wrong
#-------------------------------------------------------------------------------
main() {
  local prefix
  local tmpdir
  local tmpdir_rcode
  local ludus_arch
  local ludus_arch_rcode
  local ludus_os
  local ludus_os_rcode
  local ludus_base_url
  local ludus_url
  local ludus_file
  local ludus_checksum_file
  local ludus_bin_name
  local ludus_dl_ext
  local download_file_rcode
  local download_checksum_file_rcode
  local checksum_check_rcode
  local install_file_rcode
  local create_prefix_rcode
  local latest_tag_rcode

  if ! command_exists grep; then
    echo "Error: 'grep' not found in path. Please install it."
    exit 1
  fi

  if [[ -z "${LUDUS_VERSION:-}" && -n "${TEMPLATE_FILE:-}" ]]; then
    LUDUS_VERSION=$(infer_ludus_version_from_template "${TEMPLATE_FILE}" || true)
  fi

  if [[ "${SERVER_ONLY:-0}" != "1" || -z "${LUDUS_VERSION:-}" ]]; then
    LATEST_TAG=$(fetch_latest_tag)
    latest_tag_rcode="${?}"
    if [[ "${latest_tag_rcode}" == "20" ]]; then
      echo "Error: Neither curl nor wget is available. Please install one of them."
      exit 1
    elif [[ "${latest_tag_rcode}" != "0" ]]; then
      echo "Error: Unable to determine latest Ludus release tag."
      exit 1
    fi
  fi

  ludus_bin_name="ludus-client"
  prefix="${1}"

  print_banner
  print_message "[+] Client install prefix set to ${prefix}" "info"
  
  tmpdir="$(make_tempdir "${ludus_bin_name}")"
  tmpdir_rcode="${?}"
  if [[ "${tmpdir_rcode}" == "0" ]]; then
    print_message "[+] Created temp dir at ${tmpdir}" "info"
  elif [[ "${tmpdir_rcode}" == "1" ]]; then
    print_message "[+] Failed to create temp dir at ${tmpdir}" "error"
  else
    print_message "[+] 'mktemp' not found in path. Is it installed?" "error"
    exit 1
  fi

  ludus_arch="$(determine_arch)"
  ludus_arch_rcode="${?}"
  if [[ "${ludus_arch_rcode}" == "0" ]]; then
    print_message "[+] Architecture detected as ${ludus_arch}" "info"
  elif [[ "${ludus_arch_rcode}" == "1" ]]; then
    print_message "[+] Architecture not detected" "error"
    exit 1
  else
    print_message "[+] 'uname' not found in path. Is it installed?" "error"
    exit 1
  fi

  ludus_os="$(determine_os)"
  ludus_os_rcode="${?}"
  if [[ "${ludus_os_rcode}" == "0" ]]; then
    print_message "[+] OS detected as ${ludus_os}" "info"
  elif [[ "${ludus_os_rcode}" == "1" ]]; then
    print_message "[+] OS not detected" "error"
    exit 1
  else
    print_message "[+] 'uname' not found in path. Is it installed?" "error"
    exit 1
  fi

  case "${ludus_os}" in
     "Darwin" ) ludus_os="macOS";;
     "Linux" ) ludus_os="linux";;
    *"BusyBox"* )
        ludus_os="linux"
        ;;
    "CYGWIN"* ) ludus_os="windows";
                ludus_dl_ext="exe";
                print_message "[+] Cygwin is currently unsupported." "error";
                exit 1;;
  esac

  case "${ludus_arch}" in
     "x86_64" ) ludus_arch="amd64";;
      "amd64" ) ludus_arch="amd64";;
    "aarch64" ) ludus_arch="arm64";;
      "arm64" ) ludus_arch="arm64";;
     "armv7l" ) ludus_arch="arm";;
     "armv8l" ) ludus_arch="arm";;
     "armv9l" ) ludus_arch="arm";;
       "i686" ) ludus_arch="386";;
            * ) ludus_arch="unknown";;
  esac

  if [[ "${SERVER_ONLY:-0}" == "1" ]]; then
    if [[ "${ludus_os}" != "linux" ]] || [[ "${ludus_arch}" != "amd64" ]] || ! command_exists pveversion; then
      print_message "[!] --server-only requires an amd64 Linux Proxmox host" "error"
      exit 1
    fi
    LUDUS_VERSION="${LUDUS_VERSION:-${LATEST_TAG:-}}"
    if [[ -z "${LUDUS_VERSION}" ]]; then
      print_message "[!] Unable to determine Ludus version. Pass --version or use a template named ludus-<version>-debian13-amd64.tar.zst" "error"
      exit 1
    fi
    if lxc_host_install_exists; then
      print_message "[+] Ludus is already installed in an LXC on this host" "info"
      rm -rf "${tmpdir}"
      exit 0
    fi
    if legacy_host_install_exists && [[ ${MIGRATE_HOST:-0} != 1 ]]; then
      MIGRATE_HOST=1
      print_message "[+] Existing Ludus 2.x host install detected; selecting the transparent LXC upgrade" "info"
    fi
    rm -rf "${tmpdir}"
    print_message "[+] Installing Ludus server only (LXC mode)" "info"
    run_ludus_server_install
    exit 0
  fi

  ludus_base_url="https://gitlab.com/api/v4/projects/$PROJECT_ID/packages/generic/ludus/$LATEST_TAG"
  ludus_file="${ludus_bin_name}_${ludus_os}-${ludus_arch}-${LATEST_TAG}"
  ludus_checksum_file="ludus_${LATEST_TAG}_checksums.txt"
  ludus_url="${ludus_base_url}/${ludus_file}"
  ludus_checksum_url="${ludus_base_url}/${ludus_checksum_file}"
  download_file "${ludus_url}" "${tmpdir}" "${ludus_file}"
  download_file_rcode="${?}"
  if [[ "${download_file_rcode}" == "0" ]]; then
    print_message "[+] Downloaded ${ludus_file} into ${tmpdir}" "info"
  elif [[ "${download_file_rcode}" == "1" ]]; then
    print_message "[+] Failed to download ${ludus_file}" "error"
    exit 1
  elif [[ "${download_file_rcode}" == "20" ]]; then
    print_message "[+] Failed to locate curl or wget" "error"
    exit 1
  else
    print_message "[+] Return code of download tool returned an unexpected value of ${download_file_rcode}" "error"
    exit 1
  fi
  download_file "${ludus_checksum_url}" "${tmpdir}" "${ludus_checksum_file}"
  download_checksum_file_rcode="${?}"
  if [[ "${download_checksum_file_rcode}" == "0" ]]; then
    print_message "[+] Downloaded ludus checksums file into ${tmpdir}" "info"
  elif [[ "${download_checksum_file_rcode}" == "1" ]]; then
    print_message "[+] Failed to download ludus checksums" "error"
    exit 1
  elif [[ "${download_checksum_file_rcode}" == "20" ]]; then
    print_message "[+] Failed to locate curl or wget" "error"
    exit 1
  else
    print_message "[+] Return code of download tool returned an unexpected value of ${download_checksum_file_rcode}" "error"
    exit 1
  fi

  # Rename the client to the way the checksum file expects
  ludus_client_non_versioned="${ludus_bin_name}_${ludus_os}-${ludus_arch}"
  mv "${tmpdir}/${ludus_file}" "${tmpdir}/${ludus_client_non_versioned}"

  checksum_check "${tmpdir}/${ludus_checksum_file}" "${tmpdir}/${ludus_client_non_versioned}" "${tmpdir}"
  checksum_check_rcode="${?}"
  if [[ "${checksum_check_rcode}" == "0" ]]; then
    print_message "[+] Checksum of ${tmpdir}/${ludus_file} verified" "ok"
  elif [[ "${checksum_check_rcode}" == "1" ]]; then
    print_message "[+] Failed to verify checksum of ${tmpdir}/${ludus_file}" "error"
    exit 1
  elif [[ "${checksum_check_rcode}" == "20" ]]; then
    print_message "[+] Failed to find tool to verify sha256 sums" "error"
    exit 1
  elif [[ "${checksum_check_rcode}" == "30" ]]; then
    print_message "[+] Failed to change into working directory ${tmpdir}" "error"
    exit 1
  elif [[ "${checksum_check_rcode}" == "31" ]]; then
    print_message "[+] Failed to change back into working directory. Are you running this in a directory you have no access to?" "error"
    exit 1
  else
    print_message "[+] Unknown return code returned while checking checksum of ${tmpdir}/${ludus_file}. Returned ${checksum_check_rcode}" "error"
    exit 1
  fi

  if [[ ! -d "${prefix}" ]]; then
    create_prefix "${prefix}"
    create_prefix_rcode="${?}"
    if [[ "${create_prefix_rcode}" == "0" ]]; then
      print_message "[+] Created install prefix at ${prefix}" "info"
    elif [[ "${create_prefix_rcode}" == "20" ]]; then
      print_message "[+] Failed to find mkdir in path" "error"
      exit 1
    elif [[ "${create_prefix_rcode}" == "21" ]]; then
      print_message "[+] Failed to find sudo in path" "error"
      exit 1
    else
      print_message "[+] Failed to create the install prefix: ${prefix}" "error"
      exit 1
    fi
  else
    print_message "[+] Install prefix already exists. No need to create it." "info"
  fi

  # Rename the client to just 'ludus'
  mv "${tmpdir}/${ludus_bin_name}_${ludus_os}-${ludus_arch}" "${tmpdir}/ludus"
  case "${ludus_os}" in
    "linux" ) install_file_linux "${tmpdir}/ludus" "${prefix}/";
              install_file_rcode="${?}";;
    "macOS" ) xattr -d com.apple.quarantine "${tmpdir}/ludus";
              install_file_freebsd "${tmpdir}/ludus" "${prefix}/";
              install_file_rcode="${?}";;
  esac

  if [[ "${install_file_rcode}" == "0" ]] ; then
    print_message "[+] Installed ${ludus_file} to ${prefix}/ as 'ludus'" "ok"
  elif [[ "${install_file_rcode}" == "1" ]]; then
    print_message "[+] Failed to install ${ludus_file} as 'ludus'" "error"
    exit 1
  elif [[ "${install_file_rcode}" == "20" ]]; then
    print_message "[+] Failed to locate 'install' command" "error"
    exit 1
  elif [[ "${install_file_rcode}" == "21" ]]; then
    print_message "[+] Failed to locate 'sudo' command" "error"
    exit 1
  else
    print_message "[+] Install attempt returned an unexpected value of ${install_file_rcode}" "error"
    exit 1
  fi

  print_message "[+] Ludus client installation complete" "ok"

  # Completions
  if { [[ "$SHELL" == "/bin/zsh" ]] && [[ ! -f "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions/_ludus" ]]; } || \
    { [[ "$SHELL" == "/bin/bash" ]] && \
      { [[ "${EUID}" == "0" ]] && { { command_exists pkg-config && [[ ! -f "$(pkg-config --variable=completionsdir bash-completion)/ludus" ]]; } || { ! command_exists pkg-config && [[ ! -f "/usr/share/bash-completion/completions/ludus" ]]; }; } } || \
      { [[ "${EUID}" != "0" ]] && [[ ! -f "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/ludus" ]]; }; }; then

    if [[ "${NO_PROMPT:-0}" != "1" ]]; then
      print_message "[?] Would you like to install shell completions so tab works with the 'ludus' command?" "warn"

      if [[ "$SHELL" == "/bin/zsh" ]]; then
        print_message "[?] (y/n): " "warn"
        read -r completions_response </dev/tty
      else
        read -r -p "[?] (y/n): " completions_response </dev/tty
      fi
    else
      completions_response=n
    fi

    case "${completions_response}" in
      y|Y ) 
        print_message "[+] Installing Ludus completions" "info"
        install_completions
        ;;
      n|N ) 
        print_message "[+] Skipping Ludus completions installation" "info"
        ;;
      * ) 
        print_message "[-_-] Invalid response. Skipping Ludus completions installation" "error"
        ;;
    esac
  else
    print_message "[+] Shell completions already installed" "info"
  fi
  
  # ---- Server install (LXC mode) ---------------------------------------------
  # Only offered on Proxmox VE hosts (linux/amd64 with pveversion).
  if [[ "${ludus_os}" == "linux" ]] && [[ "${ludus_arch}" == "amd64" ]] && command_exists pveversion && lxc_host_install_exists; then
    print_message "[+] Ludus is already installed in an LXC on this host" "info"
  elif [[ "${ludus_os}" == "linux" ]] && [[ "${ludus_arch}" == "amd64" ]] && command_exists pveversion; then
    local server_action="Install"
    if legacy_host_install_exists; then
      server_action="Upgrade the existing Ludus server to the LXC runtime"
    fi
    if [[ "${NO_PROMPT:-0}" == "1" ]]; then
      install_server="y"
    else
      print_message "[?] Proxmox detected. ${server_action}?" "warn"
      if [[ "$SHELL" == "/bin/zsh" ]]; then
        print_message "[?] (y/n): " "warn"
        read -r install_server </dev/tty
      else
        read -r -p "[?] (y/n): " install_server </dev/tty
      fi
    fi
    case "${install_server}" in
      y|Y )
        if legacy_host_install_exists; then
          MIGRATE_HOST=1
          print_message "[+] Upgrading Ludus to the LXC runtime while preserving its endpoints" "info"
        else
          print_message "[+] Installing Ludus server (LXC mode)" "info"
        fi
        LUDUS_VERSION="${LUDUS_VERSION:-${LATEST_TAG}}"
        run_ludus_server_install
        ;;
      n|N )
        print_message "[+] Skipping Ludus server installation" "info"
        ;;
      * )
        print_message "[-_-] Invalid response. Skipping Ludus server installation" "error"
        ;;
    esac
  fi

  exit 0
}

#-------------------------------------------------------------------------------
#  ARGUMENT PARSING
#-------------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help      ) print_help; exit 0;;
    -p|--prefix    ) INSTALL_PREFIX="$2"; shift 2;;
    # ---- server (LXC) install flags ----
    --server-only  ) SERVER_ONLY=1; shift;;
    --migrate-host ) MIGRATE_HOST=1; SERVER_ONLY=1; shift;;
    --version      ) LUDUS_VERSION="$2"; shift 2;;
    --template-file) TEMPLATE_FILE="$2"; SERVER_ONLY=1; shift 2;;
    --airgapped   ) AIRGAPPED_INSTALL=1; SERVER_ONLY=1; shift;;
    --vm-storage  ) VM_STORAGE="$2"; shift 2;;
    --vm-storage-format) VM_STORAGE_FORMAT="$2"; shift 2;;
    --iso-storage ) ISO_STORAGE="$2"; shift 2;;
    --iso-directory) ISO_DIRECTORY="$2"; AIRGAPPED_INSTALL=1; SERVER_ONLY=1; shift 2;;
    --ca-certificate) CA_CERTIFICATE="$2"; shift 2;;
    --enterprise-plugin) ENTERPRISE_PLUGIN="$2"; shift 2;;
    --license-file ) LICENSE_FILE="$2"; shift 2;;
    --checksum-file) CHECKSUM_FILE="$2"; shift 2;;
    --checksum-signature) CHECKSUM_SIGNATURE="$2"; shift 2;;
    --checksum-public-key) CHECKSUM_PUBLIC_KEY="$2"; shift 2;;
    --skip-verification) SKIP_VERIFICATION=1; shift;;
    --token-id     ) TOKEN_ID="$2"; shift 2;;
    --token-secret ) TOKEN_SECRET="$2"; shift 2;;
    --no-prompt    ) NO_PROMPT=1; shift;;
    --vmid         ) VMID="$2"; shift 2;;
    --hostname     ) LXC_HOSTNAME="$2"; shift 2;;
    --storage      ) STORAGE="$2"; shift 2;;
    --rootfs-size  ) LXC_ROOTFS_SIZE="$2"; shift 2;;
    --bridge       ) LXC_BRIDGE="$2"; shift 2;;
    --vlan-tag     ) LXC_VLAN_TAG="$2"; shift 2;;
    --ip           ) ETH0_IP="$2"; shift 2;;
    --gw           ) ETH0_GW="$2"; shift 2;;
    --nameserver   ) LXC_NAMESERVER="$2"; shift 2;;
    --endpoints    ) ENDPOINTS="$2"; shift 2;;
    --verify-proxmox-tls) VERIFY_PROXMOX_TLS=1; shift;;
    --import-db    ) IMPORT_DB="$2"; shift 2;;
    --wg-endpoint  ) WG_EP="$2"; shift 2;;
    --wg-port      ) WG_PORT="$2"; shift 2;;
    --license      ) LICENSE="$2"; shift 2;;
    *              ) print_message "Unknown option $1" "warn"; shift;;
  esac
done

#-------------------------------------------------------------------------------
# CALL MAIN
#-------------------------------------------------------------------------------
main "${INSTALL_PREFIX}"
