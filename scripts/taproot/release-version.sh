#!/usr/bin/env bash
# Reads a release tag and prints what .github/workflows/release.yml needs, as
# name=value lines for $GITHUB_OUTPUT: the version, whether it is a
# prerelease, whether it is the latest release, and the image tags. Every
# version gets X.Y.Z and sha-<short commit>. A release (no "-" suffix) also
# gets X.Y when no newer X.Y.* release exists, and latest when no newer
# release exists at all, so a re-run or a fix for an older line never moves
# them back. A prerelease, such as 0.2.0-rc.1, gets neither, so nothing that
# follows X.Y or latest picks it up.
#
#   scripts/taproot/release-version.sh <tag> <commit sha> [file of existing tags]
#
#   scripts/taproot/release-version.sh taproot-v0.2.0-rc.1 <commit sha> tags.txt
set -euo pipefail

if (($# < 2 || $# > 3)); then
	echo "usage: $0 <tag> <commit sha> [file of existing tags]" >&2
	exit 2
fi
tag="$1" commit="$2" existing="${3:-/dev/null}"

# Semantic versions without build metadata: an image tag cannot hold "+".
number='(0|[1-9][0-9]*)'
if [[ ! "$tag" =~ ^taproot-v$number\.$number\.$number(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	printf '%s is not a release tag such as taproot-v1.2.3 or taproot-v1.2.3-rc.1\n' "$tag" >&2
	exit 1
fi
major="${BASH_REMATCH[1]}" minor="${BASH_REMATCH[2]}" suffix="${BASH_REMATCH[4]}"
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
	printf '%s is not a full commit sha\n' "$commit" >&2
	exit 1
fi
version="${tag#taproot-v}"
tags="$version sha-${commit:0:7}"

if [[ -n "$suffix" ]]; then
	printf 'version=%s\nprerelease=true\nlatest=false\nimage-tags=%s\n' "$version" "$tags"
	exit 0
fi

# newest prints the highest of the releases on stdin.
newest() { sort -V | tail -n 1; }
releases="$(grep -E "^taproot-v$number\.$number\.$number\$" "$existing" | sed "s/^taproot-v//"; echo "$version")"
latest=false
if [[ "$(grep -E "^$major\.$minor\." <<<"$releases" | newest)" == "$version" ]]; then
	tags="$tags $major.$minor"
fi
if [[ "$(newest <<<"$releases")" == "$version" ]]; then
	tags="$tags latest"
	latest=true
fi
printf 'version=%s\nprerelease=false\nlatest=%s\nimage-tags=%s\n' "$version" "$latest" "$tags"
