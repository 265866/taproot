/**
 * Typed client for taproot's own API at same-origin `/taproot/api/v1`.
 *
 * The contract is the "Taproot API contract" table in internal/taproot/DESIGN.md. Times are
 * RFC 3339 UTC strings; errors arrive as `{ error: { code, message } }`. The server admits callers
 * by Tailscale identity, so requests carry no credentials of their own.
 */

export const TAPROOT_API_BASE = '/taproot/api/v1';

/**
 * Sent on every dashboard API request. Taproot's guard refuses state-changing GETs (OAuth starts,
 * the usage queue) without it, because another site can make a browser send a plain GET but not
 * a custom header.
 */
export const TAPROOT_REQUEST_HEADERS = { 'X-Taproot-Request': '1' } as const;

export type TaprootVia = 'loopback' | 'tailnet' | 'cidr';
export type UsageBucket = '5m' | '1h' | '1d';
export type UsageGroupBy = 'model' | 'account' | 'provider' | 'session' | 'device';
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
  /** The node's stable ID, which access.devices grants permissions to. */
  nodeId?: string;
  via: TaprootVia;
  proxy?: boolean;
  admin?: boolean;
}

/** The answer to a successful claim: the calling device and its new grant. */
export interface ClaimResult {
  claimed: true;
  device: DeviceGrant;
}

/** One access.devices entry: a tailnet device's permissions, by stable ID. */
export interface DeviceGrant {
  id: string;
  /** The Tailscale name; taproot keeps it in step with the tailnet, and it grants nothing. */
  name: string;
  /** The owner's own name for the device; taproot never changes it. */
  label: string;
  proxy: boolean;
  admin: boolean;
}

/** One access.addresses entry: an address outside Tailscale that may use the proxy. */
export interface AddressGrant {
  cidr: string;
  note: string;
}

/**
 * A device on the Access page: tailscaled's view joined with its grant. `name` is empty when
 * Tailscale hides it, as it does for the devices of a person the server is shared with.
 */
export interface AccessDevice extends DeviceGrant {
  /** The MagicDNS name; empty for a device no longer on the tailnet. */
  dnsName: string;
  /** The owner's login; empty for a tagged device and one no longer on the tailnet. */
  login: string;
  /** The owner's display name, or empty. */
  ownerName: string;
  tags: string[];
  os: string;
  /** When the device joined the tailnet, or null when unknown. */
  created: string | null;
  /** Outside the server's own tailnet: shared with or shared in. */
  outside: boolean;
  online: boolean;
  /** When the device was last connected, or null when unknown or online. */
  lastSeen: string | null;
  /**
   * The device's Tailscale IPs, IPv4 first; empty for a device no longer on the tailnet. Tailscale
   * shows a sharee's device's IPs to its owner too, so they identify a device whose name it hides.
   */
  addresses: string[];
  /** The taproot server's own node: always Proxy, never Admin. */
  server: boolean;
  /** False for a granted device the last tailnet read did not list. */
  onTailnet: boolean;
  /** When successful reads stopped listing the device. */
  missingSince: string | null;
}

/** An open access request: a refused device, or one that asked from its browser. */
export interface AccessRequest {
  nodeId: string;
  login: string;
  ownerName: string;
  /** The name the device gave itself, else its Tailscale name, else empty. */
  name: string;
  /** The program that asked, such as `Claude Code`, or empty. */
  client: string;
  firstSeen: string;
  lastSeen: string;
  count: number;
  outside: boolean;
  /** What the device was refused: `admin` when it opened the dashboard, else `proxy`. */
  asked: 'proxy' | 'admin';
}

export interface AccessDevices {
  /** False when access.enabled is false; the lists then have no effect. */
  enabled: boolean;
  /**
   * Identifies the saved device and address lists. A save of whole lists sends it back, and the
   * server refuses it (409 `access_changed`) when the lists changed since.
   */
  version: string;
  devices: AccessDevice[];
  /** Open requests, newest first. */
  requests: AccessRequest[];
  addresses: AddressGrant[];
  /** Why tailscaled could not be read; devices then lists only those with a permission. */
  tailnetError?: string;
  /** Taproot forgets a device this long after it leaves the tailnet. */
  forgetAfterHours: number;
}

export interface TaprootAccount {
  authIndex: string;
  provider: string;
  label: string;
  email: string;
  plan: string;
  disabled: boolean;
  /**
   * True when the credential left the server. Removed accounts stay listed so history keeps its
   * labels, but they belong to no pool. Servers older than this field omit it.
   */
  removed?: boolean;
  removedAt?: string | null;
  firstSeen: string | null;
  lastSeen: string | null;
}

