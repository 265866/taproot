# Taproot plan

Taproot is the owner's fork of CLIProxyAPI. It runs on server and serves every Claude and Codex account to his tailnet through one endpoint, with one dashboard that covers credentials, quota, usage and cost, request logs, and backups.

## Outcome and acceptance

- Taproot is a new service on server with its own data. Nothing is migrated: usage and quota history start with taproot. At cutover, the two Claude auth files are copied in and the old `cliproxyapi` is stopped first, so the two proxies never refresh the same OAuth tokens.
- Only allowlisted Tailscale logins reach the proxy, the management API, or the dashboard. Nobody types a key or password.
- The dashboard has no Quick Start, AI Providers, sponsor or affiliate content, Kimi China, Devin, Vertex, SIWC, or config API-key editing.
- Quota page in the style of the reference screenshot (`internal/taproot/docs/quota-reference.png`): pooled remaining percentage per provider, recorded and projected lines, pool empty, first empty, next reset, and burn rate over 1h, 3h, or 6h.
- Usage section: cost, requests, tokens including cache reads and writes, latency, time to first token, and failures, broken down by account, model, and session.
- Request logs are compressed, indexed, searchable, and pruned to a budget. Backups run on a schedule, and you can download or restore them from the dashboard.
- Upstream merges stay cheap. See "Upstream boundary".

## Upstream boundary

- The Go module path stays `github.com/router-for-me/CLIProxyAPI/v8`, so upstream merges never rewrite imports.
- Fork code lives in `internal/taproot/**`. The only upstream file it edits is `cmd/server/main.go`, with one `taproot.Setup` call. Any further upstream edit needs a written reason in this file.
- The removed providers come out of the dashboard only. Their backend code stays, because deleting it would conflict with every upstream merge.
- `web/` is a hard fork of Cli-Proxy-API-Management-Center at `f154fa9`, imported with `git subtree`. The fork stops following upstream UI changes. It follows the backend's management API instead.
- `scripts/taproot/sync-upstream.sh` merges `upstream/main` and runs the checks.
- `sdk/cliproxy/service_config.go` maps `routing.strategy: soonest-reset-first` to `internal/taproot/routing.SoonestResetSelector` (7 lines).
  - Upstream builds the credential selector only there, and `routing.session-affinity` wraps whatever it builds.
  - The plugin-scheduler hook would bypass session affinity, and the plugin host overwrites it.
- `internal/api/protocol_multiplexer.go` gains a package-level RESP connection filter (`SetRESPConnectionFilter`, `RESPConnectionAllowed`, about 25 lines). The multiplexer consults it before a Redis-protocol connection reaches `handleRedisConnection` and closes a refused connection before any reply. Taproot's `Setup` installs `access.RESPFilter`, which admits loopback only.
  - Upstream serves its RESP usage queue on the proxy port outside gin, so the guard never sees it, and the guard's generated `MANAGEMENT_PASSWORD` turns on remote `AUTH` there. Without the hook, any host that reaches the port could `AUTH` with the management secret or a `config.yaml` `secret-key` and drain the usage and error queues.
  - No server option or middleware runs at that point, so a package-level hook is the smallest seam. `internal/api/taproot_resp_filter_test.go` drives it through the real multiplexer.

## Soonest-reset-first routing

- **Order.** New sessions and failovers go to the eligible account whose weekly window resets soonest:
  - Claude uses `anthropic-ratelimit-unified-7d-reset`.
  - Codex uses its 10080-minute window.
  - Weekly resets are fixed per account, so a past reset rolls forward by whole weeks.
- **Skipped and unknown accounts.** Accounts at or above 100% weekly utilization are skipped. Accounts whose responses carry no weekly reset sort last. Ties use round-robin.
- **Session affinity first.** Bound sessions keep their account.
- **Probing after a restart.** Upstream keeps quota headers in memory only. After a restart, each Claude or Codex account with no observed headers gets one new session first, so its reset becomes known. Without that, the first account to answer would take every session.

## Architecture

`taproot.Setup` (in `internal/taproot/taproot.go`) reads `taproot.yaml` from beside `config.yaml`, or from the path in `TAPROOT_CONFIG`. Upstream's config parser rejects unknown keys, so taproot's settings can't live in `config.yaml`. Setup then opens the modules and returns `api.ServerOption`s:

- `WithMiddleware(guard.Middleware())`
- `WithRouterConfigurator(...)`, which mounts `/taproot/api/v1/*` and the dashboard at `/`
- `WithRequestLoggerFactory(...)`

