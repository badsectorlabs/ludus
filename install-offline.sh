#!/bin/bash
# Distribute with the same release's install.sh and signed local installation media.
# This file is not part of the public online installer downloads.
offline_installer_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd) || exit 1
if [[ ! -r "${offline_installer_dir}/install.sh" ]] \
    || ! grep -qx 'LUDUS_INSTALLER_API=1' "${offline_installer_dir}/install.sh"; then
  echo 'Error: install-offline.sh requires a compatible, same-release install.sh beside it; nothing will be downloaded.' >&2
  exit 1
fi
source "${offline_installer_dir}/install.sh"
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

print_offline_help() {
  cat <<'EOF'
Ludus Offline Installer
Usage: bash install-offline.sh --template-file IMAGE --iso-directory DIR --iso-storage NAME [options]

Install or migrate an LXC using local media only. No client is downloaded.
Keep this script beside the install.sh supplied in the same offline distribution.
A version must be explicit or inferable from ludus-<version>-debian13-amd64.tar.zst.

Offline options:
  --iso-directory DIR    Local directory containing every pinned template ISO
  --iso-storage NAME     Required shared Proxmox storage supporting ISO content
  --license-file PATH    Signed offline license (requires --enterprise-plugin and --license)

A signed manifest, detached signature, and trusted public key are required unless
--skip-verification is explicitly selected for development media.
Existing-LXC updates are not supported by this entry point.

Shared LXC options (the defaults above override online release discovery):
EOF
  print_server_help
}

offline_verify_inputs() {
  [[ -n "${TEMPLATE_FILE:-}" ]] || { print_message '[!] Offline installation requires --template-file' error; exit 1; }
  [[ -n "${ISO_DIRECTORY:-}" && -d "${ISO_DIRECTORY}" && -r "${ISO_DIRECTORY}" ]] \
    || { print_message '[!] Offline installation requires a readable --iso-directory' error; exit 1; }
  [[ -n "${ISO_STORAGE:-}" ]] || { print_message '[!] Offline installation requires an explicit shared --iso-storage pool' error; exit 1; }
  if [[ -n "${LICENSE_FILE:-}" ]]; then
    [[ -n "${ENTERPRISE_PLUGIN:-}" ]] || { print_message '[!] --enterprise-plugin is required with --license-file' error; exit 1; }
    [[ -n "${LICENSE:-}" ]] || { print_message '[!] --license KEY is required with --license-file' error; exit 1; }
  fi
  if [[ "${SKIP_VERIFICATION:-0}" != 1 && -z "${CHECKSUM_FILE:-}" ]]; then
    print_message '[!] Offline installation requires --checksum-file, --checksum-signature, and --checksum-public-key' error
    exit 1
  fi
  local iso_name
  local signed_inputs=("${LICENSE_FILE:-}")
  for iso_name in "${AIRGAPPED_ISO_FILENAMES[@]}"; do
    signed_inputs+=("${ISO_DIRECTORY%/}/${iso_name}")
  done
  verify_local_inputs "${signed_inputs[@]}"
}

# Override shared installation phases, not the LXC provisioning/migration engine.
prepare_install_inputs() {
  if [[ -z "${ISO_STORAGE:-}" && "${NO_PROMPT:-0}" != 1 ]]; then
    read -r -p '[?] Shared ISO storage pool (required): ' ISO_STORAGE </dev/tty
  fi
  offline_verify_inputs
  [[ "${MIGRATE_HOST:-0}" != 1 ]] || prepare_migration_dependencies
  command_exists pvesh || { print_message '[!] pvesh is required for offline ISO upload' error; exit 1; }
  command_exists pvesm || { print_message '[!] pvesm is required for offline ISO verification' error; exit 1; }
  command_exists sha256sum || { print_message '[!] sha256sum is required for offline ISO verification' error; exit 1; }
  prepare_airgapped_isos
}

prepare_migration_dependencies() {
  command_exists conntrack || {
    print_message '[!] Offline host migration requires conntrack to be installed first' error
    exit 1
  }
}

customize_guest_config() {
  printf 'airgapped_install: true\n' >> "$1"
  if [[ -n "${LICENSE_FILE:-}" ]]; then
    pct push "${VMID}" "${LICENSE_FILE}" /opt/ludus/license.lic --perms 0640 --user 1001 --group 1001
  fi
}

offline_main() {
  set -Eeuo pipefail
  local original_args=("$@") shared_args=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) print_offline_help; return 0 ;;
      --iso-directory) ISO_DIRECTORY="${2:?--iso-directory requires a directory}"; shift 2 ;;
      --license-file) LICENSE_FILE="${2:?--license-file requires a path}"; shift 2 ;;
      --airgapped) echo 'Error: install-offline.sh is always offline; remove --airgapped.' >&2; return 1 ;;
      *) shared_args+=("$1"); shift ;;
    esac
  done
  parse_installer_args ${shared_args[@]+"${shared_args[@]}"}
  [[ -n "${TEMPLATE_FILE:-}" ]] || { print_message '[!] Offline installation requires --template-file' error; return 1; }
  if [[ -z "${LUDUS_VERSION:-}" ]]; then
    if ! LUDUS_VERSION=$(infer_ludus_version_from_template "${TEMPLATE_FILE}") || [[ -z "${LUDUS_VERSION}" ]]; then
      print_message '[!] Pass --version or use a template named ludus-<version>-debian13-amd64.tar.zst; offline release discovery is disabled' error
      return 1
    fi
  fi
  if [[ ${EUID} != 0 ]]; then
    command_exists sudo || { print_message '[!] Offline installation requires root' error; return 1; }
    exec sudo bash "${offline_installer_dir}/install-offline.sh" "${original_args[@]}" --version "${LUDUS_VERSION}"
  fi
  if lxc_host_install_exists; then
    print_message '[!] Offline installation does not update an existing LXC' error
    return 1
  fi
  SERVER_ONLY=1
  # Local media, including beta images, never use either public release channel.
  LUDUS_RELEASE_CHANNEL=stable
  main "${INSTALL_PREFIX}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  offline_main "$@"
fi
