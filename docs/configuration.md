# Configuration

Taproot keeps its configuration and data in one data directory. This page lists what the directory holds, every `taproot.yaml` key, the environment variables, and the `config.yaml` settings that work differently under taproot.

## The data directory

The containers mount the data directory at `/taproot/config`. The NixOS module and the systemd unit use `/var/lib/taproot`.

| Path | Contents |
| --- | --- |
| `config.yaml`, `taproot.yaml` | Configuration, written on the first start |
| `auths/` | OAuth credential files |
| `auths/.codex-refresh/` | The Codex refresh journal, a record of each Codex token refresh |
| `taproot/taproot.db` | Usage, quota, and the request-log index (SQLite) |
| `taproot/request-logs/` | Compressed request logs |
| `taproot/backups/` | Backup archives |

The data directory holds your OAuth refresh tokens and every prompt. Treat it, and every backup, like a password. Mount the whole directory into a container, not single files, because a restore replaces the files by rename, which fails on a single-file mount.

A backup holds `config.yaml`, `taproot.yaml`, the database, the credential files in `auths/`, and the Codex refresh journal. It leaves out the request logs and `auths/kv.db`, a cache that taproot rebuilds. A restore adds the journal entries that the auth directory lacks and never deletes one, so taproot does not send a Codex refresh token again that the journal records as sent.

## The two configuration files

- `config.yaml` is CLIProxyAPI's configuration. On the first start, taproot writes [`examples/taproot/config.yaml`](../examples/taproot/config.yaml), the minimal configuration for taproot, with `oauth.auth-dir` moved beside the file. CLIProxyAPI ignores keys it does not know, so a taproot setting in `config.yaml` has no effect.
- `taproot.yaml` holds taproot's own settings. Taproot reads it strictly, so an unknown key stops startup with the key's name. The dashboard's **Access** and **Settings** pages edit it in place. [`examples/taproot/taproot.yaml`](../examples/taproot/taproot.yaml) shows a filled-in file.

Taproot never replaces an existing file. If `config.yaml` exists and `taproot.yaml` does not, taproot stops and names the missing file.

## taproot.yaml

Every key is optional. Sizes accept a number of bytes or a value such as `512MiB` or `20GB`. Both suffixes mean powers of 1024.

| Key | Default | Meaning |
| --- | --- | --- |
| `data-dir` | `taproot` | Directory for the database, request logs, and backups. A relative path is relative to `config.yaml`. |
| `store.path` | `taproot.db` | The SQLite database. A relative path is relative to `data-dir`. |
| `access.devices` | empty | Each device's permissions. A device that is not listed has neither permission. |
| `access.devices[].id` | | The device's Tailscale stable ID. |
| `access.devices[].name` | | The device's Tailscale name. Taproot keeps it in step with the tailnet. |
| `access.devices[].label` | empty | A name you give the device. Tailscale renames never change it. |
| `access.devices[].proxy` | `false` | The device may send model requests. |
| `access.devices[].admin` | `false` | The device may open the dashboard and change settings. |
| `access.addresses` | empty | Addresses outside Tailscale that may use the proxy: `cidr` and an optional `note`. |
| `access.extra-hosts` | empty | More `Host` names the server answers to. |
| `access.tailscale-socket` | `/var/run/tailscale/tailscaled.sock` | The `tailscaled` LocalAPI socket. |
| `access.cache-ttl` | `1m` | How long taproot caches which device an address belongs to. |
| `access.enabled` | `true` | Keep it `true`. `false` opens the proxy to every caller and limits the dashboard and APIs to loopback. |
| `ingest.poll-interval` | `15m` | How often taproot polls the quota of idle accounts. `0` turns polling off. |
| `request-logs.max-days` | `14` | Delete request logs older than this many days. A negative value keeps them forever. |
| `request-logs.max-bytes` | `20GiB` | Delete the oldest request logs when the directory passes this size. A negative value removes the limit. |
| `request-logs.exclude-paths` | empty | Paths never stored, such as `/v1/models` or `HEAD /api/*`. |
| `request-logs.keep-unrouted-404` | `false` | Store 404 responses for paths the proxy does not serve. |
| `request-logs.queue-bytes` | `256MiB` | Memory for records that wait to be written. Past it, taproot drops the bodies, and the record says so. |
| `request-logs.segment-max-bytes` | `128MiB` | Stored size at which a segment file closes. |
| `request-logs.open-max-bytes` | `4GiB` | Total raw size of open segments. Past it, the largest one closes early. |
| `request-logs.seal-idle` | `15m` | Close a session's segment after this long without requests. |
| `request-logs.seal-age` | `6h` | Close a segment this long after it opened. |
| `backups.daily-at` | `00:00` | Time of the daily backup, as `HH:MM` in the `TZ` zone, or `off`. |
| `backups.keep` | `14` | Backups kept of each kind: scheduled, manual, and pre-restore. |
| `alerts.discord-webhook` | empty | Discord webhook URL. It is a secret. |
| `alerts.enabled` | `true` | Turns alerts off without removing the webhook. |

The 0.1.0 keys `access.allow-logins`, `access.admin-nodes`, and `access.trusted-cidrs` stop startup with a message that names their replacement, `access.devices` or `access.addresses`.

## Environment variables

| Variable | Meaning |
| --- | --- |
| `TZ` | Time zone of the backup schedule, such as `Etc/UTC` or `Europe/Berlin`. The image sets none. |
| `TAPROOT_CONFIG` | Path of `taproot.yaml`, when it is not beside `config.yaml`. |

## config.yaml

[`config.example.yaml`](../config.example.yaml) documents every CLIProxyAPI key, such as `routing`, `requests`, and the per-provider `upstream` settings. You need it only for advanced settings. Taproot replaces or ignores some of them:

- Keep `access.api-keys` empty. Taproot admits callers by Tailscale identity, and clients send a placeholder key.
- Leave `management.secret-key` unset, and keep `management.allow-remote` and the control-panel keys as the minimal configuration sets them. Taproot sets the management key itself, through the `MANAGEMENT_PASSWORD` variable, adds it to the requests it admits, and serves its own dashboard.
- The access guard ignores forwarding headers, so `server.trusted-proxies` does not change who reaches taproot.
- `observability.logs.request-log` turns taproot's request logs on and off. The dashboard's **Record requests** switch writes it.
- Taproot refuses to start in CLIProxyAPI's Home mode.
- Do not use CLIProxyAPI's Postgres, git, or object stores (the `PGSTORE_*`, `GITSTORE_*`, and `OBJECTSTORE_*` variables). Taproot is tested only with local files, and a backup captures only the local copies.
