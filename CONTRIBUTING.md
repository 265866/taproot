# Contributing to taproot

Taproot is a hard fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). It keeps merging upstream's proxy and provider code, and adds its own access guard, quota and usage tracking, request logs, backups, alerts, and dashboard. This page explains what belongs in taproot, how the fork stays mergeable, and what to check before you send a change.

To report a vulnerability, follow [SECURITY.md](SECURITY.md) instead of opening an issue.

## What belongs here

Taproot changes are welcome in these places:

- `internal/taproot/**`: the fork's Go modules (access, ingest, request logs, backups, alerts, settings, routing, and the embedded dashboard).
- `web/**`: the dashboard. It is a hard fork of the CLIProxyAPI Management Center and no longer follows it.
- `scripts/taproot/**`, `Containerfile.taproot`, `examples/taproot/**`, and the taproot documentation.
- `flake.nix`, `flake.lock`, and `nix/`: the Nix packages, the NixOS module `services.taproot`, and its VM test.

These belong upstream, in CLIProxyAPI, rather than here:

- Provider protocols, translators (`internal/translator/`), executors, and other proxy behavior that is not specific to taproot. A change there conflicts with every upstream merge.
- Home mode (CLIProxyAPIHome). Taproot refuses to start in it.

The dashboard removed some upstream features on purpose: Quick Start, AI Providers and API-key provider editing, sponsor content, the login page and management key, and the non-English locales. Do not bring them back. [`web/README.md`](web/README.md) has the full list.

[`internal/taproot/DESIGN.md`](internal/taproot/DESIGN.md) describes how taproot is built and why: the upstream boundary, the security model, each module's design and the measurements behind it, the `/taproot/api/v1` contract, and the dashboard's scope. Read it before structural work, and update it in the same change. [`web/README.md`](web/README.md) describes the dashboard's structure and conventions.

## The upstream boundary

Taproot stays cheap to merge with upstream. The "Upstream boundary" section of `DESIGN.md` is the source of truth. In short:

- Taproot's module path is `github.com/265866/taproot`. The sync script rewrites upstream's path (`github.com/router-for-me/CLIProxyAPI/v8`) in each upstream snapshot before merging it, with `scripts/taproot/rewrite-module-path.sh`, so import lines do not conflict. Do not rewrite imports by hand.
- Fork code lives in `internal/taproot/**` and plugs into the server through server options.
- `DESIGN.md` lists each upstream file taproot edits, with the reason and the size of the edit. A new upstream edit needs the same: keep it as small as you can, and add its reason to `DESIGN.md` in the same change. `scripts/taproot/check-upstream-boundary.sh` fails on a changed upstream file that the section does not name in backticks.
- Upstream files that taproot deleted stay deleted. `scripts/taproot/sync-upstream.sh` keeps them deleted when upstream changes them. It stops on every other conflict, and on new upstream files of the kinds taproot deletes, such as a new `docs/*.md` guide, so a person decides whether to keep each one.
- The rewrite must give the same output for the same upstream commit. `scripts/taproot/check-rewrite.sh` checks that it still reproduces the last snapshot; the sync script runs that check first.
- Backend code for providers the dashboard dropped stays in the Go tree, because deleting it would conflict with upstream merges.

To merge upstream, add the remote and run the sync script. It never pushes.

```sh
git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
scripts/taproot/sync-upstream.sh --dry-run
scripts/taproot/sync-upstream.sh
```

## Rules for changing code

These rules keep upstream merges cheap and protect behavior that users and long-running streams depend on. They apply to people and coding agents alike.