export interface QuotaWindowState {
  window: QuotaWindowId;
  /**
   * True for windows that limit every request of the account. Servers older than this field
   * omit it; the quota model then decides by window name.
   */
  accountWide?: boolean;
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
  accountWide?: boolean;
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
  /** Percentiles come from log-bucketed histograms (within about 5%); null without samples. */
  latencyP50Ms: number | null;
  latencyP95Ms: number | null;
  ttftP50Ms: number | null;
  ttftP95Ms: number | null;
  /** Models that used tokens but match no price; costUSD leaves them out. Absent when none. */
  unpricedModels?: string[];
}

export interface UsageSummary {
  totals: UsageTotals;
  buckets: { t: string; groups: Record<string, UsageTotals> }[];
}

/** One row of /usage/events, as internal/taproot/ingest/usage_api.go writes it. */
export interface UsageEvent {
  id: number;
  eventKey: string;
  t: string;
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
  ttftMs: number | null;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheWrite5mTokens: number;
  cacheWrite1hTokens: number;
  serviceTier: string;
  costUSD: number;
  /** False when no price pattern matches the model, so costUSD is 0. */
  priced: boolean;
  source: string;
  /** Empty for requests recorded before device attribution, or with no known device. */
  deviceId?: string;
  deviceName?: string;
  deviceLogin?: string;
}

