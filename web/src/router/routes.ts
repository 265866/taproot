/** Hash-route paths shared by the route table, the sidebar, and in-page links. */
export const ROUTES = {
  dashboard: '/',
  accounts: '/accounts',
  usage: '/usage',
  requestLogs: '/request-logs',
  routing: '/routing',
  oauthExcluded: '/routing/oauth-excluded',
  oauthModelAlias: '/routing/oauth-model-alias',
  config: '/config',
  plugins: '/plugins',
  pluginStore: '/plugin-store',
  backups: '/backups',
  access: '/access',
  settings: '/settings',
  system: '/system',
} as const;

/**
 * Pages that moved, and where they went. Accounts replaced Quota, Auth Files, and OAuth Login;
 * the provider model rule editors moved to Routing. Redirects keep the query and hash.
 */
export const LEGACY_REDIRECTS: Readonly<Record<string, string>> = {
  '/quota': ROUTES.accounts,
  '/auth-files': ROUTES.accounts,
  '/oauth': ROUTES.accounts,
  '/auth-files/oauth-excluded': ROUTES.oauthExcluded,
  '/auth-files/oauth-model-alias': ROUTES.oauthModelAlias,
};

/** A moved page's new address, with the old query and hash carried over. */
export function legacyRedirectTarget(to: string, location: { search: string; hash: string }) {
  return `${to}${location.search}${location.hash}`;
}
