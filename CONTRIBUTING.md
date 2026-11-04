# Contributing to taproot

Taproot is a hard fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). It keeps merging upstream's proxy and provider code, and adds its own access guard, quota and usage tracking, request logs, backups, alerts, and dashboard. This page explains what belongs in taproot, how the fork stays mergeable, and what to check before you send a change.

To report a vulnerability, follow [SECURITY.md](SECURITY.md) instead of opening an issue.

## What belongs here

Taproot changes are welcome in these places:

- `internal/taproot/**`: the fork's Go modules (access, ingest, request logs, backups, alerts, settings, routing, and the embedded dashboard).
- `web/**`: the dashboard. It is a hard fork of the CLIProxyAPI Management Center and no longer follows it.
- `scripts/taproot/**`, `Containerfile.taproot`, `examples/taproot/**`, and the taproot documentation.

These belong upstream, in CLIProxyAPI, rather than here:

- Provider protocols, translators (`internal/translator/`), executors, and other proxy behavior that is not specific to taproot. A change there conflicts with every upstream merge.
- Home mode (CLIProxyAPIHome). Taproot refuses to start in it.

The dashboard removed some upstream features on purpose: Quick Start, AI Providers and API-key provider editing, sponsor content, the login page and management key, and the non-English locales. Do not bring them back. [`web/AGENTS.md`](web/AGENTS.md) has the full list.

## The upstream boundary

Taproot stays cheap to merge with upstream. [`internal/taproot/PLAN.md`](internal/taproot/PLAN.md), section "Upstream boundary", is the source of truth. In short:

- Taproot's module path is `github.com/265866/taproot`. The sync script rewrites upstream's path (`github.com/router-for-me/CLIProxyAPI/v8`) in each upstream snapshot before merging it, with `scripts/taproot/rewrite-module-path.sh`, so import lines do not conflict. Do not rewrite imports by hand.
- Fork code lives in `internal/taproot/**` and plugs into the server through server options.
- PLAN lists each upstream file taproot edits, with the reason and the size of the edit. A new upstream edit needs the same: keep it as small as you can, and add its reason to PLAN in the same change.
- Upstream files that taproot deleted stay deleted. `scripts/taproot/sync-upstream.sh` keeps them deleted when upstream changes them, and stops on every other conflict.
- Backend code for providers the dashboard dropped stays in the Go tree, because deleting it would conflict with upstream merges.

To merge upstream, add the remote and run the sync script. It never pushes.

```sh
git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
scripts/taproot/sync-upstream.sh --dry-run
scripts/taproot/sync-upstream.sh
```

## Before you send a change

Read [`AGENTS.md`](AGENTS.md) for the Go conventions and [`web/AGENTS.md`](web/AGENTS.md) for the dashboard's. Structural work starts from PLAN.

Run the checks that CI runs:

```sh
go build ./...
go vet ./...
go test ./internal/taproot/... ./internal/api/... ./sdk/cliproxy/... ./cmd/...
cd web && bun install --frozen-lockfile && bun run verify && cd ..
docker build -f Containerfile.taproot .
```

Format Go code with `gofmt` (or `gofumpt`). Add or update tests for any change in behavior. For a dashboard change, also load the page in a browser against a running server.

Keep personal data out of the repository. Tests, fixtures, screenshots, and examples use example values such as `alice@github`, `example.com`, `100.64.0.x`, and `tailnet-0000.ts.net`, never a real login, email, node ID, address, or token.

Use [Conventional Commits](https://www.conventionalcommits.org/), such as `fix(taproot): …` or `feat(web): …`. After a dependency change, run `bun scripts/taproot/third-party-notices.ts` to update [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

By contributing, you agree that your contribution is released under the [MIT License](LICENSE).
