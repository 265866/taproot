import { afterEach, describe, expect, jest, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { compileString } from 'sass';
import '@/i18n';
import type { AccessDevices } from '@/services/api/taproot';
import {
  normalizePools,
  normalizeRequestSummary,
  normalizeStatus,
  type ClientRequest,
} from '@/services/api/taprootStatus';
import {
  initialReads,
  readDashboard,
  type DashboardApi,
  type DashboardReads,
} from '@/features/dashboard/hooks/useDashboardData';
import { Verdict } from '@/features/dashboard/components/Verdict';
import { POLL_INTERVAL_MS, pollWhileVisible } from '@/utils/pollWhileVisible';

const NOW = Date.parse('2026-11-10T12:34:56Z');
const SUMMARY = normalizeRequestSummary({
  totals: { requests: 451, failed: 5, failureRate: 5 / 451, retries: 37, costUSD: 20.91 },
  buckets: [],
});
const POOLS = normalizePools({ pools: [{ provider: 'claude', totalAccounts: 3 }] });
const NO_ACCOUNTS = normalizePools({ pools: [] });
const STATUS = normalizeStatus({ state: 'normal', headline: 'Serving normally' });
const FAILURE: ClientRequest = {
  ts: '2026-11-10T12:00:00Z',
  deviceId: 'local',
  deviceName: 'alice-laptop',
  path: '/v1/messages',
  model: 'claude-opus-5-5',
  provider: 'claude',
  status: 529,
  failed: true,
  errorCode: 'overloaded',
  errorMessage: 'Overloaded',
  attempts: 3,
  latencyMs: 1200,
  ttftMs: null,
  requestId: 'req-1',
};

interface Calls {
  summary: Array<{ bucket: string; tz?: string; from: string; to: string }>;
  recent: Array<{ failed?: boolean; limit?: number }>;
  access: number;
}

const fakeApi = (over: Partial<DashboardApi> = {}): { api: DashboardApi; calls: Calls } => {
  const calls: Calls = { summary: [], recent: [], access: 0 };
  const api: DashboardApi = {
    status: async () => STATUS,
    pools: async () => POOLS,
    requestsSummary: async (q) => {
      calls.summary.push(q);
      return SUMMARY;
    },
    requestsRecent: async (q = {}) => {
      calls.recent.push(q);
      return [FAILURE];
    },
    accessDevices: async () => {
      calls.access += 1;
      return { enabled: true, devices: [] } as unknown as AccessDevices;
    },
    ...over,
  };
  return { api, calls };
};

const load = async (
  api: DashboardApi,
  reads: DashboardReads = initialReads,
  window = '24h' as const
) => {
  const { error, apply } = await readDashboard(api, window, NOW, reads);
  return { error, reads: apply(reads) };
};

describe('one load', () => {
  test('reads the verdict, pools, window summary, and the last five failures', async () => {
    const { api, calls } = fakeApi();
    const { error, reads } = await load(api);
    expect(error).toBeNull();
    expect(reads.status.data).toBe(STATUS);
    expect(reads.summary.data?.summary).toBe(SUMMARY);
    expect(reads.summary.data?.range).toMatchObject({ window: '24h', bucket: '1h', toMs: NOW });
    expect(reads.failures.data).toEqual([FAILURE]);
    expect(reads.updatedAt).toBe(NOW);
    expect(calls.summary[0]).toMatchObject({
      bucket: '1h',
      tz: undefined,
      to: new Date(NOW).toISOString(),
    });
    expect(calls.recent).toContainEqual({ failed: true, limit: 5 });
  });

  test('daily buckets start at the browser zone midnight', async () => {
    const { api, calls } = fakeApi();
    await load(api, initialReads, '30d' as '24h');
    expect(calls.summary[0].bucket).toBe('1d');
    expect(calls.summary[0].tz).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);
  });

  test('a failed read keeps the last good data, reports its error, and keeps the old stamp', async () => {
    const first = await load(fakeApi().api);
    const { api } = fakeApi({
      status: async () => {
        throw new Error('status down');
      },
      requestsRecent: async (q = {}) => {
        if (q.failed) throw new Error('recent down');
        return [];
      },
    });
    const { error, reads } = await readDashboard(api, '24h', NOW + 30_000, first.reads).then(
      ({ error: e, apply }) => ({ error: e, reads: apply(first.reads) })
    );
    expect(error).toBe('status down');
    expect(reads.status).toEqual({ data: STATUS, error: 'status down' });
    expect(reads.failures).toEqual({ data: [FAILURE], error: 'recent down' });
    expect(reads.summary.error).toBeNull();
    expect(reads.updatedAt).toBe(NOW);
  });

  test("a failed summary for a new window drops the old window's numbers", async () => {
    const first = await load(fakeApi().api);
    const { api } = fakeApi({
      requestsSummary: async () => {
        throw new Error('slow disk');
      },
    });
    const sameWindow = (await readDashboard(api, '24h', NOW, first.reads)).apply(first.reads);
    expect(sameWindow.summary).toMatchObject({ error: 'slow disk' });
    expect(sameWindow.summary.data?.summary).toBe(SUMMARY);
    const newWindow = (await readDashboard(api, '7d', NOW, first.reads)).apply(first.reads);
    expect(newWindow.summary).toEqual({ data: null, error: 'slow disk' });
  });

  test('reads Access only while the setup checklist shows', async () => {
    const served = fakeApi();
    await load(served.api);
    expect(served.calls.access).toBe(0);

    const fresh = fakeApi({ pools: async () => NO_ACCOUNTS, requestsRecent: async () => [] });
    const { reads } = await load(fresh.api);
    expect(fresh.calls.access).toBe(1);
    expect(reads.anyRequest.data).toBe(false);
    expect(reads.access.data?.enabled).toBe(true);
  });

  test('once a request has arrived, it stops asking whether one has', async () => {
    const { api, calls } = fakeApi();
    const first = await load(api);
    expect(first.reads.anyRequest.data).toBe(true);
    calls.recent.length = 0;
    await load(api, first.reads);
    expect(calls.recent).toEqual([{ failed: true, limit: 5 }]);
  });
});

