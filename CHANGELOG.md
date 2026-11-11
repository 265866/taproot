# Changelog

Notable changes to taproot. Releases are tagged `taproot-vX.Y.Z`. From v0.2.0 on, the `changelog` job of `.github/workflows/release-please.yml` writes each release's section in the release PR, from the Conventional Commit messages of taproot's own commits since the previous release. Notes that need more than a commit message wait under Unreleased until the release PR; see "Releases" in `CONTRIBUTING.md`.

## Unreleased

### Upgrading from 0.4

- Check which version you run before you follow these notes. The 0.4.0 install examples, Quick start, quadlet, and compose file still pulled the `0.3` image tag, so a setup installed or updated from them runs 0.3. If your image tag, compose file, or quadlet says `0.3`, apply "Upgrading from 0.3" under 0.4.0 first, then these notes. Installs updated by `scripts/taproot/taproot-update` follow the tag they were set to. The examples now pull the current release line.
- The home page is a new Dashboard. It opens with a verdict (serving normally, degraded, or down), lists what needs you with one button each, and shows each provider's pool right now. Below that, a window of 1 hour to 30 days shows client requests, failures, latency, cost, recent failures, and the top devices and models. The old hero, tiles, provider rows, and "Where to go from here" cards are gone, and the model count moved to **System**. See [Dashboard](docs/dashboard.md).
- The database gains three tables: `client_requests`, `client_request_slots`, and `backup_scheduled_run`. They are created at start, and nothing else in the schema changes. `client_requests` records each client request once, however many accounts it tried. It starts empty, so the Dashboard counts only requests made after the upgrade. Usage keeps its full history, and a server with usage history shows the Dashboard rather than the first-run checklist. Client requests are kept as long as usage events.
- `/taproot/api/v1` has new admin-only endpoints: `GET /requests/summary`, `GET /requests/recent`, `GET /status`, `GET /attention`, and `GET /pools`. No existing endpoint changed.
- The sidebar shows badges on **Accounts**, **Access**, and **Backups**, and the tab title starts with a dot while something needs you. The Accounts badge used to count accounts. It now counts problems.
- On a touch screen, every button and link on the Dashboard and the sidebar is at least 44 pixels tall, and Usage's and Accounts' range buttons grow to match. On a phone, and on a phone held sideways, text is a step larger.
- Claude accounts show the Fable limit. Taproot now reads the limits list in Claude's usage response, so the Fable limit is recorded and charted with the 5-hour and weekly limits. A 5-hour reading from a session that has ended no longer shows as current. An account whose plan has no 5-hour limit (its limits list has no session entry) shows no 5-hour limit at all. Alerts call it the Fable limit.
- On **Accounts**, a Claude account's card and Manage sheet show the 5-hour, weekly, and Fable limits, with Fable's share of the weekly limit, and one button per kind of reset the account holds (full, Fable, 5-hour, weekly) in place of the single **Reset limits** button. Each asks first and shows what it will change. A Fable reset with no Fable use this week can't be spent.
- One **Show emails** switch in the sidebar replaces the per-page toggles on Accounts, Usage, Request Logs, Routing, and Config. Every page follows it, Access included. It is off on every page load and is never saved in the browser.
- On a phone, the Dashboard is a single column of cards: the verdict, a compact Needs you, Pools, then the window. Accounts and Usage put their options behind one **View** or **Options** button that opens a sheet, and on a desktop each page has one row of controls. Usage's Group by moved onto the cost chart.
- On a touch screen, a chart's reading on Accounts, Usage, and the Dashboard goes away 3 seconds after the finger lifts, and at once when the page scrolls or a touch lands elsewhere. Mouse and keyboard use are unchanged.
- The image and the release binaries are built with Go 1.26.11, for an HTTP/1 server crash in Go 1.26.10's `net/http` (GO-2026-6859). Building the request-log codec from source now ends with an `objcopy` step, because Go 1.26.11 refuses to link LLVM bitcode; CONTRIBUTING has the commands.
- A successful **Send test** of the Discord alerts, or a new webhook, clears the record of failed deliveries, so the "Discord alerts are not being delivered" item leaves **Needs you**.

