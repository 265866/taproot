# taproot

Taproot pools your Claude and Codex subscription accounts behind one endpoint on your tailnet, with a dashboard for quota, usage, request logs, and backups. Callers are admitted by their Tailscale identity, so there are no API keys to hand out.

Taproot is a fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) by Router-For.ME, and its dashboard is a fork of the [CLIProxyAPI Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center). Both are MIT-licensed. Taproot is not affiliated with Router-For.ME, Anthropic, or OpenAI.

> [!WARNING]
> Read [Terms of service](#terms-of-service) and [Security](#security) before you add an account. Using subscription credentials through a proxy can break the provider's terms, and a misconfigured taproot gives your OAuth tokens to anyone who can reach its port.

<!-- TODO(screenshot): the Accounts page with generic demo accounts, emails hidden. -->

## Who it is for

Taproot is for one person, or one household, who runs a Linux server on a Tailscale tailnet and uses Claude Code and Codex from several devices with more than one subscription account. It is not a multi-tenant service and has no user accounts of its own.

## How it differs from CLIProxyAPI

Taproot keeps CLIProxyAPI's proxy, provider protocols, and configuration, and adds the following features. Most of their code lives in `internal/taproot`.

- **Tailscale access guard.** Callers are identified by source address through the host's `tailscaled`. Proxy API keys and the management key prompt are gone.
- **Quota ledger.** Taproot records every account's 5-hour and weekly utilization from response headers and from a poller for idle accounts. The dashboard projects when each pool runs empty and when it resets.
- **Soonest-reset-first routing.** New sessions go to the eligible account whose weekly window resets soonest, so quota that is about to reset is spent first. Session affinity keeps a running session on its account.
- **Usage and cost.** Requests, tokens (including cache reads and writes), latency, time to first token, and failures, by account, model, session, and Tailscale device. Costs are API-equivalent prices from an editable price table.
- **Request logs.** Full request and response records, stored per session as zstd streams, indexed in SQLite, searchable, and pruned by age and total size.
- **Backups.** A daily archive of `config.yaml`, `taproot.yaml`, the credential files, and the database. You can download or restore one from the dashboard.
- **Discord alerts.** A limit hit, an empty pool, a broken login, and an expiring Claude reset grant.
- **One Accounts page.** Quota, credentials, and sign-in for every account in one place.
- **Email redaction.** Account emails are hidden everywhere in the dashboard until you turn on **Show emails** for the session.

Taproot's quota tracking and routing cover Claude and Codex. CLIProxyAPI's other providers still work through the proxy, without those features.

## How access works

The guard runs before every route and looks only at the connection's source address. Forwarding headers and `Tailscale-*` headers are ignored.

| Caller | Proxy (`/v1/...`) | Dashboard and management API |
| --- | --- | --- |
| Loopback inside the container | yes | yes |
| A login in `access.allow-logins` on a device in `access.admin-nodes` | yes | yes |
| A login in `access.allow-logins` on any other device | yes | no |
| The server's own Tailscale node | yes | no |
| An address in `access.trusted-cidrs` | yes | no |
| Anyone else, including whois failures | no | no |

For a tailnet address, the guard asks `tailscaled` who the caller is through its LocalAPI socket, and caches the answer for one minute. For admitted callers, the guard adds the management key to management requests inside the process. The key never reaches a browser. `/healthz` is open to every caller.

The dashboard page itself loads for every admitted caller, because it holds no data. A device outside `admin-nodes` sees why the API refuses it, with the device name to add.

The guard also refuses browser requests from other sites: a `Host` that is not one of the server's names, a cross-origin `Origin`, and a state-changing `GET` without the `X-Taproot-Request: 1` header. Command-line clients send none of these headers and are not affected. To reach the server under another name, add it to `access.extra-hosts`.

### Security

Taproot stores OAuth refresh tokens for your accounts, and the request logs hold every prompt and response. Treat the data directory and every backup like a password. To report a vulnerability, see [SECURITY.md](SECURITY.md).

- **Do not put anything in front of taproot.** Loopback callers are admins. `tailscale serve`, `tailscale funnel`, a reverse proxy, or an SSH tunnel on the same host usually connects from loopback, so every caller it forwards becomes an admin.
- **Publish the port on the host's Tailscale address only.** Never publish on `0.0.0.0` or `127.0.0.1`. A container runtime that delivers host connections with a loopback source makes every process on the host an admin.
- **Check the result after every deployment change.** Run the checks in [Check the deployment](#4-check-the-deployment).
- **Do not set `access.enabled: false`.** It turns off every check on the proxy, so anyone who reaches the port can spend your accounts' quota without a login. The dashboard and the management and taproot APIs then answer loopback only, which means only processes inside the container can use them.
- **List every login in `allow-logins`.** Taproot has no built-in login and refuses to start while the list is empty. It also refuses unknown keys in `taproot.yaml`, so a misspelled key stops startup with the key's name instead of being ignored.
- **Pin admin devices by stable node ID.** A MagicDNS name can be claimed by another device of the same login after the original is renamed or deleted. An ID is never reused.
- **Keep `trusted-cidrs` to single addresses** (`/32`). Every host in a wider range reaches the proxy without a login.
- **Home mode and the TUI's embedded server are refused.** CLIProxyAPI's Home mode (`-home-jwt`) and `--tui --standalone` would start the server without taproot's guard, so taproot exits with an error instead. `--tui` alone, connected to a running server, still works.

## Quick start

You need:

- A Linux host on your tailnet, with `tailscaled` running. Docker Desktop on macOS and Windows does not keep caller addresses and has no `tailscaled` socket, so it cannot run taproot.
- Rootful Docker, or rootless Podman 5 or later (which uses `pasta` and keeps caller addresses).
- Git.

### 1. Build the image

```sh
git clone https://github.com/265866/taproot.git
cd taproot
docker build -f Containerfile.taproot \
  --build-arg VERSION="$(git describe --tags --always)" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t taproot .
```

With Podman, run `podman build` with the same arguments.

### 2. Write the configuration

Taproot reads two files from one directory. `config.yaml` is CLIProxyAPI's configuration. `taproot.yaml` holds taproot's settings, because CLIProxyAPI rejects unknown keys in `config.yaml`.

```sh
mkdir -p data
cp examples/taproot/config.yaml examples/taproot/taproot.yaml data/
```

Edit `data/taproot.yaml`:

1. Put your Tailscale login in `access.allow-logins`. `tailscale whois <your-device-ip>` shows it.
2. Put the stable ID of each device that may use the dashboard in `access.admin-nodes`, as `id:<ID>`. The `ID` field of `Self` in `tailscale status --json`, run on that device, is its stable ID.

```yaml
access:
  allow-logins:
    - alice@example.com
  admin-nodes:
    - id:nEXAMPLE1CNTRL
```

Mount the directory, not the single files. Restoring a backup replaces the files by rename, which fails on a single-file mount.

### 3. Start the container

```sh
TS_IP="$(tailscale ip -4)"
docker run -d --name taproot --restart unless-stopped \
  --user "$(id -u):$(id -g)" \
  -e TZ=Etc/UTC \
  -v "$PWD/data:/taproot/config" \
  -v /var/run/tailscale/tailscaled.sock:/var/run/tailscale/tailscaled.sock \
  -p "$TS_IP:8317:8317" \
  taproot
```

For rootless Podman, use the same command with `podman run`, without the `--user` line, and the image name `localhost/taproot`. On an SELinux host, add `:Z` to the data volume. `TZ` sets the local time of the daily backup.

To run taproot as a systemd user service under rootless Podman instead, use the quadlet in [`examples/taproot/taproot.container`](examples/taproot/taproot.container). It runs the container with a read-only root filesystem and no capabilities, publishes only on the host's Tailscale address, and its header lists the install steps.

The image has no user of its own; the process runs as whatever user the container starts with. Under rootless Podman that is the container's root, which Podman's default user namespace maps to your own user, so taproot reads and writes `data/` as you and has no more rights on the host than you do. Do not pass `--user` there: Podman would map that uid to a subordinate uid that cannot read the files in `data/`, which taproot keeps at mode 0600. Rootful Docker has no such mapping, and container root is the host's root, so the Docker command passes `--user` to run as the owner of `data/`.

The data directory then holds:

| Path | Contents |
| --- | --- |
| `data/config.yaml`, `data/taproot.yaml` | Configuration |
| `data/auths/` | OAuth credential files |
| `data/taproot/taproot.db` | Usage, quota, and request-log index (SQLite) |
| `data/taproot/request-logs/` | Compressed request logs |
| `data/taproot/backups/` | Backup archives, mode 0600 |

### 4. Check the deployment

Run each check, and stop if any result differs.

1. From a device in `admin-nodes`, open `http://<server>:8317/taproot/api/v1/me`. It must show `"via":"tailnet"` and `"admin":true`. If it shows `"via":"loopback"`, something rewrites caller addresses and every caller is an admin.
2. If you have a device of the same login that is not in `admin-nodes`, the same URL must return 403 there, with the code `not_admin_node`.
3. On the server host, `curl http://$TS_IP:8317/taproot/api/v1/me` must return 403.

`<server>` is the host's MagicDNS name or Tailscale address.

## Add accounts

Open `http://<server>:8317/` from an admin device, go to **Accounts**, and select **Add account**.

1. Select a provider under **Sign in with a provider**. The provider's sign-in page opens.
2. Sign in. The provider then redirects to a `http://localhost:...` address, which does not load on your device.
3. Copy the full address from the browser's address bar, paste it into **Callback URL**, and select **Submit Callback URL**.

To move an existing credential instead, select **Upload auth file** and choose its JSON file. Taproot stores credentials in `data/auths/`.

Only one proxy may use a credential at a time. Claude rotates refresh tokens, so two proxies that refresh the same account log each other out.

## Connect clients

Clients send any placeholder key. Taproot ignores it.

Claude Code:

```sh
export ANTHROPIC_BASE_URL="http://<server>:8317"
export ANTHROPIC_AUTH_TOKEN="taproot"
```

Codex, in `~/.codex/config.toml` (see [Codex advanced configuration](https://learn.chatgpt.com/docs/config-file/config-advanced)):

```toml
model_provider = "taproot"

[model_providers.taproot]
name = "taproot"
base_url = "http://<server>:8317/v1"
wire_api = "responses"
```

## The dashboard

The dashboard is served at `/` on the proxy port. It has these sections:

- **Accounts:** pool charts, the quota ledger, credentials, and sign-in.
- **Usage:** cost, tokens, latency, and failures, by account, model, session, and device.
- **Request Logs:** search and view stored requests. The application log is a tab here.
- **Backups:** create, download, and restore backups.
- **Settings:** the access lists, retention, backup schedule, poller interval, and Discord alerts.
- **Routing, Config, Plugins, Plugin Store, and System:** CLIProxyAPI's settings and plugins.

<!-- TODO(screenshot): the Usage page with generic demo data. -->

## Configuration reference

Set `access.allow-logins`. Every other key in `taproot.yaml` is optional. An unknown key stops the server at startup. A relative `data-dir` resolves against the directory of `config.yaml`.

| Key | Default | Meaning |
| --- | --- | --- |
| `data-dir` | `taproot` | Database, request logs, and backups. |
| `access.allow-logins` | required | Tailscale logins that reach the proxy. Taproot refuses to start without one. |
| `access.admin-nodes` | empty | Devices that reach the dashboard and management API: `id:<StableID>` or a MagicDNS name. Empty means no tailnet device. |
| `access.trusted-cidrs` | empty | Addresses that reach the proxy without a login. |
| `access.extra-hosts` | empty | More `Host` names the server answers to. |
| `access.tailscale-socket` | `/var/run/tailscale/tailscaled.sock` | The `tailscaled` LocalAPI socket. |
| `access.cache-ttl` | `1m` | How long a whois answer is cached. |
| `access.enabled` | `true` | Keep it `true`. `false` opens the proxy to every caller and limits the dashboard and APIs to loopback. See [Security](#security). |
| `ingest.poll-interval` | `15m` | Quota poll interval for idle accounts. `0` disables polling. |
| `request-logs.max-days` | `14` | Retention by age. A negative value disables the limit. |
| `request-logs.max-bytes` | `20GiB` | Retention by size of the request-log directory on disk. A negative value disables the limit. |
| `request-logs.exclude-paths` | empty | Paths never stored, such as `HEAD /api/*`. |
| `backups.daily-at` | `03:13` | Local time of the daily backup, or `off`. |
| `backups.keep` | `14` | Backups kept of each kind. |
| `alerts.discord-webhook` | empty | Discord webhook URL. It is a secret. |
| `alerts.enabled` | `true` | Turns alerts off without removing the webhook. |

The environment variable `TAPROOT_CONFIG` overrides the location of `taproot.yaml`. `config.example.yaml` documents every key of `config.yaml`. Request logs are stored only while `observability.logs.request-log` is `true` in `config.yaml`.

## Terms of service

This section is a warning. It is not legal advice.

Taproot is for personal use of your own subscription accounts, at your own risk. It signs in to those accounts with OAuth, stores the tokens, and sends requests from every client on your tailnet through them. The providers' terms address this directly:

- Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) say: "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else." They also forbid access "through automated or non-human means" except with an API key or where Anthropic expressly permits it.
- Anthropic's [Claude Code legal and compliance page](https://code.claude.com/docs/en/legal-and-compliance) says: "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, Max, or Ultra plan credentials on behalf of their users." It also says that advertised plan limits "assume ordinary, individual usage".
- Anthropic's [Usage Policy](https://www.anthropic.com/legal/aup) lists "services that route requests through consumer subscriptions or misrepresent the product or client being used" under unauthorized access.
- OpenAI's [Terms of Use](https://openai.com/policies/terms-of-use/), effective January 1, 2026, say under "Registration and access": "You may not share your account credentials or make your account available to anyone else and are responsible for all activities that occur under your account." Under "Using our Services", one of the things you may not do is: "Automatically or programmatically extract data or Output". The terms do not mention Codex.

CLIProxyAPI, by default, presents clients other than Claude Code as the Claude Code CLI on Claude accounts. `upstream.claude.disable-claude-cloak-mode` in `config.yaml` turns that off.

A provider can suspend or close an account that breaks its terms. Use taproot only with accounts you own, for your own use. Read each provider's current terms before you add an account, and decide for yourself whether your use is allowed.

## Build from source

Taproot needs Go 1.26 or later, and Bun for the dashboard.

```sh
bash scripts/taproot/build-web.sh
go build -o taproot ./cmd/server
./taproot --config ./config.yaml
```

`build-web.sh` builds `web/` into one HTML file and copies it to `internal/taproot/web/dist/index.html`, which the binary embeds. Without that step, the binary serves a placeholder page.

If you start from `examples/taproot/config.yaml`, change `oauth.auth-dir` to a path on your machine. Outside a container, every process on the machine can connect from loopback, so every process is an admin. Use this for development only.

To check a change, run `go test ./...` and, in `web/`, `bun run verify`.

## Upstream sync

Taproot's Go module path is `github.com/265866/taproot`, and upstream's is `github.com/router-for-me/CLIProxyAPI/v8`. The sync script rewrites each upstream snapshot to taproot's path before it merges, so import lines do not conflict. Taproot changes only a few upstream files, each listed with its reason in [`internal/taproot/PLAN.md`](internal/taproot/PLAN.md).

To merge CLIProxyAPI's `main` branch:

```sh
git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
scripts/taproot/sync-upstream.sh --dry-run
scripts/taproot/sync-upstream.sh
```

The script commits `upstream/main` with the module path rewritten to the branch `taproot/upstream`, merges that branch into the current branch, then runs `gofmt`, `go build`, `go vet`, and the taproot and SDK tests. Files taproot deleted stay deleted. It never pushes. The dashboard does not follow the Management Center's changes.

## License and credits

Taproot is released under the [MIT License](LICENSE), the license of CLIProxyAPI. The dashboard in `web/` keeps the Management Center's [MIT License](web/LICENSE). The licenses of the Go modules and npm packages taproot ships are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI): Copyright (c) Luis Pater and Router-For.ME.
- [CLIProxyAPI Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center): Copyright (c) Router-For.ME.
- Most provider icons come from [Lobe Icons](https://github.com/lobehub/lobe-icons). Claude, Codex, OpenAI, and the other provider names and logos are trademarks of their owners.