describe('polling', () => {
  const fakeDocument = () => {
    const doc = Object.assign(new EventTarget(), { visibilityState: 'visible' });
    const setVisible = (visible: boolean) => {
      doc.visibilityState = visible ? 'visible' : 'hidden';
      doc.dispatchEvent(new Event('visibilitychange'));
    };
    return { doc, setVisible };
  };
  const original = globalThis.document;
  afterEach(() => {
    jest.useRealTimers();
    globalThis.document = original;
  });

  test('every 30 s, and a slow read is never overlapped by the next tick', async () => {
    jest.useFakeTimers();
    globalThis.document = fakeDocument().doc as unknown as Document;
    let calls = 0;
    let finish = () => {};
    const stop = pollWhileVisible(() => {
      calls += 1;
      return new Promise<void>((resolve) => (finish = resolve));
    });
    expect(POLL_INTERVAL_MS).toBe(30_000);
    expect(calls).toBe(1);
    jest.advanceTimersByTime(POLL_INTERVAL_MS * 3);
    expect(calls).toBe(1);
    finish();
    await Promise.resolve();
    await Promise.resolve();
    jest.advanceTimersByTime(POLL_INTERVAL_MS - 1);
    expect(calls).toBe(1);
    jest.advanceTimersByTime(1);
    expect(calls).toBe(2);
    stop();
  });

  test('a hidden tab pauses, and reads at once when it returns', async () => {
    jest.useFakeTimers();
    const { doc, setVisible } = fakeDocument();
    globalThis.document = doc as unknown as Document;
    let calls = 0;
    const stop = pollWhileVisible(async () => {
      calls += 1;
    });
    await Promise.resolve();
    setVisible(false);
    jest.advanceTimersByTime(POLL_INTERVAL_MS * 4);
    await Promise.resolve();
    expect(calls).toBe(1);
    setVisible(true);
    expect(calls).toBe(2);
    stop();
    jest.advanceTimersByTime(POLL_INTERVAL_MS * 2);
    expect(calls).toBe(2);
  });
});

