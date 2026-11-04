# Taproot plan

Taproot is a fork of CLIProxyAPI. It runs on one server and serves every Claude and Codex account to its owner's tailnet through one endpoint, with one dashboard that covers credentials, quota, usage and cost, request logs, and backups.

## Outcome and acceptance

- Taproot is a new service with its own data. Nothing is migrated: usage and quota history start with taproot. When it replaces another proxy, that proxy is stopped before its auth files are copied in, so the two proxies never refresh the same OAuth tokens.
- Only allowlisted Tailscale logins reach the proxy, the management API, or the dashboard. Nobody types a key or password.
- The dashboard has no Quick Start, AI Providers, sponsor or affiliate content, Kimi China, Devin, Vertex, SIWC, or config API-key editing.
- Quota, on the Accounts page: pooled remaining percentage per provider as a chart of recorded and projected lines, with large figures for pool empty, first empty, next reset, and burn rate over 1h, 3h, or 6h.
- Usage section: cost, requests, tokens including cache reads and writes, latency, time to first token, and failures, broken down by account, model, and session.
- Request logs are compressed, indexed, searchable, and pruned to a budget. Backups run on a schedule, and you can download or restore them from the dashboard.
- Upstream merges stay cheap. See "Upstream boundary".

## Upstream boundary

- The Go module path is `github.com/265866/taproot`. Upstream's is `github.com/router-for-me/CLIProxyAPI/v8`.
  - `scripts/taproot/rewrite-module-path.sh` rewrites the one into the other in every tracked `.go` file, `go.mod`, and `go.sum`, and re-sorts imports with the toolchain's `gofmt`. A path without a `/vN` suffix takes only v0 or v1 versions, so nested modules that require the root (`examples/plugin/*/go`, which replace it with a local directory) get `v0.0.0`. The rewrite must stay deterministic: the merges below depend on it giving the same output for the same input.
  - The rename commit went through that rewrite, and its second parent is upstream `6e6afab99` put through the same rewrite. That snapshot commit carries a `Taproot-Upstream: <upstream commit>` trailer.
  - `scripts/taproot/sync-upstream.sh` fetches `upstream/main`, rewrites its tip in a temporary worktree, and commits the result to the staging branch `taproot/upstream`. The staging commit has two parents: upstream's tip and the previous rewritten snapshot, which the script finds by its trailer in HEAD's history. The script then merges the staging branch. The merge base is the previous snapshot, which already has the new path, so upstream's changes arrive with the new path and only real conflicts remain. A plain merge of upstream would instead conflict wherever upstream touched an import block.
  - Tested in a throwaway clone with two simulated upstream commits that added and changed imports in a file taproot edits (`cmd/server/main.go`), in a file taproot never touched, and in a new package, and that changed the deleted `Dockerfile`. Both merged with no import conflicts and no old path left, and the `Dockerfile` stayed deleted. A plain merge of the first commit conflicted in both Go files.
  - Plugins do not depend on the path. The host loads a native plugin over a C ABI: `dlopen`, the `cliproxy_plugin_init` symbol, the ABI version from `sdk/pluginabi`, and method names with byte buffers. No Go package path crosses that boundary, so plugins built against upstream's module path, such as Claude Keep Rolling, load unchanged. Checked by loading the `codex-service-tier` example, built from upstream `6e6afab99` with the old path, into the renamed image: the host loaded and registered it and read its metadata.
- Fork code lives in `internal/taproot/**`. Apart from the module-path rewrite and the small hooks listed below, the upstream files it edits are `cmd/server/main.go`, with one `taproot.Setup` call and one `taproot.CheckServerMode` call, and `internal/tui/client.go`, which sends `X-Taproot-Request: 1` on every request (one line; see "Security model"). Any further upstream edit needs a written reason in this file.
  - `CheckServerMode` (launch audit S4) makes the binary exit before Home mode (`-home-jwt`) or `--tui --standalone` starts a server. Both paths start the server without reaching `taproot.Setup`, so without the guard. The call sits right after the flags and `HOME_JWT` are read, about 4 lines, so a Home JWT is refused, with exit status 1, before main parses it, fetches config or plugins from Home, or starts anything (review finding 8). A TUI client given a Home JWT is refused too, because main contacts Home before it knows the mode. The `if !homeMode` that used to wrap `Setup` is gone because Home mode no longer gets there.
