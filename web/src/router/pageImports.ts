import { ROUTES } from './routes';

/** Each page is its own chunk, so a load downloads and compiles only the page it shows. */
export const pageImports = {
  dashboard: () => import('@/features/dashboard/DashboardPage'),
  accounts: () => import('@/features/accounts/AccountsPage'),
  oauthExcluded: () => import('@/pages/AuthFilesOAuthExcludedEditPage'),
  oauthModelAlias: () => import('@/pages/AuthFilesOAuthModelAliasEditPage'),
  routing: () => import('@/features/routing/RoutingPage'),
  pluginResource: () => import('@/features/plugins/PluginResourcePage'),
  plugins: () => import('@/features/plugins/PluginsPage'),
  pluginStore: () => import('@/features/plugins/PluginStorePage'),
  config: () => import('@/features/config/ConfigPage'),
  system: () => import('@/pages/SystemPage'),
  usage: () => import('@/features/taproot-usage/TaprootUsagePage'),
  requestLogs: () => import('@/features/taproot-logs/TaprootLogsPage'),
  backups: () => import('@/features/taproot-backups/TaprootBackupsPage'),
  settings: () => import('@/features/taproot-settings/TaprootSettingsPage'),
};

// Every route name is also a page name, so this fails to compile when a route has no page.
const preloadByPath = new Map<string, () => Promise<unknown>>(
  (Object.keys(ROUTES) as (keyof typeof ROUTES)[]).map((name) => [ROUTES[name], pageImports[name]])
);

/**
 * Starts downloading a hash-route path's page chunk, so it arrives while the connection gate
 * waits for the server instead of after it.
 */
export function preloadPage(path: string) {
  void preloadByPath
    .get(path)?.()
    .catch(() => undefined);
}
