#!/usr/bin/env bash
# Prints the release notes for a version from CHANGELOG.md: the body of its
# "## " section, whose heading is the version as release-please writes it
# ("## [0.2.0](<compare link>) (2026-11-20)") or as v0.1.0's is written
# ("## v0.1.0"). A prerelease without its own section, such as a release
# candidate tagged by hand, gets the "## Unreleased" section, or a line saying
# it is a release candidate when that section is empty. A release without a
# section is an error.
#
#   scripts/taproot/release-notes.sh <version> [changelog]    default CHANGELOG.md
set -euo pipefail

if (($# < 1 || $# > 2)); then
	echo "usage: $0 <version> [changelog]" >&2
	exit 2
fi
version="$1" changelog="${2:-CHANGELOG.md}"

# section prints the body of the "## " section whose first word, without
# brackets, a link, or a leading v, is $1, with surrounding blank lines removed.
section() {
	awk -v want="$1" '
		/^## / {
			if (inside) exit
			name = $2
			sub(/\].*/, "", name)
			sub(/^\[/, "", name)
			sub(/^v/, "", name)
			if (name == want) inside = 1
			next
		}
		inside { lines[++n] = $0 }
		END {
			first = 1
			while (first <= n && lines[first] ~ /^[[:space:]]*$/) first++
			last = n
			while (last >= first && lines[last] ~ /^[[:space:]]*$/) last--
			for (i = first; i <= last; i++) print lines[i]
		}' "$changelog"
}

notes="$(section "$version")"
if [[ -n "$notes" ]]; then
	printf '%s\n' "$notes"
	exit 0
fi
if [[ "$version" != *-* ]]; then
	printf '%s has no section for %s\n' "$changelog" "$version" >&2
	exit 1
fi
unreleased="$(section Unreleased)"
if [[ -n "$unreleased" ]]; then
	printf 'A release candidate for testing. It includes these changes, which are not released yet:\n\n%s\n' "$unreleased"
else
	printf 'A release candidate for testing. See CHANGELOG.md for the changes in the release it leads to.\n'
fi