/** One row of `GET /usage/devices`; internal/taproot/ingest/devices.go owns the shape. */
export interface UsageDevice {
  /** `node:<StableID>`, `local`, `cidr:<address>`, `plugin:<plugin ID>`, `plugins`, or empty for requests without a device. */
  deviceId: string;
  name: string;
  login: string;
  /** `plugin` for a plugin's in-process requests, which no HTTP caller made. */
  via: TaprootVia | 'plugin' | '';
  lastSeen: string;
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
  /** Cache read ÷ (cache read + cache write + input); null without tokens. */
  cacheHitRatio: number | null;
  /** Claude main-conversation requests only; subagents (with a parent session) are left out. */
  mainConversation: {
    requests: number;
    /** Requests whose cache writes carried a 5m/1h split. */
    splitRequests: number;
    cacheWriteTokens: number;
    cacheWrite5mTokens: number;
    cacheWrite1hTokens: number;
    /** 5m ÷ (5m + 1h) cache-write tokens; null without split-known writes. */
    fiveMinuteShare: number | null;
    /**
     * An estimate, not a measurement: 5m writes after a 5 to 60 minute pause in the same
     * session count as re-writes a one-hour TTL would have avoided.
     */
    estimate: {
      rewriteRequests: number;
      rewriteTokens: number;
      rewriteCostUSD: number;
      oneHourPremiumUSD: number;
      netSavingsUSD: number;
    };
  };
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
  /** The session has more requests in range than the server reads; the fields cover the first. */
  truncated: boolean;
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

/** One row of `GET /request-logs`; reqlog's `LogMeta` owns the shape. */
export interface RequestLogMeta {
  requestId: string;
  traceId?: string;
  sessionId?: string;
  parentSessionId?: string;
  timestamp: string;
  durationMs: number;
  method: string;
  url: string;
  path: string;
  status: number;
  model?: string;
  authIndex?: string;
  streaming: boolean;
  /** Recorded only because it failed while full request logging was off. */
  errorOnly: boolean;
  /** Why parts of the record were not stored, for example a body over the memory budget. */
  dropped?: string;
  requestBytes: number;
  responseBytes: number;
  recordBytes: number;
}

/** Header values arrive as lists because a header can repeat. */
export type HeaderMap = Record<string, string[]>;

export interface RequestLogDetail {
  meta: RequestLogMeta;
  request: { method: string; url: string; headers: HeaderMap; body: string };
  response: { status: number; headers: HeaderMap; body: string };
  /** Executor-formatted sections, verbatim; each can hold several attempts. */
  upstream: {
    request?: string;
    response?: string;
    errors?: { status: number; message: string }[];
    websocketTimeline?: string;
    apiWebsocketTimeline?: string;
    /** Sizes of `request` and `response`, which `requestLog` defers to `requestLogUpstream`. */
    requestBytes?: number;
    responseBytes?: number;
  };
  /** With `blobs: 'ref'`, the payloads behind `[taproot-blob:<hash>:<bytes>]` placeholders. */
  blobs?: Record<string, RequestLogBlob>;
}

/** A base64 payload a detail read with `blobs: 'ref'` left out. */
export interface RequestLogBlob {
  /** Decoded size. */
  bytes: number;
  /** Set for JPEG, PNG, GIF, and WebP images. */
  type?: 'image/jpeg' | 'image/png' | 'image/gif' | 'image/webp';
}

/** `ref` replaces each stored base64 payload with a placeholder; `inline` keeps the original. */
export type RequestLogBlobs = 'ref' | 'inline';

/** The upstream sections `requestLog` defers. */
export interface RequestLogUpstream {
  request: string;
  response: string;
  blobs?: Record<string, RequestLogBlob>;
}

export interface RequestLogStats {
  bytesOnDisk: number;
  rawBytes: number;
  oldest: string | null;
  newest: string | null;
  count: number;
  segments: number;
  openSegments: number;
  queueBytes: number;
  droppedBodies: number;
  droppedRecords: number;
  truncatedResponses: number;
  writeErrors: number;
}

export type BackupReason = 'scheduled' | 'manual' | 'pre-restore';

export interface Backup {
  id: string;
  createdAt: string;
  bytes: number;
  reason: BackupReason;
  /** Number of files in the archive's manifest. */
  files: number;
}

export interface RestoreResult {
  restored: true;
  preRestoreBackupId: string;
  /** `scheduled`: taproot exits for its supervisor to restart it; `manual`: restart it yourself. */
  restart: 'scheduled' | 'manual';
  message: string;
}

export interface TaprootSettings {
  /** Devices listed have at least one permission; every other device has none. */
  access: { enabled: boolean; version: string; devices: DeviceGrant[]; addresses: AddressGrant[] };
  requestLogs: {
    /** Negative disables the age limit. */
    maxDays: number;
    /** Negative disables the size limit. */
    maxBytes: number;
    excludePaths: string[];
    keepUnrouted404: boolean;
  };
  backups: {
    /** `HH:MM` in the server's zone, or `off`. */
    dailyAt: string;
    keep: number;
    /** RFC 3339 with the server's offset; absent while the schedule is off. */
    nextRunAt?: string;
  };
  ingest: { pollInterval: string; activePollInterval: string; restartRequired: boolean };
  /** Absent when the server has no alerts module. The webhook itself never leaves the server. */
  alerts?: {
    /** The taproot.yaml switch; alerts only go out when it is on and a webhook is set. */
    enabled: boolean;
    webhookSet: boolean;
    /** The webhook's last six characters, such as `…a1B2c3`. */
    webhookHint: string;
    /** taproot.yaml holds a URL that is not a Discord webhook, so alerts are off until it is replaced or removed. */
    webhookInvalid: boolean;
    active: boolean;
  };
  info: TaprootServerInfo;
}

export interface TaprootServerInfo {
  configPath: string;
  dataDir: string;
  version: string;
  commit: string;
  buildDate: string;
  /** The server zone's IANA name, or `''` when the server cannot name it. */
  timeZone: string;
  /** The server zone's current offset from UTC. */
  utcOffsetSeconds: number;
  zoneAbbreviation: string;
}

/** A `PUT /settings` body: absent sections and fields stay as they are. */
export interface TaprootSettingsUpdate {
  /**
   * Each list replaces the whole list. `device` instead sets one device's entry and leaves the
   * others as saved. With `version`, the server refuses the save when the lists changed since.
   */
  access?: {
    version?: string;
    devices?: DeviceGrant[];
    device?: DeviceGrant;
    addresses?: AddressGrant[];
  };
  requestLogs?: Partial<TaprootSettings['requestLogs']>;
  backups?: Partial<TaprootSettings['backups']>;
  ingest?: { pollInterval?: string };
  /** `discordWebhook: ''` clears the webhook. */
  alerts?: { discordWebhook?: string; enabled?: boolean };
}

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
  /** A device ID; `(none)` selects requests without a device. */
  deviceId?: string;
  failed?: boolean;
}

export interface RequestLogFilter extends Partial<TimeRange>, Page {
  q?: string;
  /** A status code, `Nxx`, or `error`. */
  status?: string;
  model?: string;
  authIndex?: string;
  sessionId?: string;
}

export class TaprootApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** Seconds to wait before retrying, from a 429's body or `Retry-After` header. */
  readonly retryAfter?: number;

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message);
    this.name = 'TaprootApiError';
    this.status = status;
    this.code = code;
    if (retryAfter !== undefined) this.retryAfter = retryAfter;
  }
}

