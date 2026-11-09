# Dashboard

Taproot serves its dashboard at `/` on the proxy port, such as `http://<server>:8317/`. Only devices with Admin can open it. See [Access](access.md).

## Pages

- **Dashboard:** recent requests and their success rate, the number of credentials and models, and the server's version and runtime settings.
- **Accounts:** pool charts, the quota ledger, credentials, and sign-in. See [Accounts](accounts.md).
- **Usage:** cost, tokens, latency, and failures, by account, model, session, and device.
- **Request Logs:** search and view stored requests. The application log is a tab here.
- **Backups:** create, download, and restore backups.
- **Access:** device requests, every tailnet device with its Proxy and Admin switches, and the addresses that reach the proxy without Tailscale.
- **Settings:** request logs and their limits, the backup schedule, quota polling, and Discord alerts.
- **Routing, Config, Plugins, Plugin Store, and System:** CLIProxyAPI's settings and plugins.

Account emails stay hidden until you select **Show emails**.

## Screenshots

The screenshots use synthetic data from the demo server that [CONTRIBUTING.md](../CONTRIBUTING.md#demo-data-and-screenshots) describes. Each row shows a page on a desktop and on a phone. Select an image to see it at full size.

| Page | Desktop | Phone |
| --- | --- | --- |
| **Accounts:** pool chart and quota ledger | [<img src="screenshots/accounts.png" width="480" alt="Accounts, desktop">](screenshots/accounts.png) | [<img src="screenshots/accounts-mobile.png" width="160" alt="Accounts, phone">](screenshots/accounts-mobile.png) |
| **Manage:** quota, resets, health, credential, and advanced settings for one account | [<img src="screenshots/accounts-manage.png" width="480" alt="Manage, desktop">](screenshots/accounts-manage.png) | [<img src="screenshots/accounts-manage-mobile.png" width="160" alt="Manage, phone">](screenshots/accounts-manage-mobile.png) |
| **Usage:** cost, tokens, cache hits, and latency | [<img src="screenshots/usage-overview.png" width="480" alt="Usage, desktop">](screenshots/usage-overview.png) | [<img src="screenshots/usage-overview-mobile.png" width="160" alt="Usage, phone">](screenshots/usage-overview-mobile.png) |
| **Devices:** usage per tailnet device and the cache TTL check | [<img src="screenshots/usage-devices.png" width="480" alt="Devices, desktop">](screenshots/usage-devices.png) | [<img src="screenshots/usage-devices-mobile.png" width="160" alt="Devices, phone">](screenshots/usage-devices-mobile.png) |
| **Sessions:** session affinity and account switches | [<img src="screenshots/usage-sessions.png" width="480" alt="Sessions, desktop">](screenshots/usage-sessions.png) | [<img src="screenshots/usage-sessions-mobile.png" width="160" alt="Sessions, phone">](screenshots/usage-sessions-mobile.png) |
| **Request Logs:** every proxied request, compressed and searchable | [<img src="screenshots/request-logs.png" width="480" alt="Request Logs, desktop">](screenshots/request-logs.png) | [<img src="screenshots/request-logs-mobile.png" width="160" alt="Request Logs, phone">](screenshots/request-logs-mobile.png) |
| **Request detail:** headers with credentials redacted, and the body | [<img src="screenshots/request-log-detail.png" width="480" alt="Request detail, desktop">](screenshots/request-log-detail.png) | [<img src="screenshots/request-log-detail-mobile.png" width="160" alt="Request detail, phone">](screenshots/request-log-detail-mobile.png) |
| **Routing:** soonest-reset-first and session affinity | [<img src="screenshots/routing.png" width="480" alt="Routing, desktop">](screenshots/routing.png) | [<img src="screenshots/routing-mobile.png" width="160" alt="Routing, phone">](screenshots/routing-mobile.png) |
| **Backups:** scheduled and manual backups, with the next run in server time and your time | [<img src="screenshots/backups.png" width="480" alt="Backups, desktop">](screenshots/backups.png) | [<img src="screenshots/backups-mobile.png" width="160" alt="Backups, phone">](screenshots/backups-mobile.png) |
| **Access:** tailnet devices and requests, with their Proxy and Admin switches | [<img src="screenshots/access.png" width="480" alt="Access, desktop">](screenshots/access.png) | [<img src="screenshots/access-mobile.png" width="160" alt="Access, phone">](screenshots/access-mobile.png) |
| **Settings:** request logs and the backup schedule | [<img src="screenshots/settings.png" width="480" alt="Settings, desktop">](screenshots/settings.png) | [<img src="screenshots/settings-mobile.png" width="160" alt="Settings, phone">](screenshots/settings-mobile.png) |
| **Light theme:** the Accounts page in the light theme | [<img src="screenshots/accounts-light.png" width="480" alt="Light theme, desktop">](screenshots/accounts-light.png) | [<img src="screenshots/accounts-light-mobile.png" width="160" alt="Light theme, phone">](screenshots/accounts-light-mobile.png) |
