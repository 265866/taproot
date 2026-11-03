/**
 * Typed client for taproot's own API at same-origin `/taproot/api/v1`.
 *
 * The contract is the "Taproot API contract" table in internal/taproot/PLAN.md. Times are
 * RFC 3339 UTC strings; errors arrive as `{ error: { code, message } }`. The server admits callers
 * by Tailscale identity, so requests carry no credentials of their own.
 */

export const TAPROOT_API_BASE = '/taproot/api/v1';

export type TaprootVia = 'loopback' | 'tailnet' | 'cidr';
export type UsageBucket = '5m' | '1h' | '1d';
export type UsageGroupBy = 'model' | 'account' | 'provider' | 'session';
export type QuotaWindowId =
  | 'five_hour'
  | 'seven_day'
  | 'seven_day_opus'
  | 'codex_primary'
  | 'codex_secondary'
  | (string & {});

export interface TaprootMe {
  login: string;
  displayName: string;
  node: string;
  via: TaprootVia;
}

export interface TaprootAccount {
  authIndex: string;
  provider: string;
  label: string;
  email: string;
  plan: string;
  disabled: boolean;
  firstSeen: string;
  lastSeen: string;
}

export interface QuotaWindowState {
  window: QuotaWindowId;
  /** Fraction used, 0..1. */
  utilization: number;
  resetsAt: string | null;
  status: string;
  observedAt: string;
  periodSeconds: number;
}

export interface AccountQuota {
  authIndex: string;
  provider: string;
  windows: QuotaWindowState[];
}

export interface QuotaSeries {
  authIndex: string;
  provider: string;
  window: QuotaWindowId;
  periodSeconds: number;
  points: { t: string; utilization: number }[];
  resets: string[];
}

export interface UsageTotals {
  requests: number;
  failures: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheWrite5mTokens: number;
  cacheWrite1hTokens: number;
  costUSD: number;
  latencyP50Ms: number;
  latencyP95Ms: number;
  ttftP50Ms: number;
  ttftP95Ms: number;
}

export interface UsageSummary {
  totals: UsageTotals;
  buckets: { t: string; groups: Record<string, UsageTotals> }[];
}

// The plan lists these fields without fixing their JSON names; ingest (A1) owns the final shape.
export interface UsageEvent {
  at: string;
  requestId: string;
  traceId: string;
  sessionId: string;
  parentSessionId: string;
  provider: string;
  model: string;
  alias: string;
  authIndex: string;
  authId: string;
  stream: boolean;
  failed: boolean;
  statusCode: number;
  latencyMs: number;
  ttftMs: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheWrite5mTokens: number;
  cacheWrite1hTokens: number;
  serviceTier: string;
  costUSD: number;
}

export interface UsageSession {
  sessionId: string;
  firstAt: string;
  lastAt: string;
  requests: number;
  accounts: string[];
  models: string[];
  totals: UsageTotals;
  accountSwitches: number;
}

/** USD per million tokens. */
export interface Price {
  modelPattern: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite5m: number;
  cacheWrite1h: number;
  source: string;
  updatedAt: string;
}

export interface RequestLogMeta {
  requestId: string;
  traceId: string;
  sessionId: string;
  ts: string;
  method: string;
  path: string;
  status: number;
  model: string;
  authIndex: string;
  reqBytes: number;
  respBytes: number;
}

export interface RequestLogDetail {
  meta: RequestLogMeta;
  request: { headers: Record<string, string>; body: string };
  response: { status: number; headers: Record<string, string>; body: string };
  upstream: Record<string, unknown>;
}

export interface RequestLogStats {
  bytesOnDisk: number;
  rawBytes: number;
  oldest: string | null;
  newest: string | null;
  count: number;
}

export interface Backup {
  id: string;
  createdAt: string;
  bytes: number;
  reason: string;
  files: string[];
}

export interface RestoreResult {
  restored: true;
  preRestoreBackupId: string;
}

// The editable subset of taproot.yaml; access (A2), request logs (A3), and backups (A4) own the
// keys, so this stays an open record until those settle.
export type TaprootSettings = Record<string, unknown>;

export interface TimeRange {
  from: string;
  to: string;
}

export interface Page {
  cursor?: string;
  limit?: number;
}

export interface UsageEventFilter extends TimeRange, Page {
  authIndex?: string;
  model?: string;
  sessionId?: string;
  failed?: boolean;
}