const seconds = (value: unknown): number | undefined => {
  const n = typeof value === 'string' && value.trim() !== '' ? Number(value) : value;
  return typeof n === 'number' && Number.isFinite(n) && n >= 0 ? Math.ceil(n) : undefined;
};

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
  let retryAfter = seconds(response.headers.get('Retry-After'));
  try {
    const body: unknown = await response.json();
    const error = (
      body as { error?: { code?: unknown; message?: unknown; retryAfter?: unknown } } | null
    )?.error;
    if (typeof error?.code === 'string') code = error.code;
    if (typeof error?.message === 'string' && error.message) message = error.message;
    retryAfter = seconds(error?.retryAfter) ?? retryAfter;
  } catch {
    // Not JSON (for example a proxy error page); keep the status line.
  }
  return new TaprootApiError(response.status, code, message, retryAfter);
}

const record = (value: unknown): Record<string, unknown> =>
  typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {};
const text = (value: unknown) => (typeof value === 'string' ? value : '');
const flag = (value: unknown) => value === true;
const timeOrNull = (value: unknown) => (typeof value === 'string' && value ? value : null);
const strings = (value: unknown) =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string' && v !== '') : [];

function normalizeAccessDevice(raw: unknown): AccessDevice {
  const d = record(raw);
  return {
    id: text(d.id),
    name: text(d.name),
    label: text(d.label),
    dnsName: text(d.dnsName),
    login: text(d.login),
    ownerName: text(d.ownerName),
    tags: Array.isArray(d.tags) ? d.tags.filter((t): t is string => typeof t === 'string') : [],
    os: text(d.os),
    created: timeOrNull(d.created),
    outside: flag(d.outside),
    online: flag(d.online),
    lastSeen: timeOrNull(d.lastSeen),
    addresses: strings(d.addresses),
    server: flag(d.server),
    onTailnet: flag(d.onTailnet),
    missingSince: timeOrNull(d.missingSince),
    proxy: flag(d.proxy),
    admin: flag(d.admin),
  };
}

function normalizeAccessRequest(raw: unknown): AccessRequest {
  const r = record(raw);
  return {
    nodeId: text(r.nodeId),
    login: text(r.login),
    ownerName: text(r.ownerName),
    name: text(r.name),
    client: text(r.client),
    firstSeen: text(r.firstSeen),
    lastSeen: text(r.lastSeen),
    count: typeof r.count === 'number' && Number.isFinite(r.count) ? r.count : 0,
    outside: flag(r.outside),
    asked: r.asked === 'admin' ? 'admin' : 'proxy',
  };
}

