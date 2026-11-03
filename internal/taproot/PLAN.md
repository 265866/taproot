# Taproot plan

Taproot is the owner's fork of CLIProxyAPI. It runs on server and serves every Claude and Codex account to his tailnet through one endpoint, with one dashboard that covers credentials, quota, usage and cost, request logs, and backups.

## Outcome and acceptance

- One binary and one container replace both `cliproxyapi` and `cpa-usage-keeper` on server.
- Only allowlisted Tailscale logins reach the proxy, the management API, or the dashboard. Nobody types a key or password.
- The dashboard has no Quick Start, AI Providers, sponsor or affiliate content, Kimi China, Devin, Vertex, SIWC, or config API-key editing.
- Quota page in the style of the reference screenshot (`internal/taproot/docs/quota-reference.png`): pooled remaining percentage per provider, recorded and projected lines, pool empty, first empty, next reset, and burn rate over 1h, 3h, or 6h.
- Usage section: cost, requests, tokens including cache reads and writes, latency, time to first token, and failures, broken down by account, model, and session.
- Request logs are compressed, indexed, searchable, and pruned to a budget. Backups run on a schedule, and you can download or restore them from the dashboard.
- CPA Usage Keeper's history (from 2026-09-09) is imported.
- Upstream merges stay cheap. See "Upstream boundary".

## Upstream boundary

- The Go module path stays `github.com/router-for-me/CLIProxyAPI/v8`, so upstream merges never rewrite imports.
- Fork code lives in `internal/taproot/**`. The only upstream file it edits is `cmd/server/main.go`, with one `taproot.Setup` call. Any further upstream edit needs a written reason in this file.
- The removed providers come out of the dashboard only. Their backend code stays, because deleting it would conflict with every upstream merge.
- `web/` is a hard fork of Cli-Proxy-API-Management-Center at `f154fa9`, imported with `git subtree`. The fork stops following upstream UI changes. It follows the backend's management API instead.
- `scripts/taproot/sync-upstream.sh` merges `upstream/main` and runs the checks.

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
| ingest | `internal/taproot/ingest` | Usage events, quota observations, pricing, Usage Keeper import |
| reqlog | `internal/taproot/reqlog` | Request log storage, index, retention |
| backup | `internal/taproot/backup` | Scheduled snapshots, download, restore |
| web | `internal/taproot/web` | The embedded dashboard build |

### Access

- **Identifying callers.** For every request, the guard resolves the caller from its source IP:
  - Loopback addresses and `trusted-cidrs` are trusted.
  - Tailnet addresses (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) are resolved with tailscaled's LocalAPI `whois` over its unix socket (default `/var/run/tailscale/tailscaled.sock`, mounted into the container). The result is cached for one minute.
  - The caller is admitted when the login is in `allow-logins`. The default list holds only `alice@github`.
  - Everything else gets a 403, including whois failures. `/healthz` is exempt.
- **Why source IPs and not `tailscale serve` headers.** Source IPs work for the proxy port, so client configs stay as they are, and tailnet source addresses can't be forged.
- **Keyless management.** At setup, the guard puts a random secret in `MANAGEMENT_PASSWORD`, unless an operator already set one. Upstream's management handler reads that at construction. For admitted callers, the guard injects `Authorization: Bearer <secret>` on `/v0/management/*`, `/v8/management/*`, and plugin resource routes. The secret never leaves the process. A `secret-key` in `config.yaml` keeps working for scripts.
- **Proxy keys.** Proxy API keys (`access.api-keys`) are irrelevant once the guard admits callers by identity. They stay empty.
- **Development.** On Windows there's no tailscaled socket, so only loopback is admitted.
- `GET /taproot/api/v1/me` returns `{login, displayName, node, via: "loopback"|"tailnet"|"cidr"}`.

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
- **Import.** `taproot-import-usage-keeper <path to app.db>` is a flag on the server binary. It runs once and is idempotent by event key. It maps:
  - `usage_events` to taproot's `usage_events`
  - `quota_cycles` plus `quota_percent_segments` to quota observations
  - `model_price_settings` to prices

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
- Upstream's `/management.html` is turned off in deployment with `remote-management.disable-control-panel: true`.

## Taproot API contract (`/taproot/api/v1`)

- All endpoints return JSON.
- Times are RFC 3339 UTC.
- Time ranges use `from` and `to` query parameters (RFC 3339), and `bucket` is one of `5m|1h|1d`.
- Errors are `{error: {code, message}}`.

| Method and path | Response |
|---|---|
| `GET /me` | `{login, displayName, node, via}` |
| `GET /accounts` | `{accounts: [{authIndex, provider, label, email, plan, disabled, firstSeen, lastSeen}]}` |
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
| `GET /settings` and `PUT /settings` | The editable subset of `taproot.yaml`: retention, schedule, poller interval, `allow-logins` |

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
- **Real-surface check.** The real binary with an imported copy of server's Usage Keeper database, driven in a browser for every page. Screenshots go in the result.
- **Review.** A fresh-context review of access first, because it decides who can read OAuth tokens.

## Decisions after phase A

- **Importer.** The importer is a separate binary, `cmd/taproot-import` (`-config`, `-usage-keeper`). The server's flags stay upstream's.
- **Request log segments.** Segments roll on idle (15m), age (6h), day change, or 64 MiB raw, not hourly. On the real fixture, per-session segments compressed 97x, while per-session-hour segments compressed 73x. The 64 MiB cap keeps the worst lookup under 50 ms.
- **Plugin resource routes** (`/v0/resource/plugins/*`) are guarded but get no injected key. Upstream serves them unauthenticated and forwards headers to plugin code.
- **RESP usage queue.** Upstream multiplexes this Redis-protocol queue on the proxy port, outside gin, so the guard never sees it. The generated `MANAGEMENT_PASSWORD` enables it. Accepted risk: AUTH needs the random 256-bit secret, and upstream bans an IP after 5 failures. Taproot does not use the queue. Revisit if upstream adds a switch to disable it.
- **Poller timeout.** The idle-account poller's 30 s timeout on its read-only usage probe is an intended exception to AGENTS.md's no-timeout rule. That rule targets proxied upstream traffic.
- **Shutdown drain.** Setup runs on `context.Background()`, so request-log records still queued at shutdown are recovered from the open segment on the next start rather than drained. Usage events still queued are lost. The flush interval is 1 s.
- **Mounts on server.** On server, `config.yaml` lives in the mounted `/data` directory, which is what restore's atomic rename needs. The tailscaled socket must be mounted at `/var/run/tailscale/tailscaled.sock`. After deploying, `GET /taproot/api/v1/me` from a tailnet client must report `via: "tailnet"`. If it reports loopback, the port forwarding is rewriting source addresses and the guard would admit everyone.
