#!/usr/bin/env bash
# Builds the dashboard in web/ and copies its output (index.html and the
# content-hashed chunks under assets/) to where the Go server embeds it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dest="$root/internal/taproot/web/dist"

cd "$root/web"
bun install --frozen-lockfile
bun run build

# The server serves index.html and assets/ only, so anything else would be missing at runtime.
extra="$(find dist -type f ! -path dist/index.html ! -path 'dist/assets/*' | head -n 5)"
if [[ -n "$extra" ]]; then
	printf 'web/dist has files outside index.html and assets/:\n%s\n' "$extra" >&2
	exit 1
fi

rm -rf "$dest/assets"
cp dist/index.html "$dest/index.html"
cp -r dist/assets "$dest/assets"
printf 'Copied web/dist (%s bytes in %s files) to %s\n' "$(find dist -type f -exec cat {} + | wc -c | tr -d ' ')" "$(find dist -type f | wc -l | tr -d ' ')" "${dest#"$root"/}"
