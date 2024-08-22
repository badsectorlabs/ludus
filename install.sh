#!/bin/bash - 
#===============================================================================
#
#          FILE: install.sh
# 
#         USAGE: curl https://ludus.cloud/install | bash
#                 OR
#                wget -qO- https://ludus.cloud.install | bash
# 
#   DESCRIPTION: Ludus server Installer Script.
#
#                This script installs the Ludus client into /usr/local/bin.
#                This script optionally installs the Ludus server on amd64 Linux hosts.
#
#       OPTIONS: -p, --prefix "${INSTALL_PREFIX}"
#                      Prefix to install the Ludus client into.  Defaults to /usr/local/bin
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
#   DESCRIPTION:  Checks if a command is avilable
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
  -p INSTALL_PREFIX
      Prefix to install the Ludus client into.  Directory must already exist.
      Default = /usr/local/bin
  
  -h
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
#       RETURNS:  0 = checkusm verified
#                 1 = checksum verification failed
#                 20 = failed to determine tool to use to check checksum
#                 30 = failed to change into or go back from working dir
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
  cd - >/dev/null 2>&1 || return 30
  
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
#          NAME:  install_completions
#   DESCRIPTION:  Installs completion filkes for bash and zsh
#    PARAMETERS:  none; must be called after install
#       RETURNS:  0 = All good
#                 1 = Something went wrong
#-------------------------------------------------------------------------------
install_completions() {
  if [[ "$SHELL" == "/bin/zsh" ]]; then
    mkdir -p "${HOME}/.zsh_completions"
    ludus completion zsh > "${HOME}/.zsh_completions/_ludus"
    print_message "[+] Installed zsh completion file" "ok"
    print_message "[+] To enable, add the following to your .zshrc:" "info"
    echo
    print_message "fpath+=${HOME}/.zsh_completions" "info"
    print_message "autoload -U compinit && compinit" "info"
    echo
  elif [[ "$SHELL" == "/bin/bash" ]]; then
    mkdir -p "${HOME}/.bash_completions"
    ludus completion bash > "${HOME}/.bash_completions/ludus"
    print_message "[+] Installed bash completion file" "ok"
    print_message "[+] To enable, add the following to your .bashrc:" "info"
    echo
    print_message "source ${HOME}/.bash_completions/ludus" "info"
    echo
  else
    print_message "[+] Unsupported shell: $SHELL" "error"
    return 1
  fi
  return 0
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
    "macOS" ) install_file_freebsd "${tmpdir}/ludus" "${prefix}/";
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
  if { [[ "$SHELL" == "/bin/zsh" ]] && [[ ! -f "${HOME}/.zsh_completions/_ludus" ]] } || { [[ "$SHELL" == "/bin/bash" ]] && [[ ! -f "${HOME}/.bash_completions/ludus" ]] }; then
    print_message "[?] Would you like to install shell completions so tab works with the 'ludus' command?" "warn"
      read -r -p "[?] (y/n): " completions_response
      case "${completions_response}" in
        "y" ) print_message "[+] Installing Ludus completions" "info";
              install_completions
              ;;
        "n" ) print_message "[+] Skipping Ludus completions installation" "info";;
          * ) print_message "[-_-] Invalid response. Skipping Ludus completions installation" "error"
              ;;
      esac
  else
    print_message "[+] Shell completions already installed" "info"
  fi
  
  if [[ "${ludus_os}" == "linux" ]] && [[ "${ludus_arch}" == "amd64" ]] && [[ ! -d /opt/ludus ]]; then 
    print_message "[?] Would you like to install the Ludus server on this host?" "warn"
    read -r -p "[?] (y/n): " install_server
    case "${install_server}" in
      "y" ) print_message "[+] Installing Ludus server" "info"
            # Download
            download_file "${ludus_base_url}/ludus-server-${LATEST_TAG}" "${tmpdir}" "ludus-server-${LATEST_TAG}"
            download_file_rcode="${?}"
            if [[ "${download_file_rcode}" == "0" ]]; then
              print_message "[+] Downloaded ludus-server-${LATEST_TAG} into ${tmpdir}" "info"
            elif [[ "${download_file_rcode}" == "1" ]]; then
              print_message "[+] Failed to download ludus-server-${LATEST_TAG}" "error"
              exit 1
            elif [[ "${download_file_rcode}" == "20" ]]; then
              print_message "[+] Failed to locate curl or wget" "error"
              exit 1
            else
              print_message "[+] Return code of download tool returned an unexpected value of ${download_file_rcode}" "error"
              exit 1
            fi
            # Move the server
            mv "${tmpdir}/ludus-server-${LATEST_TAG}" "${tmpdir}/ludus-server" 
            # Checksum check
            checksum_check "${tmpdir}/${ludus_checksum_file}" "${tmpdir}/ludus-server" "${tmpdir}"
            checksum_check_rcode="${?}"
            if [[ "${checksum_check_rcode}" == "0" ]]; then
              print_message "[+] Checksum of ${tmpdir}/ludus-server-${LATEST_TAG} verified" "ok"
            elif [[ "${checksum_check_rcode}" == "1" ]]; then
              print_message "[+] Failed to verify checksum of ${tmpdir}/ludus-server-${LATEST_TAG}" "error"
              exit 1
            elif [[ "${checksum_check_rcode}" == "20" ]]; then
              print_message "[+] Failed to find tool to verify sha256 sums" "error"
              exit 1
            elif [[ "${checksum_check_rcode}" == "30" ]]; then
              print_message "[+] Failed to change into working directory ${tmpdir}" "error"
              exit 1
            else
              print_message "[+] Unknown return code returned while checking checksum of ${tmpdir}/ludus-server-${LATEST_TAG}. Returned ${checksum_check_rcode}" "error"
              exit 1
            fi
            # Chmod
            chmod +x "${tmpdir}/ludus-server"
            # Install
            if [[ "${EUID}" == "0" ]]; then
              "${tmpdir}/ludus-server"
            else
              if command -v sudo >/dev/null 2>&1; then
                sudo "${tmpdir}/ludus-server"
              else
                print_message "[+] Failed to locate 'sudo' command" "error"
                exit 1
              fi
            fi
            ;;
      "n" ) print_message "[+] Skipping Ludus server installation" "info"
            ;;
        * ) print_message "[-_-] Invalid response. Skipping Ludus server installation" "error"
            ;;
    esac
  elif [[ "${ludus_os}" == "linux" ]] && [[ "${ludus_arch}" == "amd64" ]] && [[ -d /opt/ludus ]]; then
    print_message "[+] Ludus server already installed in /opt/ludus" "info"
    print_message "[?] Would you like to update the Ludus server on this host?" "warn"
    read -r -p "[?] (y/n): " update_server
    case "${update_server}" in
      "y" ) print_message "[+] Updating Ludus server" "info"
            # Download
            download_file "${ludus_base_url}/ludus-server-${LATEST_TAG}" "${tmpdir}" "ludus-server-${LATEST_TAG}"
            download_file_rcode="${?}"
            if [[ "${download_file_rcode}" == "0" ]]; then
              print_message "[+] Downloaded ludus-server-${LATEST_TAG} into ${tmpdir}" "info"
            elif [[ "${download_file_rcode}" == "1" ]]; then
              print_message "[+] Failed to download ludus-server-${LATEST_TAG}" "error"
              exit 1
            elif [[ "${download_file_rcode}" == "20" ]]; then
              print_message "[+] Failed to locate curl or wget" "error"
              exit 1
            else
              print_message "[+] Return code of download tool returned an unexpected value of ${download_file_rcode}" "error"
              exit 1
            fi
            # Move the server
            mv "${tmpdir}/ludus-server-${LATEST_TAG}" "${tmpdir}/ludus-server" 
            # Checksum check
            checksum_check "${tmpdir}/${ludus_checksum_file}" "${tmpdir}/ludus-server" "${tmpdir}"
            checksum_check_rcode="${?}"
            if [[ "${checksum_check_rcode}" == "0" ]]; then
              print_message "[+] Checksum of ${tmpdir}/ludus-server-${LATEST_TAG} verified" "ok"
            elif [[ "${checksum_check_rcode}" == "1" ]]; then
              print_message "[+] Failed to verify checksum of ${tmpdir}/ludus-server-${LATEST_TAG}" "error"
              exit 1
            elif [[ "${checksum_check_rcode}" == "20" ]]; then
              print_message "[+] Failed to find tool to verify sha256 sums" "error"
              exit 1
            elif [[ "${checksum_check_rcode}" == "30" ]]; then
              print_message "[+] Failed to change into working directory ${tmpdir}" "error"
              exit 1
            else
              print_message "[+] Unknown return code returned while checking checksum of ${tmpdir}/ludus-server-${LATEST_TAG}. Returned ${checksum_check_rcode}" "error"
              exit 1
            fi
            # Chmod
            chmod +x "${tmpdir}/ludus-server"
            # Update
            if [[ "${EUID}" == "0" ]]; then
              "${tmpdir}/ludus-server" --update
            else
              if command -v sudo >/dev/null 2>&1; then
                sudo "${tmpdir}/ludus-server" --update
              else
                print_message "[+] Failed to locate 'sudo' command" "error"
                exit 1
              fi
            fi
            ;;
      "n" ) print_message "[+] Skipping Ludus server update" "info"
            ;;
        * ) print_message "[-_-] Invalid response. Skipping Ludus server update" "error"
            ;;
    esac
  fi

  exit 0
}

#-------------------------------------------------------------------------------
#  ARGUMENT PARSING
#-------------------------------------------------------------------------------
OPTS="hp:"
while getopts "${OPTS}" optchar; do
  case "${optchar}" in
    'h' ) print_help
          exit 0
          ;;
    'p' ) INSTALL_PREFIX="${OPTARG}"
          ;;
     /? ) print_message "Unknown option ${OPTARG}" "warn"
          ;;
  esac
done

#-------------------------------------------------------------------------------
# CALL MAIN
#-------------------------------------------------------------------------------
main "${INSTALL_PREFIX}"