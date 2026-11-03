import { ROUTES } from '@/router/routes';

/** Request Logs (B3) opens a record from `#/request-logs?requestId=<id>`. */
export function requestLogHref(requestId: string): { pathname: string; search: string } {
  return { pathname: ROUTES.requestLogs, search: `?requestId=${encodeURIComponent(requestId)}` };
}