- The removed providers come out of the dashboard only. Their backend code stays, because deleting it would conflict with every upstream merge.
- `web/` is a hard fork of Cli-Proxy-API-Management-Center at `f154fa9`, imported with `git subtree`. The fork stops following upstream UI changes. It follows the backend's management API instead.
- `scripts/taproot/sync-upstream.sh` merges the rewritten `upstream/main` (see above) and runs the checks.
- Taproot deleted upstream files a stranger would mistake for taproot's (launch audit S1 and S2): the GitHub workflows, funding file, issue template, and catalog-refresh script under `.github/`; `Dockerfile`, `docker-compose.yml`, `docker-compose.cluster.yml`, `docker-build.sh`, and `docker-build.ps1`; `README_CN.md`, `README_JA.md`, and the sponsor images in `assets/`; and the Management Center's READMEs, logo, and workflows under `web/`. `.github/workflows/ci.yml` and `Containerfile.taproot` replace them.
  - When upstream changes a file taproot deleted, the merge stops on a modify/delete conflict. The sync script resolves those as "keep deleted" (`git rm`) and stops on every other conflict. `--dry-run` lists both kinds. A file upstream deleted that taproot changed is still a conflict for a person to resolve.
- `sdk/cliproxy/service_config.go` maps `routing.strategy: soonest-reset-first` to `internal/taproot/routing.SoonestResetSelector` (7 lines).
  - Upstream builds the credential selector only there, and `routing.session-affinity` wraps whatever it builds.
  - The plugin-scheduler hook would bypass session affinity, and the plugin host overwrites it.
- `internal/api/protocol_multiplexer.go` gains a package-level RESP connection filter (`SetRESPConnectionFilter`, `RESPConnectionAllowed`, about 25 lines). The multiplexer consults it before a Redis-protocol connection reaches `handleRedisConnection` and closes a refused connection before any reply. Taproot's `Setup` installs `access.RESPFilter`, which admits loopback only.
  - Upstream serves its RESP usage queue on the proxy port outside gin, so the guard never sees it, and the guard's generated `MANAGEMENT_PASSWORD` turns on remote `AUTH` there. Without the hook, any host that reaches the port could `AUTH` with the management secret or a `config.yaml` `secret-key` and drain the usage and error queues.
  - No server option or middleware runs at that point, so a package-level hook is the smallest seam. `internal/api/taproot_resp_filter_test.go` drives it through the real multiplexer.
- `internal/pluginhost/taproot_caller.go` is a new file (no upstream file changes) that exports `CallerPlugin(ctx)` and `(*Host).PluginName(id)`, about 25 lines.
  - A plugin that calls `host.model.execute` runs the model in-process, so no HTTP request carries a device. The host tags the callback context with the plugin's ID under an unexported key, and usage records reach ingest with that context. `CallerPlugin` reads the key; nothing else exposes it.
  - `PluginName` reads the active plugin's metadata name. Upstream's `RegisteredPlugins` also calls into each plugin for OAuth and quota identifiers, which ingest's flush must not do.
  - Wrapping the host's model executor instead would need no new file, but upstream resets the executor on every config reload (`internal/api/server_reload.go`).
  - `internal/pluginhost/taproot_caller_test.go` drives `host.model.execute` and checks the context the model executor receives.

## Soonest-reset-first routing

- **Order.** New sessions and failovers go to the eligible account whose weekly window resets soonest:
  - Claude uses `anthropic-ratelimit-unified-7d-reset`.
  - Codex uses its 10080-minute window.
  - Weekly resets are fixed per account, so a past reset rolls forward by whole weeks.
- **Skipped and unknown accounts.** Accounts at or above 100% weekly utilization are skipped. Accounts whose responses carry no weekly reset sort last. Ties use round-robin.
- **Session affinity first.** Bound sessions keep their account.
- **Probing after a restart.** Upstream keeps quota headers in memory only. After a restart, each Claude or Codex account with no observed headers gets one new session first, so its reset becomes known. Without that, the first account to answer would take every session.

## Architecture

`taproot.Setup` (in `internal/taproot/taproot.go`) reads `taproot.yaml` from beside `config.yaml`, or from the path in `TAPROOT_CONFIG`. Upstream's config parser rejects unknown keys, so taproot's settings can't live in `config.yaml`. Taproot decodes `taproot.yaml` strictly too: an unknown key stops startup with its name, so a misspelled access setting cannot fall back silently. Setup then opens the modules and returns `api.ServerOption`s:

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
| alerts | `internal/taproot/alerts` | Discord alerts and their dedupe state |
| web | `internal/taproot/web` | The embedded dashboard build |

### Access

