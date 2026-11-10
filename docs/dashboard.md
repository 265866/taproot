# Dashboard

Taproot serves its dashboard at `/` on the proxy port, such as `http://<server>:8317/`. Only devices with Admin can open it. See [Access](access.md).

## Pages

- **Dashboard:** answers "is everything OK, and what needs me?" first.
  - **Verdict:** serving normally, degraded, or down, judged on the last 15 minutes and the accounts that can serve right now. A degraded or down verdict links to the failed requests of those 15 minutes.
  - **Needs you:** each problem with a severity, how long it has waited, and one button that fixes it or opens the page that does. **Sign in again** opens the provider's sign-in, **Enable** and **Back up now** run in place and say how they went, and **Review** opens Access at that device's request. When file logging is off, a problem that points at the server's log says to read the server's own output, or `journalctl -u taproot`, rather than linking to an empty tab. The same problems put badges on Accounts, Access, and Backups in the sidebar and a dot in the tab title, on every page.
  - **Pools:** each provider's accounts right now, with what is left on each, how many can serve, quota locked on signed-out accounts or waiting on cooling ones, the next reset, the burn over the last 3 hours, and when the pool runs out.
  - **The window:** 1 hour, 24 hours, 7 days, or 30 days, remembered in this browser. It sets the request count, failures, median latency and time to first token, API-equivalent cost, the traffic chart (with its data as a table), the last 5 failed requests in the window, and the top devices and models, each linking into Request Logs or Usage. Client requests count once, however many accounts they tried; retries show beside them.
  - **First run:** until the server has an account and has served its first request, the window gives way to a three-step checklist: add an account, point Claude Code or Codex at the server (with snippets that use its address), and approve devices. Each step ticks itself off.
  - The page rereads everything every 30 seconds while the tab is visible. When a read fails, the section keeps its last numbers, marks them stale, and offers **Retry**. The model count is on **System**.
- **Accounts:** pool charts, the quota ledger, credentials, and sign-in. See [Accounts](accounts.md).
- **Usage:** cost, tokens, latency, and failures, by account, model, session, and device.
- **Request Logs:** search and view stored requests. The application log is a tab here.
- **Backups:** create, download, and restore backups.
- **Access:** device requests, every tailnet device with its Proxy and Admin switches, and the addresses that reach the proxy without Tailscale.
- **Settings:** request logs and their limits, the backup schedule, quota polling, and Discord alerts.
- **Routing, Config, Plugins, Plugin Store, and System:** CLIProxyAPI's settings and plugins. System also counts the models the proxy serves.

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
