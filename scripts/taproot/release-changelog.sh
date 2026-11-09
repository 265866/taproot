#!/usr/bin/env bash
# Writes a release's section into CHANGELOG.md from taproot's own Conventional
# Commits since the previous release: feat, fix, perf, and revert, with
# breaking changes listed first. Upstream commits that a sync merged are left
# out, and one line names the upstream commit the release merges instead.
# release-please would list every upstream commit as taproot's, because it
# reads the whole history. The section goes after "## Unreleased", or replaces
# the version's section when there is one.
#
#   scripts/taproot/release-changelog.sh <version> <previous version> <head> [changelog]
#
# The links name $GITHUB_REPOSITORY (default 265866/taproot). The date is the
# head commit's, in UTC.
set -euo pipefail

if (($# < 3 || $# > 4)); then
	echo "usage: $0 <version> <previous version> <head> [changelog]" >&2
	exit 2
fi
version="$1" previous="$2" head="$3" changelog="${4:-CHANGELOG.md}"
repo="${GITHUB_REPOSITORY:-265866/taproot}"
# shellcheck source=scripts/taproot/upstream-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/upstream-lib.sh"

previous_tag="taproot-v$previous"
git rev-parse --quiet --verify "refs/tags/$previous_tag^{commit}" >/dev/null || {
	printf 'tag %s does not exist\n' "$previous_tag" >&2
	exit 1
}
head="$(git rev-parse --verify "$head^{commit}")"

# Every ancestor of the newest snapshot is upstream's, or an older snapshot.
exclude=("^$previous_tag")
upstream=""
if snapshot="$(latest_snapshot "$head")"; then
	exclude+=("^$snapshot")
	if ! git merge-base --is-ancestor "$snapshot" "$previous_tag"; then
		upstream="$(git rev-parse "$snapshot^1")"
	fi
fi

breaking="" added="" fixed="" performance="" reverted=""
pattern='^(feat|fix|perf|revert)(\(([^)]*)\))?(!)?: (.+)$'
for commit in $(git rev-list --reverse --no-merges "$head" "${exclude[@]}"); do
	message="$(git log -1 --format=%B "$commit")"
	subject="${message%%$'\n'*}"
	[[ "$subject" =~ $pattern ]] || continue
	type="${BASH_REMATCH[1]}" scope="${BASH_REMATCH[3]}" bang="${BASH_REMATCH[4]}" text="${BASH_REMATCH[5]}"
	if [[ "$text" =~ ^(.*[^[:space:]])[[:space:]]+\(#([0-9]+)\)$ ]]; then
		text="${BASH_REMATCH[1]} ([#${BASH_REMATCH[2]}](https://github.com/$repo/pull/${BASH_REMATCH[2]}))"
	fi
	entry="* ${scope:+**$scope:** }$text ([${commit:0:7}](https://github.com/$repo/commit/$commit))"$'\n'
	if [[ -n "$bang" ]] || grep -Eq '^BREAKING[ -]CHANGE: ' <<<"$message"; then
		breaking+="$entry"
	fi
	case "$type" in
	feat) added+="$entry" ;;
	fix) fixed+="$entry" ;;
	perf) performance+="$entry" ;;
	revert) reverted+="$entry" ;;
	esac
done

section="$(mktemp)"
trap 'rm -f "$section"' EXIT
{
	printf '## [%s](https://github.com/%s/compare/%s...taproot-v%s) (%s)\n' \
		"$version" "$repo" "$previous_tag" "$version" \
		"$(TZ=UTC0 git log -1 --date=format-local:%Y-%m-%d --format=%cd "$head")"
	any=""
	for part in "Breaking changes:$breaking" "Added:$added" "Fixed:$fixed" "Performance:$performance" "Reverted:$reverted"; do
		[[ -n "${part#*:}" ]] || continue
		printf '\n### %s\n\n%s' "${part%%:*}" "${part#*:}"
		any=1
	done
	if [[ -n "$upstream" ]]; then
		printf '\n### Upstream\n\n* Merges [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) up to [%s](https://github.com/router-for-me/CLIProxyAPI/commit/%s).\n' "${upstream:0:9}" "$upstream"
		any=1
	fi
	if [[ -z "$any" ]]; then
		printf '\nNo user-facing changes.\n'
	fi
} >"$section"

# Drop the version's old section, and put the new one before the first
# version's heading (after "## Unreleased"), or at the end.
awk -v want="$version" -v file="$section" '
	function name(line,   n) {
		n = line
		sub(/^## /, "", n)
		sub(/[[:space:]].*/, "", n)
		sub(/\].*/, "", n)
		sub(/^\[/, "", n)
		sub(/^v/, "", n)
		return n
	}
	function emit(trail,   line) {
		while ((getline line < file) > 0) print line
		if (trail) print ""
		done = 1
	}
	/^## / {
		skip = 0
		if (name($0) == want) { skip = 1; if (!done) emit(1); next }
		if (!done && name($0) != "Unreleased") emit(1)
	}
	skip { next }
	{ print }
	END { if (!done) { print ""; emit(0) } }
' "$changelog" >"$changelog.new"
mv "$changelog.new" "$changelog"
