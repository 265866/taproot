import { Suspense, lazy, type ComponentType } from 'react';
import { Navigate, useLocation, useRoutes, type Location } from 'react-router-dom';
import { useAuthStore } from '@/stores';
import { pageImports } from './pageImports';
import { LEGACY_REDIRECTS, ROUTES, legacyRedirectTarget } from './routes';

const RELOADED_FOR_CHUNK = 'taproot.reloadedForChunk';

function page<K extends string>(load: () => Promise<Record<K, ComponentType>>, name: K) {
  return lazy(async () => {
    try {
      const module = await load();
      sessionStorage.removeItem(RELOADED_FOR_CHUNK);
      return { default: module[name] };
    } catch (error) {
      // After an upgrade the server holds only the new build's chunks, so a tab still running the
      // old build cannot load a page it has not opened yet. Reload once to pick up the new build.
      if (sessionStorage.getItem(RELOADED_FOR_CHUNK) === null) {
        sessionStorage.setItem(RELOADED_FOR_CHUNK, '1');
        window.location.reload();
        return new Promise<never>(() => undefined);
      }
      throw error;
    }
  });
}

const DashboardPage = page(pageImports.dashboard, 'DashboardPage');
const AccountsPage = page(pageImports.accounts, 'AccountsPage');
const AuthFilesOAuthExcludedEditPage = page(
  pageImports.oauthExcluded,
  'AuthFilesOAuthExcludedEditPage'
);
const AuthFilesOAuthModelAliasEditPage = page(
  pageImports.oauthModelAlias,
  'AuthFilesOAuthModelAliasEditPage'
);
const RoutingPage = page(pageImports.routing, 'RoutingPage');
const PluginResourcePage = page(pageImports.pluginResource, 'PluginResourcePage');
const PluginsPage = page(pageImports.plugins, 'PluginsPage');
const PluginStorePage = page(pageImports.pluginStore, 'PluginStorePage');
const ConfigPage = page(pageImports.config, 'ConfigPage');
const SystemPage = page(pageImports.system, 'SystemPage');
const TaprootUsagePage = page(pageImports.usage, 'TaprootUsagePage');
const TaprootLogsPage = page(pageImports.requestLogs, 'TaprootLogsPage');
const TaprootBackupsPage = page(pageImports.backups, 'TaprootBackupsPage');
const TaprootAccessPage = page(pageImports.access, 'TaprootAccessPage');
const TaprootSettingsPage = page(pageImports.settings, 'TaprootSettingsPage');

/** Sends an old address to its new page. */
function LegacyRedirect({ to }: { to: string }) {
  const location = useLocation();
  return <Navigate to={legacyRedirectTarget(to, location)} replace />;
}

const createMainRoutes = (supportsPlugin: boolean) => [
  { path: ROUTES.dashboard, element: <DashboardPage /> },
  { path: '/dashboard', element: <Navigate to={ROUTES.dashboard} replace /> },
  { path: ROUTES.accounts, element: <AccountsPage /> },
  { path: ROUTES.usage, element: <TaprootUsagePage /> },
  { path: ROUTES.requestLogs, element: <TaprootLogsPage /> },
  { path: ROUTES.routing, element: <RoutingPage /> },
  { path: ROUTES.oauthExcluded, element: <AuthFilesOAuthExcludedEditPage /> },
  { path: ROUTES.oauthModelAlias, element: <AuthFilesOAuthModelAliasEditPage /> },
  ...Object.entries(LEGACY_REDIRECTS).map(([path, to]) => ({
    path,
    element: <LegacyRedirect to={to} />,
  })),
  { path: ROUTES.config, element: <ConfigPage /> },
  ...(supportsPlugin
    ? [
        { path: '/plugin-pages/:pluginId/:menuIndex', element: <PluginResourcePage /> },
        { path: ROUTES.plugins, element: <PluginsPage /> },
        { path: ROUTES.pluginStore, element: <PluginStorePage /> },
        { path: '/plugins/*', element: <Navigate to={ROUTES.plugins} replace /> },
      ]
    : [
        { path: '/plugin-pages/*', element: <Navigate to={ROUTES.dashboard} replace /> },
        { path: '/plugins/*', element: <Navigate to={ROUTES.dashboard} replace /> },
        { path: ROUTES.pluginStore, element: <Navigate to={ROUTES.dashboard} replace /> },
      ]),
  { path: ROUTES.backups, element: <TaprootBackupsPage /> },
  { path: ROUTES.access, element: <TaprootAccessPage /> },
  { path: ROUTES.settings, element: <TaprootSettingsPage /> },
  { path: ROUTES.system, element: <SystemPage /> },
  { path: '/logs', element: <Navigate to={`${ROUTES.requestLogs}?tab=app`} replace /> },
  { path: '*', element: <Navigate to={ROUTES.dashboard} replace /> },
];

export function MainRoutes({ location }: { location?: Location }) {
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  const element = useRoutes(createMainRoutes(supportsPlugin), location);
  return <Suspense fallback={null}>{element}</Suspense>;
}