/** Fills fields an older server leaves out, so the page reads one shape. */
export function normalizeAccessDevices(raw: unknown): AccessDevices {
  const body = record(raw);
  const list = (value: unknown) => (Array.isArray(value) ? value : []);
  const result: AccessDevices = {
    enabled: flag(body.enabled),
    version: text(body.version),
    devices: list(body.devices).map(normalizeAccessDevice),
    requests: list(body.requests).map(normalizeAccessRequest),
    addresses: list(body.addresses).map((a) => ({
      cidr: text(record(a).cidr),
      note: text(record(a).note),
    })),
    forgetAfterHours: typeof body.forgetAfterHours === 'number' ? body.forgetAfterHours : 24,
  };
  if (typeof body.tailnetError === 'string' && body.tailnetError)
    result.tailnetError = body.tailnetError;
  return result;
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
          ? { Accept: 'application/json', ...TAPROOT_REQUEST_HEADERS }
          : {
              Accept: 'application/json',
              'Content-Type': 'application/json',
              ...TAPROOT_REQUEST_HEADERS,
            },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      credentials: 'same-origin',
    });
    if (!response.ok) throw await readError(response);
    return (await response.json()) as T;
  };

  const id = (value: string) => encodeURIComponent(value);

  return {
    me: () => request<TaprootMe>('GET', '/me'),
    /**
     * Sends the setup code from taproot's log. While no device has Admin, the calling tailnet
     * device gets Proxy and Admin; wrong codes are limited to 5 a minute for the server (429).
     */
    claim: (code: string) => request<ClaimResult>('POST', '/claim', { body: { code } }),
    accounts: () => request<{ accounts: TaprootAccount[] }>('GET', '/accounts'),

    quotaCurrent: () => request<{ accounts: AccountQuota[] }>('GET', '/quota/current'),
    quotaSeries: (range: TimeRange & { provider?: string }) =>
      request<{ series: QuotaSeries[] }>('GET', '/quota/series', { query: { ...range } }),
    /** Reads one account's usage endpoint now; at most once per 30 s per account (429). */
    quotaRefresh: (authIndex: string) =>
      request<AccountQuota>('POST', '/quota/refresh', { body: { authIndex } }),

    /** `tz` is an IANA zone; daily buckets then start at its midnight instead of UTC's. */
    usageSummary: (
      query: TimeRange & { bucket: UsageBucket; groupBy: UsageGroupBy; tz?: string }
    ) => request<UsageSummary>('GET', '/usage/summary', { query: { ...query } }),
    usageEvents: (filter: UsageEventFilter) =>
      request<{ events: UsageEvent[]; nextCursor: string | null }>('GET', '/usage/events', {
        query: { ...filter },
      }),
    usageDevices: (query: TimeRange) =>
      request<{ devices: UsageDevice[] }>('GET', '/usage/devices', { query: { ...query } }),
    usageSessions: (query: TimeRange & Page) =>
      request<{ sessions: UsageSession[]; nextCursor: string | null }>('GET', '/usage/sessions', {
        query: { ...query },
      }),

    prices: () => request<{ prices: Price[] }>('GET', '/prices'),
    putPrices: (prices: Price[]) =>
      request<{ prices: Price[] }>('PUT', '/prices', { body: { prices } }),
    /** Replaces every row with the prices taproot ships. */
    resetPrices: () => request<{ prices: Price[] }>('POST', '/prices/reset'),

    requestLogs: (filter: RequestLogFilter) =>
      request<{ logs: RequestLogMeta[]; nextCursor: string | null }>('GET', '/request-logs', {
        query: { ...filter },
      }),
    /** Leaves out the upstream request and response; `requestLogUpstream` returns them. */
    requestLog: (requestId: string, blobs: RequestLogBlobs) =>
      request<RequestLogDetail>('GET', `/request-logs/${id(requestId)}`, {
        query: { upstream: 'defer', blobs: blobs === 'ref' ? 'ref' : undefined },
      }),
    requestLogUpstream: (requestId: string, blobs: RequestLogBlobs) =>
      request<RequestLogUpstream>('GET', `/request-logs/${id(requestId)}/upstream`, {
        query: { blobs: blobs === 'ref' ? 'ref' : undefined },
      }),
    requestLogRawUrl: (requestId: string) =>
      buildTaprootUrl(base, `/request-logs/${id(requestId)}/raw`),
    requestLogBlobUrl: (hash: string) => buildTaprootUrl(base, `/request-logs/blobs/${id(hash)}`),
    requestLogStats: () => request<RequestLogStats>('GET', '/request-logs/stats'),

    backups: () => request<{ backups: Backup[] }>('GET', '/backups'),
    createBackup: () => request<Backup>('POST', '/backups'),
    backupDownloadUrl: (backupId: string) =>
      buildTaprootUrl(base, `/backups/${id(backupId)}/download`),
    restoreBackup: (backupId: string) =>
      request<RestoreResult>('POST', `/backups/${id(backupId)}/restore`),

    accessDevices: async () =>
      normalizeAccessDevices(await request<unknown>('GET', '/access/devices')),
    /**
     * Asks for access for the calling device, which may name itself. Grants nothing; the server
     * allows 10 a device an hour (429).
     */
    requestAccess: (name: string) =>
      request<{ status: 'pending' }>('POST', '/access/request', { body: { name } }),
    dismissRequest: (nodeId: string) =>
      request<{ dismissed: true }>('POST', `/access/requests/${id(nodeId)}/dismiss`),
    /** Closes a request after its grant was saved, so the device's later refusals stay quiet. */
    allowRequest: (nodeId: string) =>
      request<{ allowed: true }>('POST', `/access/requests/${id(nodeId)}/allow`),

    settings: () => request<TaprootSettings>('GET', '/settings'),
    putSettings: (update: TaprootSettingsUpdate) =>
      request<TaprootSettings>('PUT', '/settings', { body: update }),
    /** Posts a test message to the saved Discord webhook. */
    testAlert: () => request<{ sent: true }>('POST', '/alerts/test'),
  };
}

export type TaprootClient = ReturnType<typeof createTaprootClient>;

export const taprootApi: TaprootClient = createTaprootClient();
