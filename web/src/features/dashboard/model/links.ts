import { ROUTES } from '@/router/routes';
import { writeQuery, DEFAULT_QUERY, type RangePreset } from '@/features/taproot-logs/model/query';
import { writeUsageParams } from '@/features/taproot-usage/hooks/useUsageParams';
import type { DashboardWindow } from './window';

const withQuery = (path: string, params: URLSearchParams) => {
  const search = params.toString();
  return search ? `${path}?${search}` : path;
};

/** Request Logs filtered to errors over `range`. */
export function failedLogsLink(range: RangePreset): string {
  return withQuery(
    ROUTES.requestLogs,
    writeQuery(new URLSearchParams(), { ...DEFAULT_QUERY, range, status: 'error' })
  );
}

/** One request's log in Request Logs. */
export function requestLogLink(requestId: string): string {
  return withQuery(ROUTES.requestLogs, new URLSearchParams({ requestId }));
}

/** Request Logs' application log tab. */
export const appLogLink = (): string =>
  withQuery(ROUTES.requestLogs, new URLSearchParams({ tab: 'app' }));

/** Usage has no 1-hour range, so the hour opens its 24 hours. */
const usageRange = (window: DashboardWindow) => (window === '1h' ? '24h' : window);

export function usageDevicesLink(window: DashboardWindow): string {
  return withQuery(
    ROUTES.usage,
    writeUsageParams(new URLSearchParams(), { tab: 'devices', range: usageRange(window) })
  );
}

export function usageModelsLink(window: DashboardWindow): string {
  return withQuery(
    ROUTES.usage,
    writeUsageParams(new URLSearchParams(), { range: usageRange(window), group: 'model' })
  );
}

/** Accounts on one provider's tab. */
export function accountsLink(provider?: string): string {
  return provider ? withQuery(ROUTES.accounts, new URLSearchParams({ provider })) : ROUTES.accounts;
}

/** Accounts, with the provider's sign-in started as Add account starts it. */
export function signInLink(provider: string): string {
  return withQuery(ROUTES.accounts, new URLSearchParams({ signIn: provider }));
}

/** Access, scrolled to the device's request and focused on it. */
export function accessLink(nodeId?: string): string {
  return nodeId ? withQuery(ROUTES.access, new URLSearchParams({ focus: nodeId })) : ROUTES.access;
}

/** Settings, scrolled to one section. */
export function settingsLink(section: string): string {
  return withQuery(ROUTES.settings, new URLSearchParams({ section }));
}
