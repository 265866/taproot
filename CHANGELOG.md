# Changelog

Notable changes to taproot. Releases are tagged `taproot-vX.Y.Z`. From v0.2.0 on, the `changelog` job of `.github/workflows/release-please.yml` writes each release's section in the release PR, from the Conventional Commit messages of taproot's own commits since the previous release. Notes that need more than a commit message wait under Unreleased until the release PR; see "Releases" in `CONTRIBUTING.md`.

## Unreleased

- `access.addresses` refuses an entry in Tailscale's ranges (`100.64.0.0/10` and `fd7a:115c:a1e0::/48`) or a wider range that includes one, such as `0.0.0.0/0`, and taproot no longer starts with one. Such an entry gave every tailnet device it covered Proxy and took Admin from admin devices. Before you update, replace it in `taproot.yaml` with the addresses outside Tailscale that need the proxy, and turn on Proxy for the tailnet devices that need it on the Access page.

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
