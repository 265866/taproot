#!/usr/bin/env bash
# The single-quoted perl programs read OLD and NEW from %ENV themselves.
# shellcheck disable=SC2016
# Rewrites the Go module path in the working tree of the current repository:
# every tracked .go file, go.mod, and go.sum. sync-upstream.sh runs it on each
# upstream snapshot before merging, and taproot's own tree went through the
# same rewrite once, so both sides of a merge carry identical import lines.
#
#   scripts/taproot/rewrite-module-path.sh [old-path [new-path]]
#
# The rewrite must stay deterministic: the same input tree always gives the
# same output, or upstream merges conflict on import lines again.
set -euo pipefail

old="${1:-github.com/router-for-me/CLIProxyAPI/v8}"
new="${2:-github.com/265866/taproot}"
root="$(git rev-parse --show-toplevel)"
cd "$root"
# The toolchain's own gofmt, so the import order does not depend on PATH.
gofmt="$(go env GOROOT)/bin/gofmt"
export OLD="$old" NEW="$new"

go_files="$(mktemp)"
trap 'rm -f "$go_files"' EXIT
git grep -lz -F -e "$old" -- '*.go' >"$go_files" || true
if [[ -s "$go_files" ]]; then
	xargs -0 perl -pi -e 's/\Q$ENV{OLD}\E/$ENV{NEW}/g' <"$go_files"
	# The new path sorts differently, so import blocks are re-sorted.
	xargs -0 "$gofmt" -w <"$go_files"
fi

# A path without a /vN suffix only takes v0 or v1 versions. Every module that
# requires this one replaces it with a local directory, so v0.0.0 is enough.
git grep -lz -F -e "$old" -- '*go.mod' | xargs -0 -r perl -pi -e '
	s/\Q$ENV{OLD}\E(\s+)v\d\S*/$ENV{NEW}${1}v0.0.0/g;
	s/\Q$ENV{OLD}\E/$ENV{NEW}/g;
' || true
# Checksums of the old path belong to a module that no longer exists here.
git grep -lz -F -e "$old " -- '*go.sum' | xargs -0 -r perl -ni -e 'print unless /^\Q$ENV{OLD}\E /' || true

if left="$(git grep -l -F -e "$old" -- '*.go' '*go.mod' '*go.sum')"; then
	printf 'still mentions %s:\n%s\n' "$old" "$left" >&2
	exit 1
fi