## [0.4.0](https://github.com/265866/taproot/compare/taproot-v0.3.0...taproot-v0.4.0) (2026-11-10)

### Upgrading from 0.3

- `PUT /taproot/api/v1/settings` refuses a field it does not know with 400 `invalid_body`, where it used to ignore it. It accepts only `access.version`, `access.devices`, `access.device`, `access.addresses`, `requestLogs.maxDays`, `requestLogs.maxBytes`, `requestLogs.excludePaths`, `requestLogs.keepUnrouted404`, `backups.dailyAt`, `backups.keep`, `ingest.pollInterval`, `alerts.discordWebhook`, and `alerts.enabled`. A script that sends back what `GET /settings` returns must first drop the read-only fields, such as `access.enabled`, `backups.nextRunAt`, and `info`, and a misspelled field, such as `daily-at` for `dailyAt`, now fails rather than changing nothing.
- `access.cache-ttl` is at most `10m`, and taproot no longer starts with a larger value. A device that took over another's tailnet address kept the other device's access for up to the whole TTL. Before you update, lower `access.cache-ttl` in `taproot.yaml` to `10m` or less, or remove it to use the default `1m`.
- `access.addresses` refuses an entry in Tailscale's ranges (`100.64.0.0/10` and `fd7a:115c:a1e0::/48`) or a wider range that includes one, such as `0.0.0.0/0`, and taproot no longer starts with one. Such an entry gave every tailnet device it covered Proxy and took Admin from admin devices. Before you update, replace it in `taproot.yaml` with the addresses outside Tailscale that need the proxy, and turn on Proxy for the tailnet devices that need it on the Access page. If you added `0.0.0.0/0` or `::/0` to open the proxy to every caller, set `access.enabled: false` instead, and remove the entry, because taproot refuses it even while access is disabled.
- NixOS: `services.taproot.settings.server.host` must now be a literal Tailscale address, an IPv4 address in 100.64.0.0/10 or a bracketed IPv6 address in fd7a:115c:a1e0::/48. A configuration that set it to a MagicDNS name, a LAN address, or another IPv4 address no longer evaluates. Remove that setting and set `services.taproot.tailscale.address` to the output of `tailscale ip -4` instead.
- `taproot.yaml` must hold one YAML document. Taproot used to read only the first document after a `---` line and ignore the rest; it now refuses to start with a second one. Merge them into one before you update.
- After a restore, taproot exits with status 75 so that its supervisor restarts it; a normal stop still exits 0. The shipped units and compose file restart on any exit. If your own unit or monitor treats a non-zero exit as a failure, allow 75.

### Fixed

