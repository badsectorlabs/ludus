#!/usr/bin/env bash
set -euo pipefail
set +x

# Run only inside the disposable legacy_migration clone, never on its seed.
TEMPLATE=${1:?LXC template path required}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
source "$ROOT/ci/job-server.sh"
[[ "${LUDUS_CI_PROFILE:-}" == lxc-2.4 && "${LUDUS_SNAPSHOT_NAME:-}" == legacy_migration ]] || {
    echo 'Migration CI requires the lxc-2.4 legacy_migration fixture' >&2; exit 1;
}
sudo test -f /opt/ludus/config.yml
! sudo test -e /etc/ludus-lxc.json
! sudo test -e /opt/ludus/install/.bootstrap-complete

# Read the old service with its existing CLI before introducing the candidate.
LUDUS_CI_PROFILE=host-2.3 ci_server_init
ci_client_config
export LUDUS_API_KEY=$(sudo cat /opt/ludus/install/root-api-key)
root_key=$LUDUS_API_KEY
old_url=$CI_SERVER_URL
ludus users add -a -n 'CI Migration' -i CIM -e cim@ludus.internal -p migration-ci-password >/dev/null
export LUDUS_API_KEY=$(ludus --user CIM users apikey --no-prompt --value)
user_key=$LUDUS_API_KEY
before_creds=$(ludus user creds get --json | jq -S '.result')
before_config=$(sudo python3 -c 'import json,yaml; print(json.dumps(yaml.safe_load(open("/opt/ludus/ranges/CIM/range-config.yml")), sort_keys=True))')
# Bundled templates can legitimately change between releases. Add a real
# user-owned template and verify that it and any built templates survive.
template_dir=$(mktemp -d)
trap 'rm -rf "$template_dir"' EXIT
sudo cp -a /opt/ludus/packer/debian12/. "$template_dir/"
sudo chown -R "$(id -u):$(id -g)" "$template_dir"
python3 -c '
import pathlib, sys
p = pathlib.Path(sys.argv[1]) / "debian12.pkr.hcl"
text = p.read_text()
old = "\"debian-12-x64-server-template\""
assert text.count(old) == 1, "Cannot identify the source template name"
p.write_text(text.replace(old, "\"ci-migration-debian-template\""))
' "$template_dir"
ludus templates add -d "$template_dir"
ludus templates list --json | jq -e 'any(.[]; .name == "ci-migration-debian-template")'
before_templates=$(ludus templates list --json | jq -S 'sort_by(.name) | map(select(.built or .name == "ci-migration-debian-template") | {name, built})')
before_vms=$(sudo pvesh get /cluster/resources --type vm --output-format json | jq -c '[.[] | select(.type == "qemu") | .vmid] | sort')

installer="$ROOT/../install.sh"
if [[ "${LUDUS_VERSION:-}" == *-beta* ]]; then installer="$ROOT/../install-beta.sh"; fi
sudo -E bash "$installer" --no-prompt --migrate-host --template-file "$TEMPLATE" --storage local
ci_server_init
ci_server_wait
ci_client_config
sudo install -m 0755 "$ROOT/../binaries/ludus-client_linux-amd64" /usr/local/bin/ludus
[[ "$(ci_server_exec cat /opt/ludus/install/root-api-key)" == "$root_key" ]]
ci_server_exec test -f /opt/ludus/install/.bootstrap-complete
ci_server_exec systemctl is-active --quiet ludus ludus-admin
! sudo systemctl is-active --quiet ludus
sudo systemctl is-active --quiet ludus-lxc-forwarding
curl -fkSs --max-time 10 "$old_url/api/health" | jq -e '.code == 200'
export LUDUS_API_KEY=$user_key
ludus user list --json | jq -e 'any(.[]; .userID == "CIM" and .isAdmin == true)'
[[ "$(ludus user creds get --json | jq -S '.result')" == "$before_creds" ]]
# Migration serializes YAML and adds the legacy router template explicitly.
# Compare range settings, not bytes or formatting, while allowing that annotation.
ci_server_exec /opt/ludus/venv/bin/python3 -c '
import json, yaml, sys
before = json.loads(sys.argv[1])
after = yaml.safe_load(open("/opt/ludus/ranges/CIM/range-config.yml"))
if "router" not in before and set(after.get("router", {})) == {"template"}:
    del after["router"]
assert after == before, "Migration changed existing range settings"
' "$before_config"
ludus templates list --json | jq -e --argjson before "$before_templates" 'map({name, built}) as $after | ($before - $after) == []'
[[ "$(sudo pvesh get /cluster/resources --type vm --output-format json | jq -c '[.[] | select(.type == "qemu") | .vmid] | sort')" == "$before_vms" ]]
# Exercise a post-migration write through the retained API key and container state.
ludus range create -n 'Migration smoke' -r CIM-migrated
ci_server_exec test -f /opt/ludus/ranges/CIM-migrated/range-config.yml
ludus range rm-range -r CIM-migrated --no-prompt
printf '%s\n' 'PASS: host migration preserves credentials, range config, templates, VMs and API endpoints; container accepts new writes'
