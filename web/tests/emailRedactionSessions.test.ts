import { afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n/index';
import { DEFAULT_QUERY, parseQuery, writeQuery } from '../src/features/taproot-logs/model/query';
import { RequestLogFilters } from '../src/features/taproot-logs/components/RequestLogFilters';
import {
  readUsageParams,
  writeUsageParams,
} from '../src/features/taproot-usage/hooks/useUsageParams';
import { RequestsView } from '../src/features/taproot-usage/components/RequestsView';
import { usePrivacyStore } from '../src/stores/usePrivacyStore';

const SESSION = 'user:report to zebrafinch.alpha@sl.example.run';

beforeAll(async () => {
  await i18n.changeLanguage('en');
});
afterEach(() => {
  usePrivacyStore.setState({ showEmails: false });
});

describe('session ids in the URL', () => {
  test('Request Logs never write the raw id, and read it back', () => {
    const params = writeQuery(new URLSearchParams(), { ...DEFAULT_QUERY, sessionId: SESSION });
    expect(params.toString()).not.toContain('zebrafinch');
    expect(decodeURIComponent(params.toString())).not.toContain('zebrafinch');
    expect(parseQuery(params).sessionId).toBe(SESSION);
  });

  test('Request Logs still accept old plain links', () => {
    expect(parseQuery(new URLSearchParams('sessionId=claude:s1')).sessionId).toBe('claude:s1');
  });

  test('Usage never writes the raw id, reads it back, and accepts old links', () => {
    const next = writeUsageParams(new URLSearchParams('tab=requests'), { session: SESSION });
    expect(decodeURIComponent(next.toString())).not.toContain('zebrafinch');
    expect(readUsageParams(next).session).toBe(SESSION);
    expect(readUsageParams(new URLSearchParams('session=sess-1')).session).toBe('sess-1');
    expect(writeUsageParams(next, { session: '' }).has('session')).toBe(false);
  });
});

describe('session filter fields', () => {
  const logFilters = () =>
    renderToStaticMarkup(
      createElement(RequestLogFilters, {
        query: { ...DEFAULT_QUERY, sessionId: SESSION },
        onChange: () => {},
        accounts: [],
        models: [],
      })
    );
  const usageRequests = () =>
    renderToStaticMarkup(
      createElement(RequestsView, {
        window: { fromMs: 0, toMs: 1 },
        refreshKey: 0,
        filters: { account: '', model: '', session: SESSION, device: '', failed: '' },
        onFiltersChange: () => {},
        accounts: [],
        models: [],
        devices: [],
        accountLabel: (index: string) => index,
        deviceLabel: (key: string) => key,
        showEmails: false,
      } as never)
    );

  for (const [name, render] of [
    ['Request Logs', logFilters],
    ['Usage requests', usageRequests],
  ] as const) {
    test(`${name} shows a masked chip while hidden and the field once shown`, () => {
      const hidden = render();
      expect(hidden).not.toContain('zebrafinch');
      expect(hidden).toContain('ze•••a@s•••.run');
      expect(hidden).toMatch(/Clear session/i);
      usePrivacyStore.getState().toggleShowEmails();
      expect(render()).toContain(`value="${SESSION}"`);
    });
  }

  test('ids without an email keep the plain field', () => {
    const html = renderToStaticMarkup(
      createElement(RequestLogFilters, {
        query: { ...DEFAULT_QUERY, sessionId: 'sess-1' },
        onChange: () => {},
        accounts: [],
        models: [],
      })
    );
    expect(html).toContain('value="sess-1"');
  });
});

describe('Usage grouped by session', () => {
  test('the group cell tooltip is masked', async () => {
    const { BreakdownTable } =
      await import('../src/features/taproot-usage/components/BreakdownTable');
    const totals = {
      requests: 1,
      failures: 0,
      inputTokens: 1,
      outputTokens: 1,
      reasoningTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      cacheWrite5mTokens: 0,
      cacheWrite1hTokens: 0,
      costUSD: 0,
    };
    const render = () =>
      renderToStaticMarkup(
        createElement(BreakdownTable, {
          rows: [{ key: SESSION, totals, costShare: null, failureRate: 0, cacheHitRatio: null }],
          groupBy: 'session',
          labelFor: () => 'user:rep',
          colorFor: () => null,
          onViewRequests: () => {},
        } as never)
      );
    const hidden = render();
    expect(hidden).not.toContain('zebrafinch');
    expect(hidden).toContain('title="user:report to ze•••a@s•••.run"');
    usePrivacyStore.getState().toggleShowEmails();
    expect(render()).toContain(`title="${SESSION}"`);
  });
});
