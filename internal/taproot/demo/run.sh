#!/usr/bin/env bash
# Seeds a demo data directory, runs the real taproot server against it on
# loopback, and optionally captures the documentation screenshots. Nothing
# reaches a provider: config.yaml sends every outbound request to a dead
# loopback proxy, and this script points the proxy variables and the
# usage-refresh and Discord overrides there too.
# A fake tailscaled, served by the demo command, gives the Access page a tailnet.
#
#   internal/taproot/demo/run.sh              seed and serve until Ctrl-C
#   internal/taproot/demo/run.sh --capture    seed, serve, write docs/screenshots/*.png, stop
#
# Options: --port N (default 8399), --dir DIR (an empty or missing directory
# for the demo data; default a new temporary directory, deleted on exit),
# --degraded (a quarter hour in which a fifth of Claude requests fail),
# --all-clear (nothing needs the user), --first-run (no accounts or requests yet),
# --deny a,b (extra strings the capture must not find on any page),
# --chrome PATH (the browser the capture drives; default the installed Chrome).
# --capture alone captures the default, degraded, and first-run demos in turn,
# each in its own subdirectory of DIR; with a scenario it captures only that
# scenario's Dashboard.
#
# Build the dashboard first (bash scripts/taproot/build-web.sh); without it
# the server embeds a placeholder page.
set -euo pipefail
# The server and the browser both show times with their zone, so pin one: a
# capture must not reveal the host's time zone.
export TZ=Etc/UTC

port=8399
dir=""
deny=""
capture=false
chrome=""
scenario=""
while (($#)); do
	case "$1" in
	--port) port="$2"; shift 2 ;;
	--dir) dir="$2"; shift 2 ;;
	--deny) deny="$2"; shift 2 ;;
	--chrome) chrome="$2"; shift 2 ;;
	--capture) capture=true; shift ;;
	--degraded) scenario="degraded"; shift ;;
	--all-clear) scenario="all-clear"; shift ;;
	--first-run) scenario="first-run"; shift ;;
	*) sed -n '2,23p' "$0" >&2; exit 2 ;;
	esac
done
if $capture && [[ "$scenario" == "all-clear" ]]; then
	echo "The all-clear demo has no screenshots; capture the default, --degraded, or --first-run demo." >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
if ! grep -q "/assets/" "$root/internal/taproot/web/dist/index.html"; then
	echo "The embedded dashboard is the placeholder. Run: bash scripts/taproot/build-web.sh" >&2
	exit 1
fi

work="$(mktemp -d)"
server_pid=""
tailnet_pid=""
stop_demo() {
	for pid in "$server_pid" "$tailnet_pid"; do
		if [[ -n "$pid" ]]; then
			kill "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
		fi
	done
	server_pid=""
	tailnet_pid=""
}
cleanup() {
	stop_demo
	rm -rf "$work"
}
trap cleanup EXIT

bin="$work/taproot$(go env GOEXE)"
demo="$work/demo$(go env GOEXE)"
(cd "$root" && go build -o "$bin" ./cmd/server && go build -o "$demo" ./internal/taproot/demo)
url="http://127.0.0.1:$port"

# start_demo DIR SCENARIO seeds DIR with the scenario ("" for the default
# demo) and starts the fake tailnet and the server on it.
start_demo() {
	local data="$1" flag=""
	[[ -z "$2" ]] || flag="-$2"
	"$demo" -dir "$data" -port "$port" $flag

	# The fake tailnet fills the Access page; the server reads it as tailscaled,
	# and refuses to start until its socket exists.
	"$demo" -dir "$data" -serve-tailnet >"$work/tailnet.log" 2>&1 &
	tailnet_pid=$!
	for _ in $(seq 1 50); do
		[[ -e "$data/tailscaled.sock" ]] && break
		sleep 0.1
	done
	[[ -e "$data/tailscaled.sock" ]] || { cat "$work/tailnet.log" >&2; exit 1; }

	local dead="http://127.0.0.1:9"
	env -u MANAGEMENT_PASSWORD -u TAPROOT_CONFIG \
		HTTP_PROXY="$dead" HTTPS_PROXY="$dead" http_proxy="$dead" https_proxy="$dead" NO_PROXY=127.0.0.1,localhost \
		TAPROOT_DEV_CLAUDE_USAGE_URL="$dead/usage" TAPROOT_DEV_CODEX_USAGE_URL="$dead/usage" \
		TAPROOT_ALERTS_DISCORD_ORIGIN="$dead" \
		"$bin" --config "$data/config.yaml" --local-model >"$work/server.log" 2>&1 &
	server_pid=$!

	for _ in $(seq 1 60); do
		curl -fsS "$url/healthz" >/dev/null 2>&1 && break
		sleep 1
	done
	curl -fsS "$url/healthz" >/dev/null || { cat "$work/server.log" >&2; exit 1; }
}

if ! $capture; then
	start_demo "${dir:-$work/data}" "$scenario"
	echo "Demo dashboard: $url/  (account emails appear about a minute after start; Ctrl-C stops the server)"
	wait "$server_pid"
	exit 0
fi

# playwright-core drives the installed Chrome; it is installed here and removed on exit.
(cd "$work" && echo '{"private":true}' >package.json && bun add --silent playwright-core@1.64.1)
scenarios=("$scenario")
[[ -n "$scenario" ]] || scenarios=("" degraded first-run)
for sc in "${scenarios[@]}"; do
	data="${dir:-$work/data}"
	((${#scenarios[@]} == 1)) || data="$data/${sc:-normal}"
	start_demo "$data" "$sc"
	bun "$root/internal/taproot/demo/capture.ts" --url "$url" --out "$root/docs/screenshots" \
		--scenario "${sc:-normal}" --playwright "$work/node_modules/playwright-core" --deny "$deny" ${chrome:+--chrome "$chrome"}
	stop_demo
done