* **taproot:** keep identifying callers after tailscaled restarts ([#46](https://github.com/265866/taproot/pull/46)) ([74efc7b](https://github.com/265866/taproot/commit/74efc7b963277bb4c96552630945d36231bdb33e))
* **web:** label nameless devices by their Tailscale IP ([#45](https://github.com/265866/taproot/pull/45)) ([529884c](https://github.com/265866/taproot/commit/529884c364f5f734538c2f933b2a252da044b7f4))
* **taproot:** admin devices read the model list and never raise access requests ([#47](https://github.com/265866/taproot/pull/47)) ([f3eb5c1](https://github.com/265866/taproot/commit/f3eb5c17dc420febb4025444b79902e12615024f))
* **taproot:** let the Admin switch work for devices from other tailnets ([#49](https://github.com/265866/taproot/pull/49)) ([b242aed](https://github.com/265866/taproot/commit/b242aed74d92b627ed2fc16b55554ecf8f35c93e))
* **taproot:** refuse Admin for the server's own node even before it is looked up ([#50](https://github.com/265866/taproot/pull/50)) ([58bb338](https://github.com/265866/taproot/commit/58bb33855316d484f2dd1d6bc2d4da8e3f8e073d))
* **taproot:** keep revoked access revoked across addresses, tabs, and websockets ([#51](https://github.com/265866/taproot/pull/51)) ([09e3d70](https://github.com/265866/taproot/commit/09e3d7030a8d7e04a7f0e255aa9e047e6e6d5730))
* **taproot:** make request log retention delete the oldest logs first ([#52](https://github.com/265866/taproot/pull/52)) ([f87ada2](https://github.com/265866/taproot/commit/f87ada2437818559a53c5d90eff68a5377905c2d))
* **taproot:** stop daily usage hangs and fix several usage and quota readings ([#53](https://github.com/265866/taproot/pull/53)) ([cc99e40](https://github.com/265866/taproot/commit/cc99e40c8b36dcf279d555a09928a670e48c02fd))
* **taproot:** bound request log memory and keep segments and keys safe ([#54](https://github.com/265866/taproot/pull/54)) ([dca4b47](https://github.com/265866/taproot/commit/dca4b477628ebe570d83f22eae485d593fef2bcd))
* **taproot:** harden the updater, release pipeline, nix module, and demo checks ([#55](https://github.com/265866/taproot/pull/55)) ([4d78a84](https://github.com/265866/taproot/commit/4d78a841c83eb13e365a8f1a7b19bb7a76431c70))
* **web:** make the dashboard shell honest, recoverable, and keyboard usable ([#57](https://github.com/265866/taproot/pull/57)) ([880a4e7](https://github.com/265866/taproot/commit/880a4e74877302b99b61745f34ab33f5b70873a9))
* **taproot:** make restores safe to run and harden backups and settings ([#58](https://github.com/265866/taproot/pull/58)) ([5e266d1](https://github.com/265866/taproot/commit/5e266d1f329c31d60a1e488a1c248a5457114024))
* **taproot:** keep queued records on shutdown and tidy first run and errors ([#56](https://github.com/265866/taproot/pull/56)) ([5da99f6](https://github.com/265866/taproot/commit/5da99f62f57ae2e9c62d3437b4d4cb33a59e6e43))
* **taproot:** fair setup claims, clearer denials, and safer tailscaled reads ([#59](https://github.com/265866/taproot/pull/59)) ([32e7d50](https://github.com/265866/taproot/commit/32e7d50b271d48de85d038802f79cd9bcd6758ef))
* **taproot:** reliable discord alerts, proxied quota reads, and fairer soonest-reset routing ([#60](https://github.com/265866/taproot/pull/60)) ([aad10ad](https://github.com/265866/taproot/commit/aad10adef1bb94253705a2baac2acb6d1c5c2ae3))
* **web:** make the dashboard home and system page honest and live ([#61](https://github.com/265866/taproot/pull/61)) ([fb6acfc](https://github.com/265866/taproot/commit/fb6acfcb1a334b1b0d0a756ddc88b0fe3d91fee2))
* **web:** fix alias data loss and broken account and sign-in flows ([#62](https://github.com/265866/taproot/pull/62)) ([932288f](https://github.com/265866/taproot/commit/932288f4607f808033e7c1ecd34455ac86f4225c))
* **web:** make quota views fast, honest about age, and safe to retry ([#63](https://github.com/265866/taproot/pull/63)) ([ed8ddd9](https://github.com/265866/taproot/commit/ed8ddd9f058fa20dac5133fc3278f6399eb208f9))
* **web:** keep usage analytics honest about stale data, ranges, and labels ([#64](https://github.com/265866/taproot/pull/64)) ([04fcfbe](https://github.com/265866/taproot/commit/04fcfbea69749c84d0b7d76b6aa3e05f9455eb4f))
* **taproot:** refuse a request-log max-days that overflows the age cutoff ([#65](https://github.com/265866/taproot/pull/65)) ([d5b1e9c](https://github.com/265866/taproot/commit/d5b1e9cbc77df95a02a48354b24ef1949453798b))
* **web:** make request and app logs reliable, faithful, and accessible ([#66](https://github.com/265866/taproot/pull/66)) ([5abe4dd](https://github.com/265866/taproot/commit/5abe4dd156a7eabd96c053cb317834d8f6cf9add))
* **web:** safer restores, accurate settings, and clearer access and setup screens ([#67](https://github.com/265866/taproot/pull/67)) ([70ea48c](https://github.com/265866/taproot/commit/70ea48c9453cb4d68ae8907815d09cd97ba3c0c5))
* **web:** keep concurrent config changes and fix config, routing, and plugin pages ([#68](https://github.com/265866/taproot/pull/68)) ([a2455d9](https://github.com/265866/taproot/commit/a2455d9d4c812ee87b56f7f5aac886ceb5159aaa))

## [0.3.0](https://github.com/265866/taproot/compare/taproot-v0.2.0...taproot-v0.3.0) (2026-11-09)

### Added

* **taproot:** refused devices become access requests you can approve ([#40](https://github.com/265866/taproot/pull/40)) ([dcc9179](https://github.com/265866/taproot/commit/dcc9179a095a54f5d235b252de2dc4f9d3975b61))

### Fixed

* **web:** hide the access stable ID header with its column on narrow screens ([#38](https://github.com/265866/taproot/pull/38)) ([bfb045a](https://github.com/265866/taproot/commit/bfb045adb82921b3c181f88d98bcdeb920a16276))
* **web:** dashboard pages work on phones ([#41](https://github.com/265866/taproot/pull/41)) ([cb07814](https://github.com/265866/taproot/commit/cb07814e41603f963f6e16a1a4a4cabc224addc0))
* **deps:** update Go, dashboard, and Nix dependencies before the release ([#42](https://github.com/265866/taproot/pull/42)) ([c846f89](https://github.com/265866/taproot/commit/c846f891a7a1cd45d743728323bafdfc76f05821))

## [0.2.0](https://github.com/265866/taproot/compare/taproot-v0.1.0...taproot-v0.2.0) (2026-11-09)

### Breaking changes

* **Access is per device.** Each tailnet device has a Proxy and an Admin switch, set on the new **Access** page or in `access.devices` by stable ID, and a new device has neither. `access.addresses` replaces `access.trusted-cidrs`. `access.allow-logins`, `access.admin-nodes`, and `access.trusted-cidrs` stop startup with a message naming their replacement; there is no migration. A device that leaves the tailnet is removed from `taproot.yaml` 24 hours later, and the server's own node keeps Proxy without an entry.

### Upgrading from 0.1.0

* Rewrite the `access` block of `taproot.yaml` before you start 0.2.0. List each device that keeps access under `access.devices` with `id` (its stable ID; `tailscale status --json` shows it as `ID`), `name`, and `proxy: true`, `admin: true`, or both; Admin does not imply Proxy. Move each `trusted-cidrs` string to `access.addresses` as an entry with `cidr:` (and an optional `note:`). See [the example](https://github.com/265866/taproot/blob/taproot-v0.2.0/examples/taproot/taproot.yaml). Or leave `access.devices` empty and claim the server from your device with the setup code it logs at start.
* Data written by v0.1.0 is not carried forward. Stop 0.1.0 first, so two servers never refresh the same OAuth tokens, then start 0.2.0 on a new data directory with your `config.yaml`, `taproot.yaml`, and `auths/` copied in. See [docs/stability.md](https://github.com/265866/taproot/blob/taproot-v0.2.0/docs/stability.md).
* Release images are published at `ghcr.io/265866/taproot`, signed and attested; the [README](https://github.com/265866/taproot/blob/taproot-v0.2.0/README.md) describes installing, verifying, and updating them.

### Added

* **taproot:** synthetic demo data and a screenshot capture script ([4f25922](https://github.com/265866/taproot/commit/4f25922cbf6e56137d399c31087b8930e45e8787))
* **taproot/reqlog:** lossless blob codecs for request-log payloads ([46cede6](https://github.com/265866/taproot/commit/46cede6b500a8fdde04bfdc768f2d2412c93b4fa))
* **taproot/reqlog:** compact request-log storage ([0316b76](https://github.com/265866/taproot/commit/0316b765021c47ca3d8c0a1e5151c4a40f3dba8d))
* **taproot/access:** give dashboard chunks the page's callers ([2023254](https://github.com/265866/taproot/commit/202325415f86d3e363575baaa41c6898d2dcee42))
* **taproot/reqlog:** serve a lean record detail with blobs on demand ([29a1baf](https://github.com/265866/taproot/commit/29a1bafc4c432d7cc653be7c6fae10db92a7f4a7))
* **web:** open request-log records lean and load payloads on demand ([376c91c](https://github.com/265866/taproot/commit/376c91cd2626d34df919f99def0150846d51c7ac))
* **taproot:** default backups to midnight and show both time zones ([#17](https://github.com/265866/taproot/pull/17)) ([00c1c86](https://github.com/265866/taproot/commit/00c1c869869a08a93ec8bc25d92043bb915e9d89))
* **web:** show request-log recording and keep-forever limits in settings ([#16](https://github.com/265866/taproot/pull/16)) ([af4c4e7](https://github.com/265866/taproot/commit/af4c4e7421b1e179e42c459d90c6de0146087c69))
* **taproot:** send discord alerts as components v2 cards ([#12](https://github.com/265866/taproot/pull/12)) ([a87199f](https://github.com/265866/taproot/commit/a87199f4aa19958b3072fc688773b9bf7daf7839))
* **taproot:** taproot version and help, and clear startup failures ([#21](https://github.com/265866/taproot/pull/21)) ([c135985](https://github.com/265866/taproot/commit/c135985331681d9902e9d3c039bf13c8f8e69972))
* **taproot:** grant access per device from a new access page ([#22](https://github.com/265866/taproot/pull/22)) ([620a401](https://github.com/265866/taproot/commit/620a401f98591bfa87bf3df8d65bbdc262826a06))
* **nix:** add a flake with packages and a nixos module ([#23](https://github.com/265866/taproot/pull/23)) ([af793e5](https://github.com/265866/taproot/commit/af793e5a8b1ca083cff83fec595b7104d8a51988))
* **taproot:** first start without config files, claimed with a setup code ([#25](https://github.com/265866/taproot/pull/25)) ([e568655](https://github.com/265866/taproot/commit/e5686553eaf9b1de886369473a32462579f4153d))
* **taproot:** opt-in updater that verifies signed releases and rolls back ([#31](https://github.com/265866/taproot/pull/31)) ([cc6cfed](https://github.com/265866/taproot/commit/cc6cfed67b3a073f084af3797ca329b3363fbf4c))

### Fixed

* **taproot/access:** admit the server's own node before allow-logins ([f150e91](https://github.com/265866/taproot/commit/f150e9109b93814b044acdc92bf0d2ea2a1adaf8))
* **taproot/reqlog:** keep escaped base64 runs, retry copy, and limit blob embedding ([2edbf64](https://github.com/265866/taproot/commit/2edbf64b6b16d3828679056d5d77cc8f122e7c7d))
* **web:** copy large bodies through the copy event on plain http ([4133e9b](https://github.com/265866/taproot/commit/4133e9bcb9ad8e770fea069966865a7b28f42a3a))
* **taproot:** ship accurate model prices and update them on upgrade ([#18](https://github.com/265866/taproot/pull/18)) ([02f4f54](https://github.com/265866/taproot/commit/02f4f54e9965af6bc4fcece7a86cb21509d45cc4))
* **taproot:** record upstream errors on streamed request logs ([131b9ae](https://github.com/265866/taproot/commit/131b9ae737321b03eb6194671ad46fefb7efc2d4))
* **ci:** start release builds on their tag so signatures name it ([#34](https://github.com/265866/taproot/pull/34)) ([8803bee](https://github.com/265866/taproot/commit/8803beeb047b4fd30bd47e2fbef4a72cf88ca0bc))
* **taproot:** back up the codex refresh journal and price the gpt-image-2 models ([#36](https://github.com/265866/taproot/pull/36)) ([10d9faf](https://github.com/265866/taproot/commit/10d9faf946bb7871261db9b467dbc25f19c96263))

### Performance

* **executor:** cut proxy overhead on large Claude requests ([a05f50b](https://github.com/265866/taproot/commit/a05f50b30faa6f77a7cf9c0b5820aef237a19261))
* **taproot/reqlog:** look up base64 bytes in a table ([6479898](https://github.com/265866/taproot/commit/64798989412153b58eb4a62fb4cca2b1bdd7fb35))
* **taproot/web:** serve the dashboard as cached, gzipped chunks ([46fa5e9](https://github.com/265866/taproot/commit/46fa5e98ffc3e9aeeced44c9d9de4ed4ae5356cd))
* **taproot/store:** keep 8 idle SQLite connections ([67f045e](https://github.com/265866/taproot/commit/67f045e582f823f66828ff59bf853f3da19cbabe))
* **taproot/ingest:** read current quota with a loose index scan ([ec1cfef](https://github.com/265866/taproot/commit/ec1cfef374944cdc4832d4a73c3e27a08c96025d))
* **taproot/ingest:** walk the devices TTL estimate in index order ([d3c9822](https://github.com/265866/taproot/commit/d3c98229374f91bbccf351c5951183c5f3252a1a))
* **taproot/ingest:** read device and session usage from hourly rollups ([741ab51](https://github.com/265866/taproot/commit/741ab51f09d6c52efca1aa5f5f107d6a42f985cc))
* **taproot/reqlog:** index the list filters and read stats ends directly ([d1c698f](https://github.com/265866/taproot/commit/d1c698f3447528e7d84158ddc403fdf793c819aa))

### Upstream

* Merges [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) up to [681ce41b6](https://github.com/router-for-me/CLIProxyAPI/commit/681ce41b6e44c45b15b79378b003909d6ba29e68).

## v0.1.0

The first release. Taproot is a fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI), merged with upstream up to commit `6e6afab99`, that pools Claude and Codex subscription accounts behind one endpoint on a Tailscale tailnet.

### Added

- **Tailscale access guard.** Callers are admitted by the Tailscale login behind their source address, through the host's `tailscaled`. Only devices in `access.admin-nodes` reach the dashboard and the management API, and the guard adds the management key itself, so no key reaches a browser. It also refuses cross-site browser requests and DNS rebinding.
- **Quota ledger.** Five-hour and weekly utilization for every Claude and Codex account, from response headers and a poller for idle accounts, with pool projections and reset times.
- **Soonest-reset-first routing.** New sessions go to the account whose weekly window resets soonest; session affinity keeps running sessions on their account.
- **Usage and cost.** Requests, tokens including cache reads and writes, latency, time to first token, and failures, by account, model, session, and device, priced from an editable table.
- **Request logs.** Compressed per-session storage, a searchable SQLite index, and retention by age and size.
- **Backups.** Scheduled archives of the configuration, credentials, and database, with download and restore.
- **Discord alerts** for a hit limit, an empty pool, a broken login, and an expiring Claude reset grant.
- **Dashboard.** One Accounts page for quota, credentials, and sign-in, plus Usage, Request Logs, Backups, and Settings. Account emails are hidden until you choose to show them.
- **Deployment files.** `Containerfile.taproot` builds one image with the dashboard embedded. `examples/taproot/` holds a `config.yaml`, a `taproot.yaml`, and a rootless Podman quadlet.

### Changed from CLIProxyAPI

- The Go module path is `github.com/265866/taproot`. `scripts/taproot/sync-upstream.sh` rewrites upstream's path before each merge.
- `taproot.yaml` is decoded strictly, and taproot refuses to start without `access.allow-logins`.
- With `access.enabled: false`, only the proxy is open; the dashboard and APIs answer loopback only and still refuse browser requests from other sites.
- Taproot refuses to start in CLIProxyAPI's Home mode and with `--tui --standalone`, which would skip the guard.
- The dashboard has no login page, Quick Start, AI Providers, API-key editing, sponsor content, update check, or non-English locales.
- Upstream's release workflows, Dockerfile, and compose files are replaced by one CI workflow and `Containerfile.taproot`.