describe('the verdict', () => {
  const render = (status: Parameters<typeof Verdict>[0]['status']) =>
    renderToStaticMarkup(
      createElement(MemoryRouter, null, createElement(Verdict, { status, onRetry: () => {} }))
    );

  test('degraded and down link to the last 15 minutes of failures; normal does not', () => {
    const degraded = normalizeStatus({ state: 'degraded', headline: 'Degraded: 6% failing' });
    expect(render({ data: degraded, error: null })).toContain(
      'href="/request-logs?range=15m&amp;status=error"'
    );
    expect(render({ data: normalizeStatus({ state: 'down' }), error: null })).toContain(
      'See failures'
    );
    expect(render({ data: STATUS, error: null })).not.toContain('See failures');
  });

  test('an error keeps the last verdict, marked stale, with Retry', () => {
    const html = render({ data: STATUS, error: 'boom' });
    expect(html).toContain('Serving normally');
    expect(html).toContain('Stale');
    expect(html).toContain('Retry');
    expect(html).toContain('role="alert"');
  });

  test('masks an email in the headline and detail', () => {
    const html = render({
      data: normalizeStatus({
        state: 'degraded',
        headline: 'Degraded',
        detail: 'alice@example.com is signed out',
      }),
      error: null,
    });
    expect(html).not.toContain('alice@example.com');
  });
});

describe('the verdict sits at the top of the rail below the rail breakpoint, and of the page above it', () => {
  const SRC = new URL('../src/', import.meta.url);
  const file = 'features/dashboard/DashboardPage.module.scss';
  const css = compileString(
    `@use "@/styles/variables.scss" as *;\n${readFileSync(new URL(file, SRC), 'utf8')}`,
    {
      url: new URL(file, SRC),
      importers: [
        { findFileUrl: (url) => (url.startsWith('@/') ? new URL(url.slice(2), SRC) : null) },
      ],
      logger: { warn: () => undefined },
    }
  ).css;
  const QUERY = /@media \(min-width: 1192px\) \{/g;
  const insideQuery = (at: number) =>
    [...css.matchAll(QUERY)].some((match) => {
      let depth = 0;
      for (let i = match.index; i < css.length; i += 1) {
        if (css[i] === '{') depth += 1;
        if (css[i] === '}' && --depth === 0) return match.index < at && at < i;
      }
      return false;
    });
  const rules = (selector: string) =>
    [...css.matchAll(new RegExp(`${selector.replace(/\./g, '\\.')} \\{([^}]*)\\}`, 'g'))].map(
      (m) => ({ body: m[1], wide: insideQuery(m.index) })
    );

  test('narrow: the rail copy shows and the page copy is display:none', () => {
    const page = rules('.page .verdictInPage').filter((r) => !r.wide);
    expect(page.map((r) => r.body.trim())).toEqual(['display: none;']);
    expect(
      rules('.rail .verdictInRail')
        .filter((r) => !r.wide)
        .some((r) => r.body.includes('display'))
    ).toBe(false);
  });

  test('wide: the two swap, only inside the min-width query', () => {
    expect(
      rules('.page .verdictInPage')
        .filter((r) => r.wide)
        .map((r) => r.body.trim())
    ).toEqual(['display: flex;']);
    expect(
      rules('.rail .verdictInRail')
        .filter((r) => r.wide)
        .map((r) => r.body.trim())
    ).toEqual(['display: none;']);
    // A fractional zoomed width falls between `max-width: 1191px` and `min-width: 1192px`.
    expect(css).not.toMatch(/max-width: 1191/);
  });

  test('the page renders both copies from one status read', () => {
    const page = readFileSync(new URL('features/dashboard/DashboardPage.tsx', SRC), 'utf8');
    expect(page).toContain('verdict(styles.verdictInRail)');
    expect(page).toContain('verdict(styles.verdictInPage)');
    expect(page.match(/<Verdict /g)).toHaveLength(1);
  });
});