- **Identifying callers.** For every request, the guard resolves the caller from its source IP:
  - Loopback addresses and `trusted-cidrs` are trusted, with the limits in "Security model".
  - Tailnet addresses (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) are resolved with tailscaled's LocalAPI `whois` over its unix socket (default `/var/run/tailscale/tailscaled.sock`, mounted into the container). The result is cached for one minute.
  - The caller is admitted when the login is in `allow-logins`. There is no default list: an enabled guard refuses to start while `allow-logins` is empty.
  - An admitted tailnet caller is an admin only when its node is in `admin-nodes` (see "Security model").
  - Everything else gets a 403, including whois failures. `/healthz` is exempt.
- **Why source IPs and not `tailscale serve` headers.** Source IPs work for the proxy port, so client configs stay as they are, and tailnet source addresses can't be forged.
- **Keyless management.** At setup, the guard puts a random secret in `MANAGEMENT_PASSWORD`, unless an operator already set one. Upstream's management handler reads that at construction. For admitted callers, the guard injects `Authorization: Bearer <secret>` on `/v0/management/*`, `/v8/management/*`, and plugin resource routes. The secret never leaves the process. A `secret-key` in `config.yaml` keeps working for scripts.
- **Proxy keys.** Proxy API keys (`access.api-keys`) are irrelevant once the guard admits callers by identity. They stay empty.
- **Development.** On Windows there's no tailscaled socket, so only loopback is admitted.
- **Guard disabled.** With `access.enabled: false`, the proxy surface admits every caller with no Host or browser checks, and the admin surface still answers loopback only (launch audit B2: before this, a remote caller read settings and created and downloaded a backup). Loopback admin requests still get the browser checks (Host, Origin, `Sec-Fetch-Site`, and `X-Taproot-Request` on state-changing GETs), because a browser on the same host connects from loopback too (review finding 2: without them, any page could create, download, or restore a backup). No key is injected, so management auth is upstream's. The startup warning names both surfaces.
- `GET /taproot/api/v1/me` returns `{login, displayName, node, via: "loopback"|"tailnet"|"cidr", admin}`. It is on the admin surface, so only admins reach it.
- **Denials the dashboard can explain.** The guard's 403 is `{error: {code, message}}`. The code is `forbidden`, except for two cases:
  - `not_admin_node`, with `node`, for an allowlisted login on a node outside `admin-nodes`. The dashboard's connection gate then tells the user to add that node to `access.admin-nodes` in `taproot.yaml`.
  - `request_header_required` for a state-changing GET without `X-Taproot-Request: 1` (see "Security model").

### Security model

The guard splits the server into two surfaces. The package comment in `internal/taproot/access/access.go` is the code-side copy of this section.

