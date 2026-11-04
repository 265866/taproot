#!/usr/bin/env bash
# Seeds a demo data directory, runs the real taproot server against it on
# loopback, and optionally captures the README screenshots. Nothing reaches a
# provider: config.yaml sends every outbound request to a dead loopback proxy,
# and this script points the proxy variables and usage-refresh overrides there too.
#
#   internal/taproot/demo/run.sh              seed and serve until Ctrl-C
#   internal/taproot/demo/run.sh --capture    seed, serve, write docs/screenshots/*.png, stop
#
# Options: --port N (default 8399), --dir DIR (an empty or missing directory
# for the demo data; default a new temporary directory, deleted on exit),
# --deny a,b (extra strings the capture must not find on any page).
#
# Build the dashboard first (bash scripts/taproot/build-web.sh); without it
# the server embeds a placeholder page.
set -euo pipefail

port=8399
dir=""
deny=""
capture=false
while (($#)); do
	case "$1" in
	--port) port="$2"; shift 2 ;;
	--dir) dir="$2"; shift 2 ;;
	--deny) deny="$2"; shift 2 ;;
	--capture) capture=true; shift ;;
	*) sed -n '2,15p' "$0" >&2; exit 2 ;;
	esac
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
if (($(wc -c <"$root/internal/taproot/web/dist/index.html") < 100000)); then
	echo "The embedded dashboard is the placeholder. Run: bash scripts/taproot/build-web.sh" >&2
	exit 1
fi

work="$(mktemp -d)"
[[ -n "$dir" ]] || dir="$work/data"
server_pid=""
cleanup() {
	if [[ -n "$server_pid" ]]; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

bin="$work/taproot$(go env GOEXE)"
(cd "$root" && go build -o "$bin" ./cmd/server)
(cd "$root" && go run ./internal/taproot/demo -dir "$dir" -port "$port")

dead="http://127.0.0.1:9"
env -u MANAGEMENT_PASSWORD -u TAPROOT_CONFIG \
	HTTP_PROXY="$dead" HTTPS_PROXY="$dead" http_proxy="$dead" https_proxy="$dead" NO_PROXY=127.0.0.1,localhost \
	TAPROOT_DEV_CLAUDE_USAGE_URL="$dead/usage" TAPROOT_DEV_CODEX_USAGE_URL="$dead/usage" \
	"$bin" --config "$dir/config.yaml" --local-model >"$work/server.log" 2>&1 &
server_pid=$!

url="http://127.0.0.1:$port"
for _ in $(seq 1 60); do
	curl -fsS "$url/healthz" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$url/healthz" >/dev/null || { cat "$work/server.log" >&2; exit 1; }

if ! $capture; then
	echo "Demo dashboard: $url/  (account emails appear about a minute after start; Ctrl-C stops the server)"
	wait "$server_pid"
	exit 0
fi

# playwright-core drives the installed Chrome; it is installed here and removed on exit.
(cd "$work" && echo '{"private":true}' >package.json && bun add --silent playwright-core@1.64.1)
bun "$root/internal/taproot/demo/capture.ts" --url "$url" --out "$root/docs/screenshots" \
	--playwright "$work/node_modules/playwright-core" --deny "$deny"