Data lives in `<data-dir>` (default `taproot/` beside `config.yaml`):

- `taproot.db`, SQLite in WAL mode
- `request-logs/`
- `backups/`

Each module owns its tables and registers migrations with `store.Register`, so no shared schema file exists.

| Module | Package | Owns |
|---|---|---|
| store | `internal/taproot/store` | DB open, migrations, `Snapshot` |
| access | `internal/taproot/access` | Tailnet identity, allowlist, keyless management |
| ingest | `internal/taproot/ingest` | Usage events, quota observations, pricing |
| reqlog | `internal/taproot/reqlog` | Request log storage, index, retention |
| backup | `internal/taproot/backup` | Scheduled snapshots, download, restore |
| web | `internal/taproot/web` | The embedded dashboard build |

### Access

- **Identifying callers.** For every request, the guard resolves the caller from its source IP:
  - Loopback addresses and `trusted-cidrs` are trusted, with the limits in "Security model".
  - Tailnet addresses (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) are resolved with tailscaled's LocalAPI `whois` over its unix socket (default `/var/run/tailscale/tailscaled.sock`, mounted into the container). The result is cached for one minute.
  - The caller is admitted when the login is in `allow-logins`. The default list holds only `alice@github`.
  - An admitted tailnet caller is an admin only when its node is in `admin-nodes` (see "Security model").
  - Everything else gets a 403, including whois failures. `/healthz` is exempt.
- **Why source IPs and not `tailscale serve` headers.** Source IPs work for the proxy port, so client configs stay as they are, and tailnet source addresses can't be forged.
- **Keyless management.** At setup, the guard puts a random secret in `MANAGEMENT_PASSWORD`, unless an operator already set one. Upstream's management handler reads that at construction. For admitted callers, the guard injects `Authorization: Bearer <secret>` on `/v0/management/*`, `/v8/management/*`, and plugin resource routes. The secret never leaves the process. A `secret-key` in `config.yaml` keeps working for scripts.
- **Proxy keys.** Proxy API keys (`access.api-keys`) are irrelevant once the guard admits callers by identity. They stay empty.
- **Development.** On Windows there's no tailscaled socket, so only loopback is admitted.
- `GET /taproot/api/v1/me` returns `{login, displayName, node, via: "loopback"|"tailnet"|"cidr", admin}`. It is on the admin surface, so only admins reach it.
- **Denials the dashboard can explain.** The guard's 403 is `{error: {code, message}}`. The code is `forbidden`, except for two cases:
  - `not_admin_node`, with `node`, for an allowlisted login on a node outside `admin-nodes`. The dashboard's connection gate then tells the user to add that node to `access.admin-nodes` in `taproot.yaml`.
  - `request_header_required` for a state-changing GET without `X-Taproot-Request: 1` (see "Security model").

### Security model

The guard splits the server into two surfaces. The package comment in `internal/taproot/access/access.go` is the code-side copy of this section.

- **Admin surface.** Everything that reads or changes credentials, settings, backups, or plugins:
  - the dashboard (`GET`/`HEAD /`)
  - `/v0/management/**` and `/v8/management/**`, including the plugin management routes upstream serves from `NoRoute`
  - `/taproot/**`
  - plugin pages and assets under `/v0/resource/plugins/**` (the dashboard frames them same-origin)
  - the OAuth callbacks on the main port (`/anthropic/callback`, `/codex/callback`, `/antigravity/callback`, `/callback`, `/devin/callback`), which write the code for a pending login
  - `/v1/ws`, which registers a websocket client as an AI Studio credential
