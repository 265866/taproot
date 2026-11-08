#!/usr/bin/env bash
# Checks that the "Upstream boundary" section of internal/taproot/DESIGN.md
# names, in backticks, every upstream path a commit changes since the newest
# rewritten upstream snapshot in its history: each modified or added path
# outside the paths taproot owns, and each deleted one. A deletion may also be
# covered by a documented directory (`assets/`) or glob (`assets/logo/*.svg`).
# The commit's own DESIGN.md is read, so commit both before running it.
#
#   scripts/taproot/check-upstream-boundary.sh [commit]    default HEAD
set -euo pipefail
# shellcheck source=scripts/taproot/upstream-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/upstream-lib.sh"

design=internal/taproot/DESIGN.md
# Paths that are taproot's own, so changing them is not an upstream edit.
fork_owned='^(internal/taproot/|web/|scripts/taproot/|examples/taproot/|docs/|\.github/|nix/|(Containerfile\.taproot|CHANGELOG\.md|CONTRIBUTING\.md|SECURITY\.md|THIRD_PARTY_NOTICES\.md|\.golangci\.yml|renovate\.json|flake\.nix|flake\.lock)$)'

rev="${1:-HEAD}"
cd "$(git rev-parse --show-toplevel)"
git rev-parse --verify --quiet "$rev^{commit}" >/dev/null || {
	printf '%s is not a commit\n' "$rev" >&2
	exit 1
}
snapshot="$(latest_snapshot "$rev")" || {
	printf '%s has no rewritten upstream snapshot (a commit with a %s: trailer naming its first parent)\n' "$rev" "$upstream_trailer" >&2
	exit 1
}

doc="$(git show "$rev:$design")"
section="$(awk '/^## Upstream boundary[[:space:]]*$/ { inside = 1; next } inside && /^## / { exit } inside' <<<"$doc")"
if [[ -z "$section" ]]; then
	printf '%s in %s has no "## Upstream boundary" section\n' "$design" "$rev" >&2
	exit 1
fi
names=()
# shellcheck disable=SC2016 # The backticks are Markdown code spans.
while IFS= read -r name; do
	names+=("$name")
done < <(grep -o '`[^`]*`' <<<"$section" | tr -d '`' | sort -u)

# documented succeeds when the section names path $2, changed with git status
# $1. Only a deletion may match a directory or a glob: an edit or a new file
# needs its own entry, which gives its reason.
documented() {
	local status="$1" path="$2" name
	for name in ${names[@]+"${names[@]}"}; do
		[[ "$name" == "$path" ]] && return 0
		[[ "$status" == D ]] || continue
		if [[ "$name" == */ ]]; then
			[[ "$path" == "$name"* ]] && return 0
		elif [[ "$name" == *[*?]* ]]; then
			# In [[ == ]], * also matches /, so a glob without ** must also
			# have as many / as the path: logo/*.svg skips logo/sub/x.svg.
			# shellcheck disable=SC2053 # $name is a glob.
			[[ "$path" == $name ]] || continue
			[[ "$name" == *'**'* || "${path//[^\/]/}" == "${name//[^\/]/}" ]] && return 0
		fi
	done
	return 1
}

changes="$(git -c core.quotePath=false diff --name-status --no-renames "$snapshot" "$rev")"
checked=0
missing=()
while IFS=$'\t' read -r status path; do
	[[ -n "$status" ]] || continue
	[[ "$path" =~ $fork_owned ]] && continue
	checked=$((checked + 1))
	documented "$status" "$path" || missing+=("$status	$path")
done <<<"$changes"

if ((${#missing[@]})); then
	printf 'These upstream paths changed since snapshot %s, but the "Upstream boundary" section of %s does not name them:\n' "${snapshot:0:9}" "$design" >&2
	printf '  %s\n' "${missing[@]}" >&2
	printf 'Name each one in backticks there, with the reason for the change and its size, or undo the change.\n' >&2
	exit 1
fi
printf '%s names all %d upstream paths changed since snapshot %s.\n' "$design" "$checked" "${snapshot:0:9}"