- **Admin surface.** Everything that reads or changes credentials, settings, backups, or plugins:
  - the dashboard (`GET`/`HEAD /`), except that the static page itself loads for every admitted caller (see the note under the table)
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
  | Allowlisted login on the server's own node (`server`, `100.64.0.1`) | yes | no |
  | Allowlisted login on another node listed in `admin-nodes` | yes | yes |
  | Allowlisted login on a node outside `admin-nodes` (another server or a router of the same login) | yes | no |
  | Anyone else | no | no |

  - **The dashboard page is open to every admitted caller.** `GET`/`HEAD /` returns the static single-file build, which holds no data. A device outside `admin-nodes` can therefore load it, and the connection gate shows why the API refuses it. Every request for data is still admin-only.

  - **The server's own node is proxy-only.** Under rootless Podman, containers and local users on the server host reach the published `100.64.0.1:8317` with that source address (measured through pasta), and whois says that node belongs to the owner's login, such as `alice@github`. Agents running on the server need the proxy, so the node keeps it, but none of those processes gets credentials or settings.
  - **The server's own node comes from tailscaled.** The guard reads `Self` from `GET /localapi/v0/status?peers=false` and treats a caller as the server when its address is in `Self.TailscaleIPs` or its whois `Node.StableID` equals `Self.ID`. It refreshes every cache TTL and keeps the last known node when a refresh fails. Until the first read succeeds, tailnet callers get no admin access, only local Host names pass the Host check, and a warning is logged.
  - **`trusted-cidrs` are proxy-only.** They hold host-local addresses that every local process shares. Under rootless Podman, a host-local client that uses a `127.0.1.1:8317` publish arrives as the host's LAN address, such as `192.168.0.88` (measured through pasta). If such a client needs the proxy, add `192.168.0.88/32`, never `192.168.0.0/24`, which would admit the whole LAN. Local clients can also use the Tailscale publish, `100.64.0.1:8317`, instead.
  - **`admin-nodes`** lists the tailnet nodes that reach the admin surface. Admin is deny-by-default: with the list empty, no tailnet caller is an admin and only loopback is, and startup logs a warning. An owner's other server nodes and routers usually resolve to the same login, so any process, compromised container, or SSRF on them would otherwise get the owner's tokens and settings (security recheck F1).
    - An entry is either a node's stable ID as `id:<StableID>` (preferred; `GET /taproot/api/v1/me` reports it as `nodeId`), or a MagicDNS name, either full (`laptop.tailnet-0000.ts.net`) or its first label for nodes in the server's tailnet (`laptop`).
    - Names are matched against whois `Node.Name` only, never `ComputedName`. `Node.Name` still derives from the device's hostname unless it was set in the admin console, and names are unique only while held. After a listed device is deleted or renamed, another node of the same login can claim its name with `tailscale set --hostname` and become an admin (security re-review F1). A stable ID is never reused, so ID pins do not have this gap.
    - Settings shows and edits the list. A change that drops the caller's own node, by name or by ID, is refused; loopback callers are exempt.
    - Pin each admin device, such as a laptop and a phone, by its stable ID from `tailscale status --json` (`Peer[].ID`).
  - **Never publish the port on host loopback.** Publishing `127.0.0.1:8317` could make pasta deliver host connections with a loopback source (inferred from pasta's splice path, not measured), which would make every host process an admin. Publish on the host's Tailscale address (and `127.0.1.1` if host-local clients need it) instead.
- **Browser hardening.** The admin surface is keyless for admitted callers, so any page the admin visits could otherwise use the admin's tailnet address. Upstream answers every request with `Access-Control-Allow-Origin: *` and its websocket upgraders accept any origin. The guard runs before upstream's CORS middleware on both surfaces, and its 403 carries no CORS headers. It rejects:
  - a `Host` that is not one of the server's names: `localhost`, any loopback address, the server's tailnet addresses, its MagicDNS name with and without the tailnet suffix (`server`, `server.tailnet-0000.ts.net`, with or without a trailing dot), and `extra-hosts`, each with any port. This stops DNS rebinding.
  - an `Origin` whose host and port differ from the request's `Host`, or that is not http or https (including `null`). This covers CORS preflights, websocket upgrades, and every cross-site request a browser sends with a method other than `GET` or `HEAD`, including form posts and no-cors `POST`s.
  - `Sec-Fetch-Site: cross-site` or `same-site`, except top-level `GET` navigations (`Sec-Fetch-Mode: navigate`, `Sec-Fetch-Dest: document`) to the dashboard and the OAuth callbacks. Following a link to the dashboard or a provider redirect still works. **This layer only works over https or to loopback.** Browsers send `Sec-Fetch-*` only to potentially trustworthy targets, and taproot is used at a plain-http tailnet name such as `http://server:8317`, so there a cross-site no-cors `GET` or `<img>` arrives with no `Origin` and no `Sec-Fetch-*` (measured in Chrome 156 by the security recheck, F3). The other site cannot read the response, but the handler runs.
  - a state-changing `GET` without `X-Taproot-Request: 1`, from any caller, loopback included. That covers the gap above: a cross-site page cannot add a custom header without a CORS preflight, and the guard rejects the preflight's `Origin`. The routes are `/v0/management/usage-queue` and `/v8/management/observability/usage/queue` (reading drains the queue), every `/v0/management/*-auth-url` and `/v8/management/oauth/auth-url` (each starts a pending OAuth login), and `/v0/management/get-auth-status` and `/v8/management/oauth/status` (which mark a finished login adopted). The outbound fetches are on the list too: the plugin store (`/v0/management/plugin-store`, `/v8/management/plugins/store`), the latest release (`/v0/management/latest-version`, `/v8/management/server/latest-version`), and plugin quota probes (`/management/plugins/:id/quota`), which use stored credentials.
    - Plugin management `GET`s are assumed read-only. Upstream lets a plugin register any `GET` handler, so a plugin that acts on a `GET` could be triggered cross-site. Claude Keep Rolling's only action, a manual ping, is a `POST`.
    - Gin's trailing-slash redirects run before any middleware, so a stranger can learn which routes exist from a `301`/`307` `Location`. No data comes back, and the followed request is guarded.
    - The dashboard's management and taproot clients always send the header.
    - Read-only `GET`s need no header, so plugin pages such as Claude Keep Rolling's status page keep working. A cross-site `GET` there cannot read the response.
    - The `GET` OAuth callbacks are not on the list. Providers reach them by navigation, and they act only on a pending login whose random state the caller must know.
    - Loopback needs the header too (review finding 9), so the check does not depend on the browser sending `Sec-Fetch-*` to loopback targets. Upstream's TUI client calls these routes, so `internal/tui/client.go` sends the header on every request: one line in `doRequest`, an upstream edit for this reason.
  - The proxy surface gets the same checks, so a web page cannot spend quota either.
- **Command-line clients are unaffected.** Claude Code 2.1.316 and Codex 0.159.3 (HTTP and websocket) send no `Origin` or `Sec-Fetch-*` headers, and `Host` is the base URL's authority (measured against a header-capture server on 2026-11-02). Over the 3,000 most recent request logs of a production deployment, no request carried `Origin`, `Sec-Fetch-Site`, or `Referer`.
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
  - device: the caller the access guard admitted (`node:<StableID>` for a tailnet node, `local` for loopback and the server's own node, `cidr:<address>` for trusted-cidrs), with its name, login, and how it was reached. Ingest migration 4 added these columns and an hourly device rollup backfilled from existing events. Events recorded before it have no device.
  - **Plugins.** A plugin's in-process model request (`host.model.execute`) has no HTTP caller. It gets the device `plugin:<plugin ID>`, named after the plugin's metadata name (for example "Claude Keep Rolling"), with via `plugin`. When the host marks a plugin call without its ID, the device is `plugins`, "In-process plugins". Limits: upstream's non-streaming `host.model.execute` does not check that a callback ID belongs to the calling plugin (the streaming path does), so a plugin can attribute its usage to another plugin or to that plugin's client; native plugins already run in-process with full access, so this is accepted. With the guard disabled, a client request a plugin forwards is credited to the plugin. Tests build the loaders' context by hand; if upstream stops tagging it, plugin usage falls back to an empty device without a failing test.
    - The admitted HTTP caller wins when the guard knows the record's request ID. A plugin that executes a client's request through the host, such as `claude-web-search-router`'s fallback, carries that request's ID, and the usage is the client's. Upstream assigns request IDs only under `/v1`, `/v1beta`, `/openai/v1`, and `/backend-api/codex`, so a plugin's timer or a management or plugin-page action never has one.
    - Stored device IDs are never rewritten. Claude Keep Rolling pings recorded before plugin attribution keep an empty device. The dashboard labels that group "No caller recorded", which is true of every empty-device event. Mapping them to the plugin at read time would rest on their shape (Haiku, 100 input and 1 output tokens, no request ID), and other callers without a request ID, such as routes outside those prefixes, share the missing ID.
  - **Names at read time.** For trusted-cidrs devices, the devices and events APIs ask the guard for the current name, so imported history shows it too; plugin devices keep the name stored when their usage was recorded. Read-time naming also renames past rows if an address later changes hands. The request path stores the bare address and never reads the interfaces. A trusted-cidrs device whose address is one of the server's own interface addresses is named `<server> (via <address>)`, such as "server (via 192.168.0.88)": only a process on the server host connects from that address (under rootless Podman, a `127.0.1.1` publish arrives through pasta as the LAN address). The server cannot see which publish was used, so the name says what it knows: the host and the address. The address also separates the row from "server (local)" and matches the `trusted-cidrs` entry. The guard reads the interfaces with `net.InterfaceAddrs` at most once a minute and keeps the last list when a read fails. The device ID stays `cidr:<address>` and access stays proxy-only; the own-address check names devices and never admits.
- **Quota observations from headers.** Each record's `ResponseHeaders` supplies quota observations:
  - Anthropic: `anthropic-ratelimit-unified-{5h,7d,...}-{utilization,reset,status}`
  - Codex: the `x-codex-*` primary and secondary used-percent, window, and reset headers
  - A row goes to `quota_observations(auth_index, provider, window, utilization, resets_at, status, observed_at)` when a value changes, or when 5 minutes have passed since the last row for that account and window.
- **Polling idle accounts.** A poller covers accounts with no recent traffic. It reads the same usage endpoints the dashboard's quota adapters read (`api.anthropic.com/api/oauth/usage`, `chatgpt.com/backend-api/wham/usage`), through the auth manager's credentials. The default interval is 15 minutes, it is configurable, and `0` disables it.
- **Manual refresh.** `POST /quota/refresh` polls one account's usage endpoint on request, at most once per 30 seconds per account. Refreshed observations are always stored, so the account's `observedAt` moves even when nothing changed.
- **Development overrides.** `TAPROOT_DEV_CLAUDE_USAGE_URL` and `TAPROOT_DEV_CODEX_USAGE_URL` replace the two usage endpoints, so a local dashboard can exercise quota refresh against a fake server. Only loopback URLs (`127.0.0.1`, `localhost`, `::1`) are accepted; any other value is ignored with a warning, so a stray variable cannot send tokens off the machine.
- **Account labels.** `accounts(auth_index, provider, label, email, plan, first_seen, last_seen, disabled)` is refreshed from the auth manager. It gives imported and historical rows labels.
- **Pricing.**
  - The `prices(model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, per Mtok USD, source, updated_at)` table is seeded with current list prices and is editable.
  - Cost is computed at query time, so a price edit reprices all history.
  - The dashboard labels Claude and Codex subscription costs as "API-equivalent cost".

### Request logs

- **Storage.** Upstream's file logger wrote about 69 GB a day of raw text on the first deployment; one request can be 32 MB. Claude Code resends the whole conversation every turn, so records in one session share most of their bytes, and that is where the 150x zstd ratio of today's daily archives comes from.
  - Request logs are stored per session per day, as a zstd stream with a long window.
  - SQLite holds `request_logs(request_id, trace_id, session_id, ts, method, path, status, model, auth_index, req_bytes, resp_bytes, segment, offset, length)`, with offsets into the uncompressed stream.
  - Viewing a record decompresses its segment up to the record. The target is under 3 seconds for any record.
  - The open segment for the current hour stays uncompressed until it rolls.
- **Retention.** By days and total bytes, plus a path exclusion list (default excludes 404s on unknown routes, such as `HEAD /api/hello`).
- **Budget.** Compressed storage at or below 1 GB a day at the first deployment's traffic, measured on a real sample before the format is accepted.
- **Application logs.** These stay upstream's `/v8/management/observability/logs`.

### Backups

- **Contents.** `config.yaml`, `taproot.yaml`, the auth dir, and a `store.Snapshot` of the DB go into `backups/<UTC timestamp>.tar.zst` with a manifest of SHA-256 sums.
- **Schedule and retention.** Daily at a configured local time. Keep N (default 14).
- **Endpoints.** List, create now, download, and restore.
  - Restore writes the current state to a pre-restore backup first.
  - It then replaces the config files and auth files, and restarts the database from the snapshot on the next start.
- **Security.** Backups contain OAuth tokens. They stay under the data dir with mode 0600.

### Alerts

Taproot posts to one Discord channel through a webhook. The webhook URL is the only setting.

- **Config.** `alerts.discord-webhook` in `taproot.yaml`, plus `alerts.enabled` (default on when a webhook is set). Settings shows whether a webhook is set by its last six characters only, and sets, replaces, or clears it. The server accepts only `https://discord.com/api/webhooks/<id>/<token>` and its `discordapp.com`, `ptb.`, `canary.`, and `/api/v<n>/` forms. The URL is a credential: it is never logged, never returned by the API, and lives only in `taproot.yaml` (and so in backups, which are 0600). An invalid webhook in a hand-edited `taproot.yaml` does not stop the server: alerts log an error without the URL, stay off, and settings report `webhookInvalid` until the webhook is replaced or cleared.
- **Scope.** Alerts watch the accounts the manager lists and the user has not disabled. Upstream sets `Disabled` and `StatusDisabled` only from the auth file's `disabled` switch, so a disabled account is out of scope whatever its last error. When an account is removed or disabled, its open alerts, and its provider's pool alert once the provider has no accounts left, are dropped without a recovery.
- **Events.** Each has a stable dedupe key.

  | Event | Key | Fires when | Recovery |
  |---|---|---|---|
  | Limit hit | `limit:<authIndex>:<window>` | A window's latest observation is `rejected` or at utilization 1 or more, and its reset is still ahead | The window is below its limit, or its reset passed |
  | Pool empty | `pool:<provider>` | Every account of the provider is at an account-wide limit or has a broken login that no longer serves. When a broken login is part of the reason, the condition must hold for 2 minutes, like the login alert. The message names the first account back and when | One account is usable |
  | Login broken | `login:<authIndex>` | For 2 minutes, upstream marks the credential `RequiresSignIn` or a terminal 401 (`HasUnauthorizedAuthFailure`). The message carries upstream's sanitized reason. Upstream keeps serving a `RequiresSignIn` account while its access token is valid (`canServeWithAccessToken`), so then the message says it keeps working until the token expires, and the account still counts as usable for the pool | The credential works again |
  | Reset grant expiring | `grant:<authIndex>:<grantId>` | A Claude `cedar_ember` grant has resets left and ends within 24 hours. It warns once per grant: the episode stays open until the grant ends, even while a read is missing | None |

  The login alert warns as soon as the refresh credential is rejected, rather than waiting for the access token to expire, so there is time to sign in again before the account stops serving.
- **Flapping.** A key alerts once per episode. A recovery waits until the condition stays clear for 2 minutes, so a shorter flap continues the episode. A new alert for the same key waits 30 minutes after the previous one. The detector's state is stored in `alert_state`, so a restart neither repeats an alert nor drops a pending recovery. Upstream keeps `RequiresSignIn` and `RefreshError` in memory only, so after a restart a broken login looks healthy until a refresh fails again. A login alert that was open at the restart therefore stays open, for the login and the pool, until a refresh succeeds after startup or the account's access token changes.
- **Sources.** Quota comes from ingest in-process: `Ingest.OnQuota` publishes every observation of a committed flush, and `LatestQuota` seeds the module at start. Account state comes from the auth manager, which `taproot.go` attaches in the router configurator. Quota rows of accounts the manager no longer lists are ignored. Nothing is evaluated while alerts are off or before the manager has loaded.
- **Reset grants.** For each Claude account in scope that does not need sign-in, at most every 3 hours, alerts reads `GET https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1` with the account's current access token, as the dashboard's `claudeResetGrants.ts` does. It never refreshes a token and never redeems a grant.
- **Delivery.** One embed per message: title, description, a color by severity, timestamp, and fields. Times use Discord's `<t:…>` markup, so each reader sees local time. `allowed_mentions` is empty and labels are markdown-escaped, so account or grant text cannot ping or reformat. One sender drains a queue that holds at most one message per key: a newer message for a pending key replaces it in place, so a recovery is never dropped behind its own alert. The queue holds up to 256 keys and drops a message for a new key beyond that, with a warning, so the proxy never waits. The sender honors `retry_after` on 429 and retries network errors and 5xx with backoff. It gives up after 4 attempts or once the next wait would take the message past 1 minute of waiting, and logs failures without the URL.
- **Test button.** `POST /taproot/api/v1/alerts/test` makes one delivery attempt within 10 seconds. A 429 from Discord returns 429 `rate_limited` with Discord's `retryAfter`; other failures return Discord's status.
- **End-to-end testing.** `TAPROOT_ALERTS_DISCORD_ORIGIN=http://127.0.0.1:<port>` sends deliveries to a local fake Discord. It keeps the webhook path and replaces only the scheme and host. Only loopback origins (`127.0.0.1`, `localhost`, `::1`) are accepted, with the same check as ingest's `TAPROOT_DEV_*` overrides, because the webhook token travels in the path. An active override logs a warning; another origin is ignored with a warning.

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
| `POST /quota/refresh` | Body `{authIndex}`. Polls that account's usage endpoint now and returns `{authIndex, provider, windows}`, with windows shaped as in `/quota/current`. Each account can refresh once per 30 s; a faster request gets 429 `{error: {code: "rate_limited", message, retryAfter}}` (whole seconds) and a `Retry-After` header. Other errors: 404 `not_found`, 400 `unsupported_provider` (Claude and Codex only), 502 `upstream_failed` |
| `GET /quota/series?from&to&provider` | `{series: [{authIndex, provider, window, periodSeconds, points: [{t, utilization}], resets: [t]}]}` |
| `GET /usage/summary?from&to&bucket&groupBy=model\|account\|provider\|session\|device` | `{totals: Totals, buckets: [{t, groups: {[key]: Totals}}]}`. With `groupBy=device`, events without a device group under `(none)`. |
| `GET /usage/events?from&to&authIndex&model&sessionId&deviceId&failed&cursor&limit` | `{events: [UsageEvent], nextCursor}`. Each event carries `deviceId`, `deviceName`, and `deviceLogin`; `deviceId=(none)` selects events without a device. |
| `GET /usage/devices?from&to` | `deviceId` is `node:<StableID>`, `local`, `cidr:<address>`, `plugin:<plugin ID>`, `plugins`, or empty for events without a device; `via` is `loopback`, `tailnet`, `cidr`, `plugin`, or empty. `{devices: [{deviceId, name, login, via, lastSeen, requests, failures, inputTokens, outputTokens, reasoningTokens, cacheReadTokens, cacheWriteTokens, cacheWrite5mTokens, cacheWrite1hTokens, costUSD, cacheHitRatio, mainConversation}]}`. `mainConversation` covers the device's Claude main-conversation requests: `{requests, splitRequests, cacheWriteTokens, cacheWrite5mTokens, cacheWrite1hTokens, fiveMinuteShare, estimate: {rewriteRequests, rewriteTokens, rewriteCostUSD, oneHourPremiumUSD, netSavingsUSD}}`, where `estimate` guesses what a one-hour cache TTL would change. |
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
| `GET /settings` and `PUT /settings` | The editable subset of `taproot.yaml`: retention, schedule, poller interval, `allow-logins`, `admin-nodes`, and alerts (`{enabled, webhookSet, webhookHint, webhookInvalid, active}`; `PUT` takes `{discordWebhook, enabled}`, and `discordWebhook: ""` clears it) |
| `POST /alerts/test` | `{sent: true}`, or 409 `no_webhook` or `invalid_webhook`, or 429 `rate_limited` (`retryAfter` in whole seconds, plus a `Retry-After` header), or 502 `delivery_failed` |

`Totals` is `{requests, failures, inputTokens, outputTokens, reasoningTokens, cacheReadTokens, cacheWriteTokens, cacheWrite5mTokens, cacheWrite1hTokens, costUSD, latencyP50Ms, latencyP95Ms, ttftP50Ms, ttftP95Ms}`.

## Dashboard scope

- **Kept and trimmed:**
  - Dashboard
  - Routing (OAuth credentials only; it also holds the provider model rules, OAuth excluded models and model aliases)
  - Config (no API keys, no BestProxy hint)
  - Plugins and Plugin Store
  - System
- **New:** Accounts (one page for quota, credentials, and sign-in; it replaced the Quota, Auth Files, and OAuth Login pages, so it has no SIWC toggle and signs in with Claude, Codex, Antigravity, Kimi International, xAI, and Muse only, with no Devin, Vertex, Kimi China, or sponsor cards), Usage, Request Logs (replaces the logs viewer, keeping app logs as a tab), Backups, Settings (access allowlist, retention, schedule).
- **Removed:** Quick Start, AI Providers, every sponsor, the login page, and the non-English locales (English only; the i18n layer stays, and each feature owns `src/i18n/locales/en/<feature>.json`).
- **Style:** a quiet, dark quota dashboard.
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

- **No data migration.** Taproot starts fresh. The usage importer that phase A built for test data was removed.
- **Request log segments.** Segments roll on idle (15m), age (6h), day change, or 64 MiB raw, not hourly. On the real fixture, per-session segments compressed 97x, while per-session-hour segments compressed 73x. The 64 MiB cap keeps the worst lookup under 50 ms.
- **Plugin resource routes** (`/v0/resource/plugins/*`) are guarded but get no injected key. Upstream serves them unauthenticated and forwards headers to plugin code.
- **RESP usage queue.** Upstream multiplexes this Redis-protocol queue on the proxy port, outside gin, so the guard never sees it, and the generated `MANAGEMENT_PASSWORD` enables remote `AUTH`. The security recheck (F2) showed a tailnet peer authenticating with the secret. Taproot does not use the queue, so the RESP connection filter (see "Upstream boundary") closes every RESP connection that does not come from loopback, before any reply. Drop the hook if upstream adds a switch to disable the queue.
- **Poller timeout.** The idle-account poller's 30 s timeout on its read-only usage probe is an intended exception to AGENTS.md's no-timeout rule. That rule targets proxied upstream traffic. The alerts module's reset-grant read (30 s) and Discord webhook POST (15 s) are exceptions for the same reason.
- **Shutdown drain.** Setup runs on `context.Background()`, so request-log records still queued at shutdown are recovered from the open segment on the next start rather than drained. Usage events still queued are lost. The flush interval is 1 s.
- **Mounts.** `config.yaml` lives in the mounted data directory, which is what restore's atomic rename needs. The tailscaled socket must be mounted at `/var/run/tailscale/tailscaled.sock`. After deploying, `GET /taproot/api/v1/me` from a tailnet client listed in `admin-nodes` must report `via: "tailnet"` and `admin: true`, and the same request from another node of the same login outside `admin-nodes` must get 403 `not_admin_node`. If it reports loopback, the port forwarding is rewriting source addresses and the guard would admit everyone. From the server host itself, `curl http://100.64.0.1:8317/taproot/api/v1/me` (with the host's Tailscale address) must return 403 and `curl http://100.64.0.1:8317/v1/models` must not, which shows the server's own node is proxy-only. A `127.0.1.1:8317` publish, from the host, gets 403 on both unless the host's LAN address is in `trusted-cidrs` (see "Security model").