- **Proxy surface.** Everything else, such as `/v1/messages`, `/v1/responses`, `/backend-api/codex/**`, and `/v1/models`.
- **Who reaches what.**

  | Caller | Proxy | Admin |
  |---|---|---|
  | Loopback (inside the container, or local development) | yes | yes |
  | `trusted-cidrs` | yes | no |
  | Allowlisted login on the server's own node (server, `100.64.0.1`) | yes | no |
  | Allowlisted login on another node listed in `admin-nodes` | yes | yes |
  | Allowlisted login on a node outside `admin-nodes` (worker, nas, relay, router) | yes | no |
  | Anyone else | no | no |

  - **The dashboard page is open to every admitted caller.** `GET`/`HEAD /` returns the static single-file build, which holds no data. A device outside `admin-nodes` can therefore load it, and the connection gate shows why the API refuses it. Every request for data is still admin-only.

  - **The server's own node is proxy-only.** Rootless containers and local users on server reach the published `100.64.0.1:8317` with that source address (measured through pasta), and whois says that node belongs to `alice@github`. the owner's agents on server need the proxy, so the node keeps it, but none of those processes gets credentials or settings.
  - **The server's own node comes from tailscaled.** The guard reads `Self` from `GET /localapi/v0/status?peers=false` and treats a caller as the server when its address is in `Self.TailscaleIPs` or its whois `Node.StableID` equals `Self.ID`. It refreshes every cache TTL and keeps the last known node when a refresh fails. Until the first read succeeds, tailnet callers get no admin access, only local Host names pass the Host check, and a warning is logged.
  - **`trusted-cidrs` are proxy-only.** They hold host-local addresses that every local process shares. A server-local client that uses the `127.0.1.1:8317` publish arrives as server's LAN address `192.168.0.88` (measured). If such a client needs the proxy, add `192.168.0.88/32`, never `192.168.0.0/24`, which would admit the whole LAN. Local clients can also use `100.64.0.1:8317` instead.
  - **`admin-nodes`** lists the tailnet nodes that reach the admin surface. Admin is deny-by-default: with the list empty, no tailnet caller is an admin and only loopback is, and startup logs a warning. the owner's other server nodes (worker, nas, relay, router) resolve to his login too, so any process, compromised container, or SSRF on them would otherwise get his tokens and settings (security recheck F1).
    - An entry is a MagicDNS name assigned by control, either full (`desktop.tailnet-0000.ts.net`) or its first label for nodes in server's tailnet (`desktop`).
    - Only whois `Node.Name` is matched. `ComputedName` and the device-reported hostname are not used, because the device controls them.
    - Settings shows and edits the list. A change that drops the caller's own node is refused; loopback callers are exempt.
    - On server: `desktop`, `laptop`, and `phone`.
  - **Never publish the port on host loopback.** Publishing `127.0.0.1:8317` could make pasta deliver host connections with a loopback source (inferred from pasta's splice path, not measured), which would make every host process an admin. Keep the `100.64.0.1` and `127.0.1.1` publishes.
- **Browser hardening.** The admin surface is keyless for admitted callers, so any page the admin visits could otherwise use the admin's tailnet address. Upstream answers every request with `Access-Control-Allow-Origin: *` and its websocket upgraders accept any origin. The guard runs before upstream's CORS middleware on both surfaces, and its 403 carries no CORS headers. It rejects:
  - a `Host` that is not one of the server's names: `localhost`, any loopback address, the server's tailnet addresses, its MagicDNS name with and without the tailnet suffix (`server`, `server.tailnet-0000.ts.net`, with or without a trailing dot), and `extra-hosts`, each with any port. This stops DNS rebinding.
  - an `Origin` whose host and port differ from the request's `Host`, or that is not http or https (including `null`). This covers CORS preflights, websocket upgrades, and every cross-site request a browser sends with a method other than `GET` or `HEAD`, including form posts and no-cors `POST`s.
  - `Sec-Fetch-Site: cross-site` or `same-site`, except top-level `GET` navigations (`Sec-Fetch-Mode: navigate`, `Sec-Fetch-Dest: document`) to the dashboard and the OAuth callbacks. Following a link to the dashboard or a provider redirect still works. **This layer only works over https or to loopback.** Browsers send `Sec-Fetch-*` only to potentially trustworthy targets, and taproot is used at `http://server:8317`, so there a cross-site no-cors `GET` or `<img>` arrives with no `Origin` and no `Sec-Fetch-*` (measured in Chrome 156 by the security recheck, F3). The other site cannot read the response, but the handler runs.
  - a state-changing `GET` without `X-Taproot-Request: 1`, from any caller but loopback. That covers the gap above: a cross-site page cannot add a custom header without a CORS preflight, and the guard rejects the preflight's `Origin`. The routes are `/v0/management/usage-queue` and `/v8/management/observability/usage/queue` (reading drains the queue), every `/v0/management/*-auth-url` and `/v8/management/oauth/auth-url` (each starts a pending OAuth login), and `/v0/management/get-auth-status` and `/v8/management/oauth/status` (which mark a finished login adopted).
    - The dashboard's management and taproot clients always send the header.
    - Read-only `GET`s need no header, so plugin pages such as Claude Keep Rolling's status page keep working. A cross-site `GET` there cannot read the response.
    - The `GET` OAuth callbacks are not on the list. Providers reach them by navigation, and they act only on a pending login whose random state the caller must know.
    - Loopback is exempt because browsers do send `Sec-Fetch-*` to loopback targets, and upstream's TUI calls these routes over `127.0.0.1` without the header.
  - The proxy surface gets the same checks, so a web page cannot spend quota either.
- **Command-line clients are unaffected.** Claude Code 2.1.316 and Codex 0.159.3 (HTTP and websocket) send no `Origin` or `Sec-Fetch-*` headers, and `Host` is the base URL's authority (measured against a header-capture server on 2026-11-02). Over the 3,000 most recent request logs of the current cliproxyapi on server, no request carried `Origin`, `Sec-Fetch-Site`, or `Referer`.
- **Framing.** The dashboard response sets `X-Frame-Options: DENY` and `Content-Security-Policy: frame-ancestors 'none'`. Other admin responses get `X-Frame-Options: SAMEORIGIN`, so only the dashboard can frame plugin pages.
- **Upstream control panel.** The guard answers `/management.html` with 404 for every caller, so upstream's auto-downloaded panel never runs same-origin with keyless management, whatever `remote-management.disable-control-panel` says.
- **Accepted:** a plugin page under `/v0/resource/plugins/**` runs same-origin with keyless management. Plugins already run in the server process with full access, so this adds nothing.

### Ingest

- **Usage events.** Ingest registers a `usage.Plugin` with `usage.RegisterNamedPlugin("taproot", ...)`. Each `usage.Record` becomes a `usage_events` row with these fields:
  - ids: request, trace, session, parent session
  - provider, model, alias, auth index and id
  - stream flag, failed flag, status code
  - latency, time to first token
  - tokens: input, output, reasoning, cache read, cache creation, and the 5m and 1h cache-creation splits
  - service tier
- **Quota observations from headers.** Each record's `ResponseHeaders` supplies quota observations:
  - Anthropic: `anthropic-ratelimit-unified-{5h,7d,...}-{utilization,reset,status}`
  - Codex: the `x-codex-*` primary and secondary used-percent, window, and reset headers
  - A row goes to `quota_observations(auth_index, provider, window, utilization, resets_at, status, observed_at)` when a value changes, or when 5 minutes have passed since the last row for that account and window.
- **Polling idle accounts.** A poller covers accounts with no recent traffic. It reads the same usage endpoints the dashboard's quota adapters read (`api.anthropic.com/api/oauth/usage`, `chatgpt.com/backend-api/wham/usage`), through the auth manager's credentials. The default interval is 15 minutes, it is configurable, and `0` disables it.
- **Account labels.** `accounts(auth_index, provider, label, email, plan, first_seen, last_seen, disabled)` is refreshed from the auth manager. It gives imported and historical rows labels.
- **Pricing.**
  - The `prices(model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, per Mtok USD, source, updated_at)` table is seeded with current list prices and is editable.
  - Cost is computed at query time, so a price edit reprices all history.
  - The dashboard labels Claude and Codex subscription costs as "API-equivalent cost".

### Request logs

- **Storage.** Upstream's file logger writes about 69 GB a day of raw text on server; one request can be 32 MB. Claude Code resends the whole conversation every turn, so records in one session share most of their bytes, and that is where the 150x zstd ratio of today's daily archives comes from.
  - Request logs are stored per session per day, as a zstd stream with a long window.
  - SQLite holds `request_logs(request_id, trace_id, session_id, ts, method, path, status, model, auth_index, req_bytes, resp_bytes, segment, offset, length)`, with offsets into the uncompressed stream.
  - Viewing a record decompresses its segment up to the record. The target is under 3 seconds for any record.
  - The open segment for the current hour stays uncompressed until it rolls.
- **Retention.** By days and total bytes, plus a path exclusion list (default excludes 404s on unknown routes, such as `HEAD /api/hello`).
- **Budget.** Compressed storage at or below 1 GB a day at server's current traffic, measured on a real sample before the format is accepted.
- **Application logs.** These stay upstream's `/v8/management/observability/logs`.

### Backups

- **Contents.** `config.yaml`, `taproot.yaml`, the auth dir, and a `store.Snapshot` of the DB go into `backups/<UTC timestamp>.tar.zst` with a manifest of SHA-256 sums.
- **Schedule and retention.** Daily at a configured local time. Keep N (default 14).
- **Endpoints.** List, create now, download, and restore.
  - Restore writes the current state to a pre-restore backup first.
  - It then replaces the config files and auth files, and restarts the database from the snapshot on the next start.
- **Security.** Backups contain OAuth tokens. They stay under the data dir with mode 0600.

### Web

- `web/` builds to one `dist/index.html` (`vite-plugin-singlefile`).
- `internal/taproot/web` embeds `internal/taproot/web/dist/index.html` and serves it at `/` with `Cache-Control: no-store`.
  - The build copies `web/dist/index.html` there.
  - A committed placeholder keeps `go build` working without a web build.
- The dashboard calls the management API at same-origin `/v8/management` with no key, and taproot at `/taproot/api/v1`.
- The login screen and the management-key flow are removed.
- Upstream's `/management.html` returns 404 from the guard. Deployment also sets `remote-management.disable-control-panel: true` so the panel is not downloaded.

## Taproot API contract (`/taproot/api/v1`)

- All endpoints return JSON.
- Times are RFC 3339 UTC.
- Time ranges use `from` and `to` query parameters (RFC 3339), and `bucket` is one of `5m|1h|1d`.
- Errors are `{error: {code, message}}`.

| Method and path | Response |
|---|---|
| `GET /me` | `{login, displayName, node, via, admin}` |
| `GET /accounts` | `{accounts: [{authIndex, provider, label, email, plan, disabled, removed, removedAt, firstSeen, lastSeen}]}`. Removed accounts stay listed for labels; pools skip them. |
| `GET /quota/current` | `{accounts: [{authIndex, provider, windows: [{window: "five_hour"\|"seven_day"\|"seven_day_opus"\|"codex_primary"\|"codex_secondary"\|..., utilization (0..1), resetsAt, status, observedAt, periodSeconds}]}]}` |
| `GET /quota/series?from&to&provider` | `{series: [{authIndex, provider, window, periodSeconds, points: [{t, utilization}], resets: [t]}]}` |
| `GET /usage/summary?from&to&bucket&groupBy=model\|account\|provider\|session` | `{totals: Totals, buckets: [{t, groups: {[key]: Totals}}]}` |
| `GET /usage/events?from&to&authIndex&model&sessionId&failed&cursor&limit` | `{events: [UsageEvent], nextCursor}` |
| `GET /usage/sessions?from&to&cursor&limit` | `{sessions: [{sessionId, firstAt, lastAt, requests, accounts: [authIndex], models: [string], totals: Totals, accountSwitches}]}` |
| `GET /prices` and `PUT /prices` | `{prices: [Price]}` |
| `GET /request-logs?from&to&q&status&model&authIndex&sessionId&cursor&limit` | `{logs: [RequestLogMeta], nextCursor}` |
| `GET /request-logs/:requestId` | `{meta, request: {headers, body}, response: {status, headers, body}, upstream: {...}}` |
| `GET /request-logs/:requestId/raw` | The raw text, as a download |
| `GET /request-logs/stats` | `{bytesOnDisk, rawBytes, oldest, newest, count}` |
| `GET /backups` | `{backups: [{id, createdAt, bytes, reason, files}]}` |
| `POST /backups` | Creates a backup now and returns the new entry |
| `GET /backups/:id/download` | The `tar.zst` file |
| `POST /backups/:id/restore` | `{restored: true, preRestoreBackupId}` |
| `GET /settings` and `PUT /settings` | The editable subset of `taproot.yaml`: retention, schedule, poller interval, `allow-logins`, `admin-nodes` |

`Totals` is `{requests, failures, inputTokens, outputTokens, reasoningTokens, cacheReadTokens, cacheWriteTokens, cacheWrite5mTokens, cacheWrite1hTokens, costUSD, latencyP50Ms, latencyP95Ms, ttftP50Ms, ttftP95Ms}`.

## Dashboard scope

- **Kept and trimmed:**
  - Dashboard
  - Auth Files (no SIWC toggle)
  - OAuth Login (Claude, Codex, Antigravity, Kimi International, xAI, Muse; no Devin, Vertex, Kimi China, or sponsor cards)
  - Routing (OAuth credentials only)
  - Config (no API keys, no BestProxy hint)
  - Plugins and Plugin Store
  - System
- **New:** Quota (replaces the old quota page), Usage, Request Logs (replaces the logs viewer, keeping app logs as a tab), Backups, Settings (access allowlist, retention, schedule).
- **Removed:** Quick Start, AI Providers, every sponsor, the login page, and the non-English locales (English only; the i18n layer stays, and each feature owns `src/i18n/locales/en/<feature>.json`).
- **Style:** the reference screenshot.
  - Near-black background, quiet borders, and large numerals for the headline figures.
  - Provider accents: Claude orange, Codex blue.
  - A collapsible left nav.
  - Recharts for charts.

## Workstreams and ownership

| ID | Owner writes | Depends on |
|---|---|---|
| S0 scaffold (done) | `internal/taproot/{taproot.go,store,PLAN.md}`, `cmd/server/main.go` hook | none |
| A1 ingest | `internal/taproot/ingest/**` | S0 |
| A2 access | `internal/taproot/access/**` | S0 |
| A3 request logs | `internal/taproot/reqlog/**` | S0 |
| A4 backups and web embed | `internal/taproot/backup/**`, `internal/taproot/web/**`, `scripts/taproot/**`, `Containerfile.taproot` | S0 |
| A5 dashboard strip and shell | `web/**` except new feature folders | S0 |
| B1 quota page | `web/src/features/taproot-quota/**` | A1 contract, A5 shell |
| B2 usage page | `web/src/features/taproot-usage/**` | A1 contract, A5 shell |
| B3 request logs, backups, and settings pages | `web/src/features/taproot-{logs,backups,settings}/**` | A3, A4 contract, A5 shell |

- **Coordinator-only files:** `go.mod`, `go.sum`, `taproot.go`, `web/package.json`, `bun.lock`, route registration, and nav.
  - Workers that need a dependency list it in their result. The coordinator adds it.
- **Exception: A5** owns `web/package.json` and `bun.lock` during phase A.

## Verification

- **Go.** `gofmt`, `go vet`, `go build`, and `go test ./internal/taproot/...`. `go test ./...` runs before each integration.
- **Web.** `bun run verify`. The baseline has 8 timezone test failures on Windows from upstream; those files get deleted or replaced with the old quota timeline.
- **Real-surface check.** The real binary, driven in a browser for every page. Development data comes from real or fake-upstream traffic; screenshots go in the result.
- **Review.** A fresh-context review of access first, because it decides who can read OAuth tokens.

## Decisions after phase A

- **No data migration.** Taproot starts fresh. The Usage Keeper importer that phase A built for test data was removed.
- **Request log segments.** Segments roll on idle (15m), age (6h), day change, or 64 MiB raw, not hourly. On the real fixture, per-session segments compressed 97x, while per-session-hour segments compressed 73x. The 64 MiB cap keeps the worst lookup under 50 ms.
- **Plugin resource routes** (`/v0/resource/plugins/*`) are guarded but get no injected key. Upstream serves them unauthenticated and forwards headers to plugin code.
- **RESP usage queue.** Upstream multiplexes this Redis-protocol queue on the proxy port, outside gin, so the guard never sees it, and the generated `MANAGEMENT_PASSWORD` enables remote `AUTH`. The security recheck (F2) showed a tailnet peer authenticating with the secret. Taproot does not use the queue, so the RESP connection filter (see "Upstream boundary") closes every RESP connection that does not come from loopback, before any reply. Drop the hook if upstream adds a switch to disable the queue.
- **Poller timeout.** The idle-account poller's 30 s timeout on its read-only usage probe is an intended exception to AGENTS.md's no-timeout rule. That rule targets proxied upstream traffic.
- **Shutdown drain.** Setup runs on `context.Background()`, so request-log records still queued at shutdown are recovered from the open segment on the next start rather than drained. Usage events still queued are lost. The flush interval is 1 s.
- **Mounts on server.** On server, `config.yaml` lives in the mounted `/data` directory, which is what restore's atomic rename needs. The tailscaled socket must be mounted at `/var/run/tailscale/tailscaled.sock`. After deploying, `GET /taproot/api/v1/me` from a tailnet client listed in `admin-nodes` must report `via: "tailnet"` and `admin: true`, and the same request from worker must get 403 `not_admin_node`. If it reports loopback, the port forwarding is rewriting source addresses and the guard would admit everyone. From server itself, `curl http://100.64.0.1:8317/taproot/api/v1/me` must return 403 and `curl http://100.64.0.1:8317/v1/models` must not, which shows the server's own node is proxy-only. `127.0.1.1:8317` from server gets 403 on both unless `192.168.0.88/32` is in `trusted-cidrs` (see "Security model").
