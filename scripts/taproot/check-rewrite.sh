#!/usr/bin/env bash
# Checks that rewrite-module-path.sh still reproduces the newest rewritten
# upstream snapshot in the history of a commit. It rewrites the upstream commit
# named by the snapshot's Taproot-Upstream trailer and compares the trees.
#
#   scripts/taproot/check-rewrite.sh [commit]    default HEAD
set -euo pipefail
# shellcheck source=scripts/taproot/upstream-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/upstream-lib.sh"

rev="${1:-HEAD}"
cd "$(git rev-parse --show-toplevel)"
snapshot="$(latest_snapshot "$rev")" || {
	printf '%s has no rewritten upstream snapshot (a commit with a %s: trailer naming its first parent)\n' "$rev" "$upstream_trailer" >&2
	exit 1
}
check_snapshot "$snapshot"
