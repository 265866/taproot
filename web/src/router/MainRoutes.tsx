import { Navigate, useLocation, useRoutes, type Location } from 'react-router-dom';
import { DashboardPage } from '@/features/dashboard/DashboardPage';
import { AccountsPage } from '@/features/accounts/AccountsPage';
import { AuthFilesOAuthExcludedEditPage } from '@/pages/AuthFilesOAuthExcludedEditPage';
import { AuthFilesOAuthModelAliasEditPage } from '@/pages/AuthFilesOAuthModelAliasEditPage';
import { RoutingPage } from '@/features/routing/RoutingPage';
import { PluginResourcePage } from '@/features/plugins/PluginResourcePage';
import { PluginsPage } from '@/features/plugins/PluginsPage';
import { PluginStorePage } from '@/features/plugins/PluginStorePage';
import { ConfigPage } from '@/features/config/ConfigPage';
import { SystemPage } from '@/pages/SystemPage';
import { TaprootUsagePage } from '@/features/taproot-usage/TaprootUsagePage';
import { TaprootLogsPage } from '@/features/taproot-logs/TaprootLogsPage';
import { TaprootBackupsPage } from '@/features/taproot-backups/TaprootBackupsPage';
import { TaprootSettingsPage } from '@/features/taproot-settings/TaprootSettingsPage';
import { useAuthStore } from '@/stores';
import { LEGACY_REDIRECTS, ROUTES, legacyRedirectTarget } from './routes';

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
  { path: ROUTES.settings, element: <TaprootSettingsPage /> },
  { path: ROUTES.system, element: <SystemPage /> },
  { path: '/logs', element: <Navigate to={`${ROUTES.requestLogs}?tab=app`} replace /> },
  { path: '*', element: <Navigate to={ROUTES.dashboard} replace /> },
];

export function MainRoutes({ location }: { location?: Location }) {
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  return useRoutes(createMainRoutes(supportsPlugin), location);
}