- Do not change `internal/translator/`. It is upstream code, and every edit there conflicts with upstream merges; a translator fix belongs upstream.
- In every executor, the user's payload configuration is the last change to a request's content. That holds on streaming, WebSocket, continuation, retry and fallback, image, and token-count paths. Finish all built-in translation, normalization, injection, and cleanup first, then apply the payload rules exactly once to the final payload. Nothing after them may overwrite a configured value or restore a filtered field; only transport framing, serialization, signature calculation, and read-only validation may follow. A change to a request-building path needs a regression test for this order.
- Set timeouts only while acquiring credentials. Once an upstream connection is established, nothing on it gets a timeout. The intended exceptions include the Codex websocket liveness deadlines in `internal/runtime/executor/codex_websockets_executor.go`, the wsrelay session deadlines in `internal/wsrelay/session.go`, the management APICall timeout in `internal/api/handlers/management/api_tools.go`, the `cmd/fetch_antigravity_models` utility, and taproot's own outbound calls, which are not proxied traffic: the idle-account usage poller (30 s), the alerts reset-grant read (30 s), the Discord webhook POST (15 s) and its test send (10 s), and the tailscaled LocalAPI calls (3 s).
- Keep the promises in [`docs/stability.md`](docs/stability.md). A database change only adds tables, indexes, or nullable or defaulted columns. A renamed or removed `taproot.yaml` key needs a line in the release notes that says what to change.
- Taproot refuses to start in CLIProxyAPIHome mode (`-home-jwt`), so Home features are out of scope.
- Endpoints under `/v0/management` are deprecated. Change them only when a compile error requires it.
- `internal/runtime/executor/` holds executors and their tests only. Helpers go in `internal/runtime/executor/helps/`.
- Go code follows upstream's style: `gofmt` with goimports ordering, errors wrapped with context, a shadowed error named after its call (`errStart := server.Start()`), a close error in a `defer` logged rather than dropped, logrus for logging with no secrets or tokens in a log line, no `log.Fatal` or `log.Fatalf`, and no panic in an HTTP handler, which logs the error and returns a meaningful status instead.
- Tests of TTLs, expiry, ordering, or cache eviction use a controllable clock (a `nowFunc` or fake clock) or explicit timestamps, not wall-clock `time.Sleep`. Timer granularity (about 15.6 ms on Windows) and CI load make sleeps flaky.
- Comments and new Markdown are in English.

## Before you send a change

Run the checks that CI runs:

```sh
go build ./...
GOOS=linux go vet ./...
git ls-files -z '*.go' | xargs -0 "$(GOTOOLCHAIN=go$(go list -m -f '{{.GoVersion}}') go env GOROOT)/bin/gofmt" -l
scripts/taproot/check-upstream-boundary.sh
go test -skip '^TestAdvertiserAndBrowser_Integration$' ./...
golangci-lint run
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
cd web && bun install --frozen-lockfile && bun run verify && cd ..
docker build -f Containerfile.taproot .
```

`go vet` runs for linux, as in CI: on Windows it also checks upstream's `internal/pluginhost/loader_windows*.go` and flags its `unsafe.Pointer` use. CI uses the Go version in `go.mod`, and `Containerfile.taproot`'s `GO_VERSION` must match it. `gofmt -l` must print nothing. Run the `gofmt` of `go.mod`'s Go version, as that line does, and not the installed Go's. `gofmt`'s output changes between Go releases, so a newer `gofmt` flags upstream files, and rewriting them makes upstream merges conflict. `scripts/taproot/sync-upstream.sh --checks-only` runs that `gofmt` check along with the build, vet, test, and dashboard checks. The skipped test is upstream's and needs real mDNS multicast. `.golangci.yml` reports only taproot-owned code (`internal/taproot/`, `cmd/`, `scripts/taproot/`, and `taproot_*.go` files); CI runs golangci-lint v2.15.0. Format the Go files you change with that `gofmt` (or `gofumpt`). Add or update tests for any change in behavior. For a dashboard change, also load the page in a browser against a running server.

A plain `go build` links a stub request-log codec. A server built that way cannot read the Lepton (JPEG) and PNG blobs that the image's compactor writes. The image builds the Rust codec library and links it with the `taproot_codecs` build tag; to build the same way on Linux, with cgo and a Rust toolchain:

```sh
(cd internal/taproot/reqlog/codec/native && cargo build --release)
go build -tags taproot_codecs -o taproot ./cmd/server
```

