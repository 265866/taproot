# Changelog

Notable changes to taproot. Releases are tagged `taproot-vX.Y.Z`.

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
- With `access.enabled: false`, only the proxy is open; the dashboard and APIs answer loopback only.
- Taproot refuses to start in CLIProxyAPI's Home mode and with `--tui --standalone`, which would skip the guard.
- The dashboard has no login page, Quick Start, AI Providers, API-key editing, sponsor content, update check, or non-English locales.
- Upstream's release workflows, Dockerfile, and compose files are replaced by one CI workflow and `Containerfile.taproot`.
