#!/usr/bin/env bash
# Refreshes the two flake files that lockfiles decide: web/bun.nix, which
# bun2nix generates from web/bun.lock, and nix/vendor-hash, the hash of the Go
# module download that go.sum decides. Run it after go.sum or web/bun.lock
# changes; CI's nix-hashes job fails until both match. Needs Nix.
#
#   scripts/taproot/nix-refresh.sh
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"
nix_flags=(--extra-experimental-features 'nix-command flakes')
placeholder="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

# bun2nix comes from flake.lock, so the output does not depend on the caller's version.
nix "${nix_flags[@]}" run --inputs-from . bun2nix -- --lock-file web/bun.lock --output-file web/bun.nix
# A package without integrity in bun.lock gets an empty hash, which no build accepts.
if grep -q 'hash = "";' web/bun.nix; then
	echo "web/bun.lock has packages without an integrity hash; reinstall them with bun so the lockfile records one:" >&2
	grep -B2 'hash = "";' web/bun.nix | sed -n 's/^ *"\(.*\)" = .*/  \1/p' >&2
	exit 1
fi

# The module download is a fixed-output derivation, so a stale hash whose
# output is already in the store would pass a plain build. Build it with a
# placeholder hash and read the real one from the mismatch.
old="$(cat nix/vendor-hash)"
log="$(mktemp)"
cleanup() {
	rm -f "$log"
	if [[ "$(cat nix/vendor-hash)" == "$placeholder" ]]; then
		printf '%s\n' "$old" >nix/vendor-hash
	fi
}
trap cleanup EXIT
printf '%s\n' "$placeholder" >nix/vendor-hash
if nix "${nix_flags[@]}" build --no-link .#taproot.goModules >"$log" 2>&1; then
	echo "the Go module download built with a placeholder hash; check nix/taproot.nix" >&2
	exit 1
fi
new="$(sed -n 's/^ *got: *\(sha256-[A-Za-z0-9+/=]*\)$/\1/p' "$log" | tail -n 1)"
if [[ -z "$new" ]]; then
	cat "$log" >&2
	echo "building the Go module download failed without a hash mismatch; see above" >&2
	exit 1
fi
printf '%s\n' "$new" >nix/vendor-hash
if [[ "$new" == "$old" ]]; then
	echo "nix/vendor-hash is current: $new"
else
	echo "nix/vendor-hash: $old -> $new"
fi