export interface RequestLogFilter extends TimeRange, Page {
  q?: string;
  status?: number;
  model?: string;
  authIndex?: string;
  sessionId?: string;
}

export class TaprootApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'TaprootApiError';
    this.status = status;
    this.code = code;
  }
}

type QueryValue = string | number | boolean | undefined;

export function buildTaprootUrl(
  base: string,
  path: string,
  query?: Record<string, QueryValue>
): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const search = params.toString();
  return `${base.replace(/\/+$/, '')}${path}${search ? `?${search}` : ''}`;
}

async function readError(response: Response): Promise<TaprootApiError> {
  let code = 'http_error';
  let message = `${response.status} ${response.statusText}`.trim();
  try {
    const body: unknown = await response.json();
    const error = (body as { error?: { code?: unknown; message?: unknown } } | null)?.error;
    if (typeof error?.code === 'string') code = error.code;
    if (typeof error?.message === 'string' && error.message) message = error.message;
  } catch {
    // Not JSON (for example a proxy error page); keep the status line.
  }
  return new TaprootApiError(response.status, code, message);
}

export function createTaprootClient(base = TAPROOT_API_BASE, fetchImpl: typeof fetch = fetch) {
  const request = async <T>(
    method: 'GET' | 'POST' | 'PUT',
    path: string,
    options: { query?: Record<string, QueryValue>; body?: unknown } = {}
  ): Promise<T> => {
    const response = await fetchImpl(buildTaprootUrl(base, path, options.query), {
      method,
      headers:
        options.body === undefined
          ? { Accept: 'application/json' }
          : { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      credentials: 'same-origin',
    });
    if (!response.ok) throw await readError(response);
    return (await response.json()) as T;
  };

  const id = (value: string) => encodeURIComponent(value);

  return {
    me: () => request<TaprootMe>('GET', '/me'),
    accounts: () => request<{ accounts: TaprootAccount[] }>('GET', '/accounts'),

    quotaCurrent: () => request<{ accounts: AccountQuota[] }>('GET', '/quota/current'),
    quotaSeries: (range: TimeRange & { provider?: string }) =>
      request<{ series: QuotaSeries[] }>('GET', '/quota/series', { query: { ...range } }),

    usageSummary: (query: TimeRange & { bucket: UsageBucket; groupBy: UsageGroupBy }) =>
      request<UsageSummary>('GET', '/usage/summary', { query: { ...query } }),
    usageEvents: (filter: UsageEventFilter) =>
      request<{ events: UsageEvent[]; nextCursor: string | null }>('GET', '/usage/events', {
        query: { ...filter },
      }),
    usageSessions: (query: TimeRange & Page) =>
      request<{ sessions: UsageSession[]; nextCursor?: string | null }>('GET', '/usage/sessions', {
        query: { ...query },
      }),

    prices: () => request<{ prices: Price[] }>('GET', '/prices'),
    putPrices: (prices: Price[]) =>
      request<{ prices: Price[] }>('PUT', '/prices', { body: { prices } }),

    requestLogs: (filter: RequestLogFilter) =>
      request<{ logs: RequestLogMeta[]; nextCursor: string | null }>('GET', '/request-logs', {
        query: { ...filter },
      }),
    requestLog: (requestId: string) =>
      request<RequestLogDetail>('GET', `/request-logs/${id(requestId)}`),
    requestLogRawUrl: (requestId: string) =>
      buildTaprootUrl(base, `/request-logs/${id(requestId)}/raw`),
    requestLogStats: () => request<RequestLogStats>('GET', '/request-logs/stats'),

    backups: () => request<{ backups: Backup[] }>('GET', '/backups'),
    createBackup: () => request<Backup>('POST', '/backups'),
    backupDownloadUrl: (backupId: string) =>
      buildTaprootUrl(base, `/backups/${id(backupId)}/download`),
    restoreBackup: (backupId: string) =>
      request<RestoreResult>('POST', `/backups/${id(backupId)}/restore`),

    settings: () => request<TaprootSettings>('GET', '/settings'),
    putSettings: (settings: TaprootSettings) =>
      request<TaprootSettings>('PUT', '/settings', { body: settings }),
  };
}

export type TaprootClient = ReturnType<typeof createTaprootClient>;

export const taprootApi: TaprootClient = createTaprootClient();
