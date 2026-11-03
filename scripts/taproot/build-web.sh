#!/usr/bin/env bash
# Builds the dashboard in web/ and copies its single-file output to where the
# Go server embeds it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dest="$root/internal/taproot/web/dist/index.html"

cd "$root/web"
bun install --frozen-lockfile
bun run build

# Only index.html is embedded, so any other build output would be missing at runtime.
extra="$(find dist -type f ! -path dist/index.html | head -n 5)"
if [[ -n "$extra" ]]; then
	printf 'web/dist has files besides index.html; the build must be single-file:\n%s\n' "$extra" >&2
	exit 1
fi

cp dist/index.html "$dest"
printf 'Copied web/dist/index.html (%s bytes) to %s\n' "$(wc -c <"$dest" | tr -d ' ')" "${dest#"$root"/}"
