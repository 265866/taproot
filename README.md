# taproot

Taproot pools your Claude and Codex subscription accounts behind one endpoint on your tailnet. Claude Code and Codex on every device you allow send their requests to it, and taproot spreads them across the accounts. Only tailnet devices reach it, and it decides what each device may do by its Tailscale identity, so there are no API keys to hand out. Taproot is for one person or one household with a Linux server. It is a fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) and adds these features:

- **Per-device access control.** Each tailnet device gets its own permissions: Proxy to send model requests, Admin to open the dashboard. New devices start with neither. A device you haven't allowed shows up as a request you can approve, including devices from your other tailnets. See [Access](docs/access.md).
- **Soonest-reset-first routing.** New sessions go to the account whose weekly window resets soonest, so quota that is about to reset is spent first. A running session stays on its account.
- **Quota ledger.** Taproot records every account's 5-hour and weekly use and projects when each pool runs empty.
- **Usage and cost.** Requests, tokens, latency, and failures by account, model, session, and device, priced at API rates from an editable table.
- **Request logs.** Every request and response, compressed, searchable, and deleted by age or total size.
- **Backups.** A daily archive of the configuration, credentials, and database, which you can download or restore from the dashboard.
- **Discord alerts.** A limit hit, an empty pool, a broken login, an expiring Claude reset grant, and a device that asks for access.

CLIProxyAPI's other providers still work through the proxy, without quota tracking or routing.

> [!WARNING]
> Read [Terms of service](docs/terms-of-service.md) and [SECURITY.md](SECURITY.md) before you add an account. Using subscription credentials through a proxy can break the provider's terms, and a misconfigured taproot gives your OAuth tokens to anyone who can reach its port.

![The Accounts page: the pool chart and quota ledger for five demo accounts, with emails hidden](docs/screenshots/accounts.png)

## Quick start

You need a Linux server on your tailnet with `tailscaled` and rootless Podman 5 or later, and a second tailnet device with a browser. For Docker, NixOS, or a plain binary, see [Install](docs/install.md).

1. Start the container:

   ```sh
   mkdir -p "$HOME/taproot/data"
   podman run -d --name taproot --restart unless-stopped \
     --network pasta \
     -e TZ=Etc/UTC \
     -v "$HOME/taproot/data:/taproot/config" \
     -v /var/run/tailscale:/var/run/tailscale:ro \
     -p "$(tailscale ip -4):8317:8317" \
     ghcr.io/265866/taproot:0.3
   ```

   So taproot keeps recognizing devices after tailscaled restarts or updates, apply the one-time host setting in [Keep tailscaled's folder for a container](docs/install.md#keep-tailscaleds-folder-for-a-container).

2. Claim the server. Find the setup code:

   ```sh
   podman logs taproot 2>&1 | grep 'setup code [A-Z0-9]'
   ```

   On the device you will manage taproot from, open `http://<server>:8317/`, where `<server>` is the host's MagicDNS name or Tailscale address. If you published another port, use it instead of 8317. Enter the code and select **Claim this server**. That device gets Proxy and Admin, and the code stops working.

3. Add an account. On the **Accounts** page, select **Add account** and sign in with Claude or Codex. The provider then sends you to a `http://localhost:...` address that does not load, so copy that whole address into **Callback URL** and select **Submit Callback URL**. [Accounts](docs/accounts.md) has the details.

4. Point Claude Code and Codex at taproot. Taproot ignores the API key, so any placeholder works. For Claude Code, set these variables in your shell profile:

   ```sh
   export ANTHROPIC_BASE_URL="http://<server>:8317"
   export ANTHROPIC_AUTH_TOKEN="taproot"
   ```

   For Codex, add this to `~/.codex/config.toml` (see [Codex advanced configuration](https://learn.chatgpt.com/docs/config-file/config-advanced)):

   ```toml
   model_provider = "taproot"

   [model_providers.taproot]
   name = "taproot"
   base_url = "http://<server>:8317/v1"
   wire_api = "responses"
   ```

5. Check the proxy with `curl http://<server>:8317/v1/models`. The list includes the models of the accounts you added. To let another device use taproot, see [Access](docs/access.md).

This container does not start again after the host reboots. To run taproot as a service, follow [Install](docs/install.md).

## Documentation

- [Install](docs/install.md): the Podman quadlet, Docker Compose, NixOS, and a plain binary with systemd.
- [Accounts](docs/accounts.md): add Claude and Codex accounts and move existing credentials.
- [Access](docs/access.md): which devices may use the proxy and the dashboard, and how to allow them.
- [Update taproot](docs/updating.md): manual and automatic updates, rollback, and moving from 0.1.0.
- [Verify a release](docs/verifying-releases.md): check the signature and attestation of an image or tarball.
- [Configuration](docs/configuration.md): the data directory, every `taproot.yaml` key, and the `config.yaml` settings taproot changes.
- [Dashboard](docs/dashboard.md): the pages, with desktop and phone screenshots.
- [Troubleshooting](docs/troubleshooting.md): fixes for common errors, listed by symptom.
- [Stability](docs/stability.md): what a release can change.
- [Terms of service](docs/terms-of-service.md): what the providers' terms say about proxies.
- [Security policy](SECURITY.md): how to run taproot safely and report a vulnerability.
- [Changelog](CHANGELOG.md): what changed in each release.
- [Contributing](CONTRIBUTING.md): the rules for changes, the checks, and the demo server.
- [Design](internal/taproot/DESIGN.md): how taproot is built and why.
- [Dashboard development](web/README.md): the dashboard's structure and conventions.
- Upstream CLIProxyAPI references: the [v8 Management API](docs/management-api-v8.md) and [LAN AI gateway discovery](docs/lan-discovery.md). Their management-key authentication does not apply under taproot.

## License

Taproot is released under the [MIT License](LICENSE), the license of CLIProxyAPI. The dashboard in `web/` keeps the Management Center's [MIT License](web/LICENSE). The request-log codec links Rust crates under the GNU LGPL v3, so keep [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [licenses/](licenses) with every copy you pass on.

- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI): Copyright (c) Luis Pater and Router-For.ME.
- [CLIProxyAPI Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center): Copyright (c) Router-For.ME.
- Most provider icons come from [Lobe Icons](https://github.com/lobehub/lobe-icons).

Taproot is not affiliated with Router-For.ME, Anthropic, OpenAI, or Tailscale. Claude, Codex, OpenAI, Tailscale, and the other provider names and logos are trademarks of their owners.
