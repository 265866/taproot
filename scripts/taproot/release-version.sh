#!/usr/bin/env bash
# Reads a release tag and prints what .github/workflows/release.yml needs, as
# name=value lines for $GITHUB_OUTPUT: the version, whether it is a
# prerelease, and the image tags. A release gets X.Y.Z, X.Y, latest, and
# sha-<short commit>; a prerelease (a version with a "-" suffix, such as
# 0.2.0-rc.1) gets only its version and sha-<short commit>, so nothing that
# follows X.Y or latest picks it up.
#
#   scripts/taproot/release-version.sh taproot-v0.2.0-rc.1 <commit sha>
set -euo pipefail

if (($# != 2)); then
	echo "usage: $0 <tag> <commit sha>" >&2
	exit 2
fi
tag="$1" commit="$2"

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
short="sha-${commit:0:7}"

if [[ -n "$suffix" ]]; then
	printf 'version=%s\nprerelease=true\nimage-tags=%s %s\n' "$version" "$version" "$short"
else
	printf 'version=%s\nprerelease=false\nimage-tags=%s %s.%s latest %s\n' "$version" "$version" "$major" "$minor" "$short"
fi