The Nix build reads two files that the lockfiles decide: `nix/vendor-hash` (from `go.sum`) and `web/bun.nix` (from `web/bun.lock`). After a change to either lockfile, run `scripts/taproot/nix-refresh.sh` on a machine with Nix and commit what it changes; CI's `nix-hashes` job fails until you do. `nix flake check` builds the packages and runs the NixOS VM test, as CI's `nix` job does; the VM test needs KVM.

Keep personal data out of the repository. Tests, fixtures, screenshots, and examples use example values such as `alice@github`, `example.com`, `100.64.0.x`, and `tailnet-0000.ts.net`, never a real login, email, node ID, address, or token.

Use [Conventional Commits](https://www.conventionalcommits.org/), such as `fix(taproot): …` or `feat(web): …`. After a dependency change in `go.mod`, `web/package.json`, `web/bun.lock`, the codec's `Cargo.lock`, or `RUST_VERSION` in `Containerfile.taproot`, run `bun scripts/taproot/third-party-notices.ts` to update [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). It needs Go, Bun, and a Rust toolchain, and CI fails when the file is stale.

By contributing, you agree that your contribution is released under the [MIT License](LICENSE).

## Dependency updates

[Renovate](https://docs.renovatebot.com/) opens update pull requests once a week, as `renovate.json` configures it. The "Dependency updates" section of `DESIGN.md` has the details.

- **What it updates.** The dashboard's packages and Bun, the codec's crates, the GitHub Actions, the `Containerfile.taproot` base images and their digests, the Rust toolchain in the image and in CI, the flake inputs, and three Go modules: `modernc.org/sqlite`, `github.com/klauspost/compress`, and `github.com/andybalholm/brotli`.
- **Why not the other Go modules.** Every other line in `go.mod`, including the `go` line, is upstream's. Bumping one conflicts with the next upstream merge, so those updates arrive with the sync. `GO_VERSION` follows the `go` line, so the golang image gets digest updates only. The SQLite driver's indirect dependencies move only with the driver, because it needs the exact `modernc.org/libc` version that its own `go.mod` names. The "Upstream boundary" section of `DESIGN.md` records each `go.mod` line taproot changes, with its version, so a Go update pull request must update those entries before it merges, including any upstream requirement that minimal version selection newly raises. Renovate repeats this in the pull request's description.
- **What merges itself.** Digest pins and digest updates of GitHub Actions and base images merge once CI passes. Every other update waits for a review.
- **Generated files.** When a Renovate pull request changes `go.sum`, `web/bun.lock`, the codec's `Cargo.lock`, `flake.lock`, or `Containerfile.taproot` (`THIRD_PARTY_NOTICES.md` records its `RUST_VERSION`), `.github/workflows/renovate-refresh.yml` regenerates `nix/vendor-hash`, `web/bun.nix`, and `THIRD_PARTY_NOTICES.md`, pushes them to the branch, and starts CI for the new commit. On any other branch, refresh them by hand as described above.

## Releases

[release-please](https://github.com/googleapis/release-please) prepares each release, and [`.github/workflows/release.yml`](.github/workflows/release.yml) builds and publishes it.

- **The release PR.** On every push to `main`, release-please opens or updates a pull request titled `chore: release taproot X.Y.Z`. The PR sets the version in `.github/.release-please-manifest.json` and adds the release's section to `CHANGELOG.md`. The section lists the Conventional Commit subjects merged since the previous release: `feat` under Added, `fix` under Fixed, `perf` under Performance, and `revert` under Reverted. Other types are left out. A `!` after the type, or a `BREAKING CHANGE:` footer, also lists the change under breaking changes. Before 1.0, a breaking change or a `feat` raises the minor version, and anything else raises the patch version.
- **Notes by hand.** Some changes need more than a commit subject, such as a renamed setting or an upgrade step. Write those notes under `## Unreleased` in `CHANGELOG.md`, in the same change. Before you merge the release PR, move them into the new release's section on the PR's branch, and delete the empty `## Unreleased` heading. release-please rewrites that branch whenever `main` moves, so make this edit last.
- **Publishing.** Merging the release PR tags `taproot-vX.Y.Z` on the merge commit and creates a draft GitHub release. `release.yml` then builds `linux/amd64` and `linux/arm64` on native runners. It runs the codec tests on both, and each architecture decodes request-log bytes the other stored. It pushes `ghcr.io/265866/taproot` with the tags `X.Y.Z`, `X.Y`, `latest`, and `sha-<commit>`, an SBOM, build provenance, a GitHub attestation, and a keyless cosign signature. Last, it publishes the release with `taproot-X.Y.Z-linux-amd64.tar.gz`, `taproot-X.Y.Z-linux-arm64.tar.gz`, `SHA256SUMS`, and the tarballs' attestation, `taproot-X.Y.Z.sigstore.json`. The release notes are the version's `CHANGELOG.md` section.
- **Release candidates.** To test a release before you merge its PR, push a tag with an `-rc.N` suffix on the commit to test. The tag runs the `release.yml` of that commit. A candidate's image gets only its version tag and its `sha-` tag, never `X.Y` or `latest`. Its GitHub release is a prerelease, with the `## Unreleased` section as its notes. Give each new candidate the next number rather than moving a tag.

  ```sh
  git tag taproot-v0.2.0-rc.1 <commit>
  git push origin taproot-v0.2.0-rc.1
  ```

- **Re-running.** To rebuild and republish an existing tag, for example after a failed run, run the release workflow from the Actions tab with the tag.
- **Repository settings.** release-please opens its PR with the workflow's `GITHUB_TOKEN`, which needs "Allow GitHub Actions to create and approve pull requests" under Settings, Actions, General. GitHub starts no workflow for a PR that token opens, so CI does not run on the release PR, which changes only `CHANGELOG.md` and the manifest.

To check a published release:

```sh
gh attestation verify oci://ghcr.io/265866/taproot:0.2.0 --repo 265866/taproot
cosign verify ghcr.io/265866/taproot:0.2.0 \
  --certificate-identity-regexp '^https://github.com/265866/taproot/.github/workflows/release.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh release download taproot-v0.2.0 --repo 265866/taproot
sha256sum -c SHA256SUMS
gh attestation verify taproot-0.2.0-linux-amd64.tar.gz --repo 265866/taproot
```

## Demo data and screenshots

`internal/taproot/demo` seeds a data directory with synthetic accounts, 30 days of usage, quota history, request logs, and backups, all written through taproot's own ingest, request-log, and backup code. It also serves a fake tailscaled with a small tailnet, so the Access page has devices. The README screenshots come from it.

```sh
bash scripts/taproot/build-web.sh                 # embed the dashboard
bash internal/taproot/demo/run.sh                 # seed a temporary directory and serve it on http://127.0.0.1:8399
bash internal/taproot/demo/run.sh --capture       # also rewrite docs/screenshots/*.png, then stop
```

- **Nothing leaves the machine.** The demo config sends every outbound request to a dead loopback proxy, the quota poller and alerts are off, and the credentials are fake.
- **Same data every run.** The generator uses a fixed seed, and every timestamp is an offset from a start time computed from the current time, so each run produces the same history shifted to end now.
- **Privacy check.** Before each screenshot, `capture.ts` refuses the page if it shows an address outside `example.com`, `example.org`, `100.64.0.x`, and the documentation range `192.0.2.x`, or a home-directory path. Pass `--deny a,b` to refuse extra strings, such as your user name. Run it from a directory whose path does not contain your user name, or the Settings page shows that path. On Windows, `subst T: <temp dir>` then `--dir T:/taproot-demo` works.
- **Read the images** before committing them. `build-web.sh` rewrites `internal/taproot/web/dist/index.html`; restore it with `git checkout` before you commit. The chunks it writes to `internal/taproot/web/dist/assets/` are ignored by git.
