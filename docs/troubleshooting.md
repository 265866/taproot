# Troubleshooting

Find the symptom you see. Each section gives the cause and the fix.

## Taproot refuses every caller, or `/me` shows `loopback`

Something between the caller and taproot replaces the caller's address.

- **Rootless Docker** shows taproot every caller as the Docker bridge's gateway address, so taproot cannot identify any device and refuses them all. Use rootless Podman with `--network pasta`, or rootful Docker. See [Install](install.md#requirements).
- **A proxy on the same host**, such as `tailscale serve`, a reverse proxy, or an SSH tunnel, connects from loopback, and loopback callers are admins. Remove it and publish the port on the host's Tailscale address.

Then run [Check the deployment](access.md#check-the-deployment) again.

## Every tailnet device is refused after tailscaled restarts or updates

Taproot runs in a container and still holds the `tailscaled` socket from before the restart, so it cannot identify any device, admins included. Taproot logs `tailscaled LocalAPI ... has failed` and, when alerts are on, posts "Taproot can't reach tailscaled" after 2 minutes.

Restart taproot now, for example with `systemctl --user restart taproot`. To keep it from happening again, mount the folder `/var/run/tailscale` rather than the socket file, as the [examples](../examples/taproot) do, and keep that folder across `tailscaled` restarts with the host setting in [Keep tailscaled's folder for a container](install.md#keep-tailscaleds-folder-for-a-container).

## Taproot cannot write to `data/` under Docker Compose

Docker created `data/` as root because it did not exist before the first `docker compose up`. Stop taproot, then give the directory to your user with `sudo chown -R "$(id -u):$(id -g)" data`, and start taproot again.

## A browser on the server cannot claim taproot or open the dashboard

A browser on the server connects as the server's own node, which never gets Admin. Claim the server and open the dashboard from another device on the tailnet.

## The browser shows "Taproot doesn't answer to this name"

You opened taproot by a name it does not know, such as a local DNS name, an `/etc/hosts` alias, or a custom domain. Taproot answers only to `localhost`, its Tailscale name and addresses, and the names under `access.extra-hosts`, so that another website cannot reach it through DNS rebinding. Open it by its Tailscale name, such as `http://server:8317/`, or add the name under `access.extra-hosts` in `taproot.yaml` and restart taproot. If the page says taproot cannot read its own Tailscale name, tailscaled is not answering; see [Every tailnet device is refused after tailscaled restarts or updates](#every-tailnet-device-is-refused-after-tailscaled-restarts-or-updates).

## Taproot stops with `taproot.yaml not found`

`config.yaml` exists but `taproot.yaml` does not. Taproot never writes one beside an existing `config.yaml`. Create the file in the directory the error names. An empty file works, or start from [`examples/taproot/taproot.yaml`](../examples/taproot/taproot.yaml). To keep `taproot.yaml` elsewhere, set `TAPROOT_CONFIG` to its path.

## Taproot stops on an unknown key

Taproot reads `taproot.yaml` strictly, and the error names the key and its line. Fix the spelling, or remove the key. [Configuration](configuration.md#taprootyaml) lists every key. A key that a newer patch release added stops an older release too.

## Taproot stops on `access.allow-logins`, `access.admin-nodes`, or `access.trusted-cidrs`

These keys from 0.1.0 were removed. Replace them with `access.devices` and `access.addresses`, as the error says. [Move from 0.1.0](updating.md#move-from-010) shows the steps.

## Claude Code says "Please run /login"

The device has no Proxy permission yet, and its request may wait on the Access page. Do not run `/login`. Open `http://<server>:8317/` in a browser on that device and request access, or allow the device on the Access page. See [Allow a new device](access.md#allow-a-new-device).

## A device is named `device-of-shared-to-user` or "\<owner\>'s device"

The device belongs to a person you shared the taproot server with in Tailscale. Tailscale hides the names of their devices, so the Access page names each one after its owner and its Tailscale IP, such as `dana's device · 100.64.0.31`. The owner sees the same IP in their own Tailscale admin console and apps, so ask them which device it is. Such a device gets no access unless you allow it. See [Allow a device from another tailnet](access.md#allow-a-device-from-another-tailnet).
