<!-- Say what changes for someone who runs taproot, and why. Link the issue it fixes, if any. -->

## Checks

[CONTRIBUTING.md](https://github.com/265866/taproot/blob/main/CONTRIBUTING.md#before-you-send-a-change) explains each check. Tick the ones you ran, and say why you skipped any.

- [ ] `go build ./...`
- [ ] `GOOS=linux go vet ./...`
- [ ] `gofmt -l`, using the `gofmt` of `go.mod`'s Go version, prints nothing
- [ ] `go test -skip '^TestAdvertiserAndBrowser_Integration$' ./...`
- [ ] `golangci-lint run`
- [ ] `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`
- [ ] `cd web && bun install --frozen-lockfile && bun run verify` (dashboard changes)
- [ ] I loaded the changed dashboard page in a browser against a running server (dashboard changes)
- [ ] `docker build -f Containerfile.taproot .` (image, build script, or embedded asset changes)
- [ ] `bun scripts/taproot/third-party-notices.ts` updated `THIRD_PARTY_NOTICES.md` (dependency changes)
- [ ] Tests cover the changed behavior
- [ ] `internal/taproot/DESIGN.md` lists every upstream file I edited, with the reason
- [ ] No real logins, emails, node IDs, addresses, or tokens in code, tests, or screenshots
