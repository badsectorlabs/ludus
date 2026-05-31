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
  --version VER          Ludus version to install (default: latest release tag)
  --template-file PATH   Use a local LXC template tarball (air-gapped install)
  --token-id ID          Proxmox API token ID (e.g. root@pam!ludus)
  --token-secret SECRET  Proxmox API token secret
  --no-prompt            Non-interactive; use defaults / supplied flags
  --vmid N               VMID for the Ludus LXC (default: cluster nextid)
  --storage NAME         Storage for the LXC rootfs (default: local-lvm)
  --ip CIDR|dhcp         LXC eth0 address (default: dhcp)
  --gw IP                LXC eth0 gateway (required if --ip is not dhcp)
  --endpoints \"URL ...\"  Space-separated Proxmox API endpoints
  --wg-endpoint HOST     WireGuard endpoint clients will dial
  --license KEY          License key (default: community)
  --import-db TARBALL    Import an existing ludus DB/WireGuard tarball

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

  if [[ -z "${LANGUAGE+x}" ]]; then
    export LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8 LC_CTYPE=en_US.UTF-8
  fi

  # Tiny JSON helper — avoids a jq dependency on the PVE host
  _json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

  local NODE
  NODE=$(hostname)

  # ---- 1. Token ----------------------------------------------------------------
  if [[ -z "${TOKEN_ID:-}" || -z "${TOKEN_SECRET:-}" ]]; then
    if [[ "${EUID}" -eq 0 ]]; then
      print_message "[+] Generating API token root@pam!ludus ..." "info"
      local TOK_JSON
      TOK_JSON=$(pveum user token add root@pam ludus --privsep 0 --output-format json 2>/dev/null || true)
      if [[ -z "${TOK_JSON}" ]]; then
        print_message "[!] Token 'root@pam!ludus' already exists." "warn"
        local yn
        read -r -p "[?] Delete and recreate it? [y/N] " yn </dev/tty
        if [[ "${yn}" =~ ^[Yy]$ ]]; then
          pveum user token remove root@pam ludus
          TOK_JSON=$(pveum user token add root@pam ludus --privsep 0 --output-format json)
        else
          read -r -p "[?] Enter existing token secret: " TOKEN_SECRET </dev/tty
          TOKEN_ID="root@pam!ludus"
        fi
      fi
      if [[ -n "${TOK_JSON}" ]]; then
        TOKEN_ID=$(echo "${TOK_JSON}"     | _json 'd["full-tokenid"]')
        TOKEN_SECRET=$(echo "${TOK_JSON}" | _json 'd["value"]')
      fi
    else
      read -r -p  "[?] Proxmox API token ID (e.g. root@pam!ludus): " TOKEN_ID </dev/tty
      read -r -sp "[?] Proxmox API token secret: " TOKEN_SECRET </dev/tty; echo
    fi
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

    DEFAULT_VMID=$(curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/nextid" | _json 'd["data"]')
    read -r -p "[?] LXC VMID [${DEFAULT_VMID}]: " VMID </dev/tty
    VMID=${VMID:-${DEFAULT_VMID}}

    DEFAULT_STORAGE=$(curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/nodes/${NODE}/storage?content=rootdir" | _json 'd["data"][0]["storage"]' 2>/dev/null || echo "local-lvm")
    read -r -p "[?] LXC rootfs storage [${DEFAULT_STORAGE}]: " STORAGE </dev/tty
    STORAGE=${STORAGE:-${DEFAULT_STORAGE}}

    read -r -p "[?] LXC eth0 IP (CIDR, or 'dhcp') [dhcp]: " ETH0_IP </dev/tty
    ETH0_IP=${ETH0_IP:-dhcp}
    if [[ "${ETH0_IP}" != "dhcp" ]]; then
      read -r -p "[?] LXC eth0 gateway: " ETH0_GW </dev/tty
    fi

    read -r -p "[?] WireGuard endpoint (IP/host clients dial) [${ETH0_IP%%/*}]: " WG_EP </dev/tty
    WG_EP=${WG_EP:-${ETH0_IP%%/*}}

    read -r -p "[?] VM storage pool [local]: " VM_STORAGE </dev/tty
    VM_STORAGE=${VM_STORAGE:-local}
    read -r -p "[?] ISO storage pool [local]: " ISO_STORAGE </dev/tty
    ISO_STORAGE=${ISO_STORAGE:-local}
    read -r -p "[?] License key [community]: " LICENSE </dev/tty
    LICENSE=${LICENSE:-community}
  else
    ENDPOINTS=${ENDPOINTS:-${DEFAULT_EPS}}
    VMID=${VMID:-$(curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/nextid" | _json 'd["data"]')}
    STORAGE=${STORAGE:-local-lvm}
    ETH0_IP=${ETH0_IP:-dhcp}
    WG_EP=${WG_EP:-auto}
    VM_STORAGE=${VM_STORAGE:-local}
    ISO_STORAGE=${ISO_STORAGE:-local}
    LICENSE=${LICENSE:-community}
  fi

  # ---- 3. SDN bootstrap --------------------------------------------------------
  local NODE_COUNT ZONE_TYPE PEERS
  NODE_COUNT=$(echo "${CLUSTER_JSON}" | _json 'sum(1 for n in d["data"] if n["type"]=="node")')
  ZONE_TYPE=simple
  PEERS=""
  if [[ "${NODE_COUNT}" -gt 1 ]]; then
    ZONE_TYPE=vxlan
    PEERS=$(echo "${CLUSTER_JSON}" | _json '",".join(n["ip"] for n in d["data"] if n["type"]=="node")')
  fi
  print_message "[+] Creating SDN zone 'ludus' (${ZONE_TYPE}) ..." "info"
  if ! curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/sdn/zones/ludus" | grep -q '"zone"'; then
    # shellcheck disable=SC2086
    curl -sk -H "${AUTH}" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/zones" \
      --data-urlencode "zone=ludus" --data-urlencode "type=${ZONE_TYPE}" \
      --data-urlencode "ipam=pve" ${PEERS:+--data-urlencode "peers=${PEERS}"} >/dev/null
  fi
  if ! curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat" | grep -q '"vnet"'; then
    curl -sk -H "${AUTH}" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/vnets" \
      --data-urlencode "vnet=ludusnat" --data-urlencode "zone=ludus" \
      --data-urlencode "vlanaware=1" >/dev/null
  fi
  curl -sk -H "${AUTH}" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat/subnets" \
    --data-urlencode "subnet=192.0.2.0/24" --data-urlencode "type=subnet" \
    --data-urlencode "gateway=192.0.2.254" --data-urlencode "snat=1" >/dev/null 2>&1 || true
  curl -sk -H "${AUTH}" -X PUT "${EP_LOCAL}/api2/json/cluster/sdn" >/dev/null
  local _i
  for _i in $(seq 1 30); do
    curl -sk -H "${AUTH}" "${EP_LOCAL}/api2/json/cluster/sdn" | grep -q '"state":"ok"' && break
    sleep 1
  done
  print_message "[+] SDN zone/vnet applied" "ok"

  # ---- 4. Template -------------------------------------------------------------
  local TMPL_NAME TMPL_CACHE R2_BASE
  TMPL_NAME="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
  TMPL_CACHE="/var/lib/vz/template/cache/${TMPL_NAME}"
  if [[ -n "${TEMPLATE_FILE:-}" ]]; then
    print_message "[+] Using local template ${TEMPLATE_FILE}" "info"
    cp "${TEMPLATE_FILE}" "${TMPL_CACHE}"
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

  # ---- 5. Create container -----------------------------------------------------
  local ETH0_CFG
  ETH0_CFG="ip=${ETH0_IP}"
  [[ -n "${ETH0_GW:-}" ]] && ETH0_CFG="${ETH0_CFG},gw=${ETH0_GW}"
  print_message "[+] Creating LXC ${VMID} (rootfs on ${STORAGE}) ..." "info"
  pct create "${VMID}" "local:vztmpl/${TMPL_NAME}" \
    --hostname ludus --unprivileged 1 --features nesting=1,keyctl=1 \
    --cores 4 --memory 4096 --swap 512 --rootfs "${STORAGE}:20" \
    --net0 "name=eth0,bridge=vmbr0,${ETH0_CFG},firewall=0" \
    --net1 "name=eth1,bridge=ludusnat,ip=192.0.2.253/24" \
    --onboot 1 --startup order=99
  cat >> "/etc/pve/lxc/${VMID}.conf" <<EOF
lxc.cgroup2.devices.allow: c 10:200 rwm
lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file
EOF

  # ---- 6. Configure ------------------------------------------------------------
  local CFG
  CFG=/tmp/ludus-config.$$.yml
  cat > "${CFG}" <<EOF
proxmox_endpoints:
$(for e in ${ENDPOINTS}; do echo "  - ${e}"; done)
proxmox_token_id: ${TOKEN_ID}
proxmox_token_secret: ${TOKEN_SECRET}
proxmox_node: ${NODE}
proxmox_user_realm: pve
proxmox_vm_storage_pool: ${VM_STORAGE}
proxmox_vm_storage_format: qcow2
proxmox_iso_storage_pool: ${ISO_STORAGE}
proxmox_invalid_cert: true
ludus_nat_interface: ludusnat
ludus_nat_ip: 192.0.2.253
ludus_nat_gateway: 192.0.2.254
wireguard_endpoint: ${WG_EP}
wireguard_port: 51820
sdn_zone: ludus
license_key: ${LICENSE}
expose_admin_port: false
port: 8080
admin_port: 8081
data_directory: /opt/ludus/db
database_encryption_key: $(head -c 24 /dev/urandom | base64 | head -c 32)
EOF
  chmod 0600 "${CFG}"
  pct start "${VMID}"
  sleep 5
  pct exec "${VMID}" -- mkdir -p /opt/ludus
  pct push "${VMID}" "${CFG}" /opt/ludus/config.yml --perms 0600
  rm -f "${CFG}"

  if [[ -n "${IMPORT_DB:-}" ]]; then
    print_message "[+] Importing DB/WireGuard state from ${IMPORT_DB} ..." "info"
    pct push "${VMID}" "${IMPORT_DB}" /tmp/ludus-import.tar.gz
    pct exec "${VMID}" -- tar xzf /tmp/ludus-import.tar.gz -C / --strip-components=0
    pct exec "${VMID}" -- rm /tmp/ludus-import.tar.gz
  fi

  pct exec "${VMID}" -- systemctl restart ludus
  print_message "[+] Waiting for first-boot bootstrap (up to 5m) ..." "info"
  for _i in $(seq 1 60); do
    if pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete 2>/dev/null; then
      break
    fi
    sleep 5
  done

  # ---- 7. Output ---------------------------------------------------------------
  if pct exec "${VMID}" -- test -f /opt/ludus/install/.bootstrap-complete; then
    local LXC_IP
    LXC_IP=$(pct exec "${VMID}" -- hostname -I | awk '{print $1}')
    echo
    print_message "[+] Ludus is running in LXC ${VMID}" "ok"
    print_message "    API:        https://${LXC_IP}:8080" "info"
    print_message "    Admin API:  https://${LXC_IP}:8081 (localhost-only inside LXC by default)" "info"
    print_message "    WireGuard:  ${WG_EP}:51820" "info"
    echo
    print_message "[+] Next: install ludus-client and run 'ludus user add <name>'" "info"
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

  if ! command_exists grep; then
    echo "Error: 'grep' not found in path. Please install it."
    exit 1
  fi

  if command_exists curl; then
    LATEST_TAG=$(curl -s "https://gitlab.com/api/v4/projects/$PROJECT_ID/repository/tags" | grep -o '"name":"[^"]*' | cut -d'"' -f4 | head -n1)
  elif command_exists wget; then
    LATEST_TAG=$(wget -qO- "https://gitlab.com/api/v4/projects/$PROJECT_ID/repository/tags" | grep -o '"name":"[^"]*' | cut -d'"' -f4 | head -n1)
  else
      echo "Error: Neither curl nor wget is available. Please install one of them."
      exit 1
  fi

  ludus_bin_name="ludus-client"
  ludus_base_url="https://gitlab.com/api/v4/projects/$PROJECT_ID/packages/generic/ludus/$LATEST_TAG"
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

    print_message "[?] Would you like to install shell completions so tab works with the 'ludus' command?" "warn"
    
    if [[ "$SHELL" == "/bin/zsh" ]]; then
      print_message "[?] (y/n): " "warn"
      read -r completions_response </dev/tty
    else
      read -r -p "[?] (y/n): " completions_response </dev/tty
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
  if [[ "${ludus_os}" == "linux" ]] && [[ "${ludus_arch}" == "amd64" ]] && command_exists pveversion; then
    if [[ "${NO_PROMPT:-0}" == "1" ]]; then
      install_server="y"
    else
      print_message "[?] Proxmox detected. Install the Ludus server (LXC container) on this host?" "warn"
      if [[ "$SHELL" == "/bin/zsh" ]]; then
        print_message "[?] (y/n): " "warn"
        read -r install_server </dev/tty
      else
        read -r -p "[?] (y/n): " install_server </dev/tty
      fi
    fi
    case "${install_server}" in
      y|Y )
        print_message "[+] Installing Ludus server (LXC mode)" "info"
        LUDUS_VERSION="${LUDUS_VERSION:-${LATEST_TAG}}"
        ludus_install_server
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
    --version      ) LUDUS_VERSION="$2"; shift 2;;
    --template-file) TEMPLATE_FILE="$2"; shift 2;;
    --token-id     ) TOKEN_ID="$2"; shift 2;;
    --token-secret ) TOKEN_SECRET="$2"; shift 2;;
    --no-prompt    ) NO_PROMPT=1; shift;;
    --vmid         ) VMID="$2"; shift 2;;
    --storage      ) STORAGE="$2"; shift 2;;
    --ip           ) ETH0_IP="$2"; shift 2;;
    --gw           ) ETH0_GW="$2"; shift 2;;
    --endpoints    ) ENDPOINTS="$2"; shift 2;;
    --import-db    ) IMPORT_DB="$2"; shift 2;;
    --wg-endpoint  ) WG_EP="$2"; shift 2;;
    --license      ) LICENSE="$2"; shift 2;;
    *              ) print_message "Unknown option $1" "warn"; shift;;
  esac
done

#-------------------------------------------------------------------------------
# CALL MAIN
#-------------------------------------------------------------------------------
main "${INSTALL_PREFIX}"