#!/usr/bin/env bash
# Regenerates THIRD_PARTY_NOTICES.md for each Go subproject from go-licenses
# (go install github.com/google/go-licenses/v2@latest).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
repo_root="$PWD"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

# First-party workspace modules carry the project's own license, not a third-party one.
ignore=(--ignore ludus --ignore ludusapi --ignore ludus-server --ignore ludus-installer --ignore dynamic-inventory --ignore gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk)

# Modules whose license file go-licenses cannot classify, verified by hand: "<module> <SPDX id> <license file>".
overrides=(
	"github.com/keygen-sh/go-update Apache-2.0 LICENSE" # file holds only the Apache-2.0 header
)

for project in ludus-server ludus-client ludus-api ludus-installer dynamic-inventory ludus-plugin-sdk; do
	echo "Generating $project/THIRD_PARTY_NOTICES.md" >&2
	cd "$repo_root/$project"
	out="$tmp_dir/$project.md"

	go-licenses report ./... "${ignore[@]}" --template "$repo_root/scripts/notices.tpl" > "$out"

	for override in "${overrides[@]}"; do
		read -r module spdx file <<< "$override"
		grep -qxF "## $module" "$out" || continue
		read -r module_dir version < <(go list -m -f '{{.Dir}} {{.Version}}' "$module")
		# Fill in the license line and the empty license text block of the module's entry.
		awk -v heading="## $module" -v license="* License: [$spdx](https://$module/blob/$version/$file)" -v text="$module_dir/$file" '
			$0 == heading { entry = 1 }
			entry && /^\* License: / { print license; next }
			entry && $0 == "```" { print; while ((getline line < text) > 0) print line; entry = 0; skip = 1; next }
			skip { if ($0 == "```") { print; skip = 0 }; next }
			{ print }
		' "$out" > "$out.tmp"
		mv "$out.tmp" "$out"
	done

	# `save` copies each library's license plus any NOTICE files (Apache-2.0 §4(d)).
	# It exits non-zero on licenses it cannot classify; the report above already
	# surfaced those, so only the copied files matter here.
	save_dir="$tmp_dir/$project-save"
	go-licenses save ./... "${ignore[@]}" --save_path "$save_dir" 2>/dev/null || true

	notices="$(find "$save_dir" -type f \( -name NOTICE -o -name NOTICE.txt -o -name NOTICE.md \) 2>/dev/null | sort)"
	if [[ -n "$notices" ]]; then
		printf '## NOTICE files\n\n' >> "$out"
		while IFS= read -r notice; do
			lib="${notice#"$save_dir/"}"
			printf '### %s\n\n```\n%s\n```\n\n' "${lib%/*}" "$(cat "$notice")" >> "$out"
		done <<< "$notices"
	fi

	mv "$out" THIRD_PARTY_NOTICES.md
done
