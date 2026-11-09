# Security policy

Taproot stores OAuth refresh tokens for your Claude and Codex accounts, and its request logs hold every prompt and response. Anyone who reaches its admin surface can read those tokens. This page describes the access model, how to run taproot safely, and how to report a vulnerability.

## Access model

Taproot identifies every caller by the connection's source address. It ignores forwarding headers and `Tailscale-*` headers. For a tailnet address, it asks the host's `tailscaled` who the caller is.

The server has two surfaces:

- **Admin surface:** the dashboard, `/taproot/**`, `/v0/management/**`, `/v8/management/**`, plugin pages under `/v0/resource/plugins/**`, the OAuth callbacks, and `/v1/ws`. These read or change credentials, settings, backups, and plugins.
- **Proxy surface:** everything else, such as `/v1/messages` and `/v1/responses`. These spend your accounts' quota.

| Caller | Proxy | Admin |
| --- | --- | --- |
| Loopback inside the container | yes | yes |
| A device with `proxy: true` in `access.devices` | yes | no |
| A device with `admin: true` in `access.devices` | no | yes |
| The server's own Tailscale node | yes | no |
| An address in `access.addresses` | yes | no |
| Any other device, including a new one, and anyone else, including whois failures | no | no |

The admin surface needs no key: for admitted callers, the guard adds the management key inside the process, and the key never reaches a browser. Because of that, the guard also refuses browser requests made on behalf of another site: a `Host` that is not one of the server's names, a cross-origin `Origin`, cross-site `Sec-Fetch-Site` values, and a state-changing `GET` without `X-Taproot-Request: 1`. Upstream's Redis-protocol usage queue on the same port accepts loopback connections only.

Permissions belong to a device's Tailscale stable ID, which is never reused; names grant nothing. Taproot refuses unknown keys in `taproot.yaml`, and refuses CLIProxyAPI's Home mode and `--tui --standalone`, which would start the server without the guard. The [README](README.md#how-access-works) and [`internal/taproot/DESIGN.md`](internal/taproot/DESIGN.md) describe the model in full.

## Run taproot safely

- **Never run with the guard off.** With `access.enabled: false`, anyone who reaches the port can use the proxy and spend your quota. The admin surface then answers loopback callers only, and still refuses browser requests from other sites. Outside a container, or with host networking, every process on the host is a loopback caller and so an admin, and nothing checks who calls the proxy.
- **Never expose the port beyond your tailnet.** Publish it on the host's Tailscale address only, never on `0.0.0.0`, a LAN address, or the internet. Do not use `tailscale funnel`.
- **Never put anything in front of taproot on the same host.** `tailscale serve`, a reverse proxy, or an SSH tunnel usually connects from loopback, and loopback callers are admins.
- **Do not publish the port on `127.0.0.1`.** A container runtime that delivers host connections with a loopback source makes every process on the host an admin.
- **Give Admin only to devices you use the dashboard from**, and keep `access.addresses` to single addresses.
- **Treat the data directory and every backup like a password.** They hold the OAuth tokens.
- **Check the deployment after every change**, with the steps in the README's [Check the deployment](README.md#5-check-the-deployment).

## Scope

In scope:

- Reaching the admin surface, or any credential, setting, backup, or request log, without being an admitted admin caller.
- Reaching the proxy without being an admitted caller.
- A web page that makes an admin's browser read or change data through taproot (CSRF, DNS rebinding, CORS, framing).
- OAuth tokens, the management key, or account emails leaking through responses, logs, backups, or the dashboard.
- A `taproot.yaml` or startup path that silently weakens the guard.
- Any of the above caused by upstream CLIProxyAPI code as taproot ships it.

Out of scope:

- Deployments that ignore the warnings above, such as the guard turned off, the port published beyond the tailnet, or a proxy in front of taproot.
- Attacks that need root on the server host, control of an admin device, or control of your tailnet.
- Plugins. A plugin runs inside the server process with full access, so install only plugins you trust.
- The providers' terms of service. See the README's [Terms of service](README.md#terms-of-service).
- Bugs in upstream CLIProxyAPI that do not affect taproot's access model or data. Report those to [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI).

## Supported versions

Only the latest minor release gets security fixes. For example, once `taproot-v0.3.0` is out, a fix ships in a `0.3.x` release and not in a `0.2.x` release. [`docs/stability.md`](docs/stability.md) explains how to upgrade.

## Report a vulnerability

Use GitHub's private vulnerability reporting on this repository: open the **Security** tab and select **Report a vulnerability**. Do not open a public issue or pull request for a vulnerability.

Include the taproot version or commit, how you deployed it (container runtime, publish addresses, and the relevant parts of `taproot.yaml` with logins and node IDs removed), the steps to reproduce, and what an attacker gains.

The maintainer acknowledges every report within 7 days. Fixes land on the `main` branch and ship in the next release of the latest minor version.
