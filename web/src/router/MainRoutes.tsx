import { Navigate, useRoutes, type Location } from 'react-router-dom';
import { DashboardPage } from '@/features/dashboard/DashboardPage';
import { AuthFilesPage } from '@/features/authFiles/AuthFilesPage';
import { AuthFilesOAuthExcludedEditPage } from '@/pages/AuthFilesOAuthExcludedEditPage';
import { AuthFilesOAuthModelAliasEditPage } from '@/pages/AuthFilesOAuthModelAliasEditPage';
import { OAuthPage } from '@/features/oauth/OAuthPage';
import { RoutingPage } from '@/features/routing/RoutingPage';
import { PluginResourcePage } from '@/features/plugins/PluginResourcePage';
import { PluginsPage } from '@/features/plugins/PluginsPage';
import { PluginStorePage } from '@/features/plugins/PluginStorePage';
import { ConfigPage } from '@/features/config/ConfigPage';
import { LogsPage } from '@/features/logs/LogsPage';
import { SystemPage } from '@/pages/SystemPage';
import { TaprootQuotaPage } from '@/features/taproot-quota/TaprootQuotaPage';
import { TaprootUsagePage } from '@/features/taproot-usage/TaprootUsagePage';
import { TaprootLogsPage } from '@/features/taproot-logs/TaprootLogsPage';
import { TaprootBackupsPage } from '@/features/taproot-backups/TaprootBackupsPage';
import { TaprootSettingsPage } from '@/features/taproot-settings/TaprootSettingsPage';
import { useAuthStore } from '@/stores';
import { ROUTES } from './routes';

const createMainRoutes = (supportsPlugin: boolean) => [
  { path: ROUTES.dashboard, element: <DashboardPage /> },
  { path: '/dashboard', element: <Navigate to={ROUTES.dashboard} replace /> },
  { path: ROUTES.quota, element: <TaprootQuotaPage /> },
  { path: ROUTES.usage, element: <TaprootUsagePage /> },
  { path: ROUTES.requestLogs, element: <TaprootLogsPage /> },
  { path: ROUTES.authFiles, element: <AuthFilesPage /> },
  { path: '/auth-files/oauth-excluded', element: <AuthFilesOAuthExcludedEditPage /> },
  { path: '/auth-files/oauth-model-alias', element: <AuthFilesOAuthModelAliasEditPage /> },
  { path: ROUTES.oauth, element: <OAuthPage /> },
  { path: ROUTES.routing, element: <RoutingPage /> },
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
  // Upstream pages a later unit replaces; reachable by URL and from their placeholders.
  { path: ROUTES.legacyLogs, element: <LogsPage /> },
  { path: '*', element: <Navigate to={ROUTES.dashboard} replace /> },
];

export function MainRoutes({ location }: { location?: Location }) {
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  return useRoutes(createMainRoutes(supportsPlugin), location);
}
