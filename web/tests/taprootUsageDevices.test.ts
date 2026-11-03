import { describe, expect, test } from 'bun:test';
import { createElement, type ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import type { UsageDevice } from '@/services/api/taproot';
import {
  TTL_MIN_SPLIT_REQUESTS,
  deviceFilterKey,
  loginLabel,
  sortDevices,
  ttlStatus,
} from '@/features/taproot-usage/logic/devices';
import { DevicesTable, TtlAdvice } from '@/features/taproot-usage/components/DevicesView';
import { en } from '@/i18n/resources';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
const render = (element: ReactElement) =>
  renderToStaticMarkup(createElement(I18nextProvider, { i18n }, element));

function device(
  id: string,
  over: Partial<Omit<UsageDevice, 'mainConversation'>> = {},
  main: Partial<UsageDevice['mainConversation']> = {}
): UsageDevice {
  return {
    deviceId: id,
    name: id ? id.replace(/^node:/, '') : '',
    login: id ? 'alice@github' : '',
    via: id ? 'tailnet' : '',
    lastSeen: '2026-11-03T08:00:00Z',
    requests: 10,
    failures: 0,
    inputTokens: 100,
    outputTokens: 10,
    reasoningTokens: 0,
    cacheReadTokens: 900,
    cacheWriteTokens: 0,
    cacheWrite5mTokens: 0,
    cacheWrite1hTokens: 0,
    costUSD: 1,
    cacheHitRatio: 0.9,
    ...over,
    mainConversation: {
      requests: 0,
      splitRequests: 0,
      cacheWriteTokens: 0,
      cacheWrite5mTokens: 0,
      cacheWrite1hTokens: 0,
      fiveMinuteShare: null,
      estimate: {
        rewriteRequests: 0,
        rewriteTokens: 0,
        rewriteCostUSD: 0,
        oneHourPremiumUSD: 0,
        netSavingsUSD: 0,
      },
      ...main,
    },
  };
}

describe('cache TTL flag', () => {
  test('flags only a majority of 5m writes over enough split-known requests', () => {
    const enough = TTL_MIN_SPLIT_REQUESTS;
    expect(ttlStatus(device('a', {}, { fiveMinuteShare: 0.51, splitRequests: enough }))).toBe(
      'flag'
    );
    // Exactly half is not "mostly".
    expect(ttlStatus(device('a', {}, { fiveMinuteShare: 0.5, splitRequests: enough }))).toBe('ok');
    expect(ttlStatus(device('a', {}, { fiveMinuteShare: 0.1, splitRequests: 500 }))).toBe('ok');
    expect(ttlStatus(device('a', {}, { fiveMinuteShare: 1, splitRequests: enough - 1 }))).toBe(
      'few'
    );
    // History without the split, or only subagent writes, says nothing about the setting.
    expect(ttlStatus(device('a', {}, { fiveMinuteShare: null, requests: 400 }))).toBe('none');
  });
});

describe('device helpers', () => {
  test('requests without a device filter by the none key', () => {
    expect(deviceFilterKey('')).toBe('(none)');
    expect(deviceFilterKey('node:nA')).toBe('node:nA');
  });

  test('hidden logins keep only their domain', () => {
    expect(loginLabel('alice@github', false)).toBe('•••@github');
    expect(loginLabel('bob@example.org', true)).toBe('bob@example.org');
    expect(loginLabel('', false)).toBe('');
  });
});

describe('device table sorting', () => {
  const laptop = device('node:laptop', { costUSD: 5, requests: 50 }, { fiveMinuteShare: 0.2 });
  const phone = device(
    'node:phone',
    { costUSD: 2, requests: 80 },
    {
      fiveMinuteShare: 0.9,
      splitRequests: 40,
    }
  );
  const unknown = device('', { costUSD: 9, requests: 5 });
  const lan = device(
    'cidr:192.168.0.88',
    { name: '192.168.0.88', costUSD: 2, requests: 1, login: '', cacheHitRatio: null },
    { fiveMinuteShare: 0.6, splitRequests: 2 }
  );
  const all = [laptop, phone, unknown, lan];
  const ids = (rows: UsageDevice[]) => rows.map((d) => d.deviceId);

  test('cost descending breaks ties by ID', () => {
    expect(ids(sortDevices(all, 'costUSD', 'desc'))).toEqual([
      '',
      'node:laptop',
      'cidr:192.168.0.88',
      'node:phone',
    ]);
  });

  test('names sort alphabetically with the unknown device last in both directions', () => {
    expect(ids(sortDevices(all, 'name', 'asc'))).toEqual([
      'cidr:192.168.0.88',
      'node:laptop',
      'node:phone',
      '',
    ]);
    expect(ids(sortDevices(all, 'name', 'desc'))).toEqual([
      'node:phone',
      'node:laptop',
      'cidr:192.168.0.88',
      '',
    ]);
  });

  test('missing 5m shares and cache hits go last', () => {
    expect(ids(sortDevices(all, 'fiveMinuteShare', 'desc'))).toEqual([
      'node:phone',
      'cidr:192.168.0.88',
      'node:laptop',
      '',
    ]);
    expect(ids(sortDevices(all, 'fiveMinuteShare', 'asc'))).toEqual([
      'node:laptop',
      'cidr:192.168.0.88',
      'node:phone',
      '',
    ]);
    expect(ids(sortDevices(all, 'cacheHitRatio', 'asc')).at(-1)).toBe('cidr:192.168.0.88');
  });

  test('the TTL column ranks flagged devices first, then by status and 5m share', () => {
    expect(ids(sortDevices(all, 'ttl', 'desc'))).toEqual([
      'node:phone',
      'cidr:192.168.0.88',
      'node:laptop',
      '',
    ]);
  });

  test('requests sort numerically, not as text', () => {
    expect(ids(sortDevices(all, 'requests', 'desc'))).toEqual([
      'node:phone',
      'node:laptop',
      '',
      'cidr:192.168.0.88',
    ]);
  });
});

describe('devices tab rendering', () => {
  const flagged = device(
    'node:desktop',
    {},
    {
      requests: 30,
      splitRequests: 30,
      cacheWrite5mTokens: 900_000,
      cacheWrite1hTokens: 100_000,
      fiveMinuteShare: 0.9,
      estimate: {
        rewriteRequests: 4,
        rewriteTokens: 400_000,
        rewriteCostUSD: 1.92,
        oneHourPremiumUSD: 1.5,
        netSavingsUSD: 0.42,
      },
    }
  );

  test('the table names unknown devices, flags the TTL, and hides logins on request', () => {
    const html = render(
      createElement(DevicesTable, {
        devices: [flagged, device('')],
        showEmails: false,
        onViewRequests: () => {},
      })
    );
    expect(html).toContain('Unknown device');
    expect(html).toContain('desktop');
    expect(html).toContain('Mostly 5m');
    expect(html).toContain('•••@github');
    expect(html).not.toContain('alice@github');
    expect(html).toContain('5m 90%, 1h 10%');
  });

  test('the TTL advice gives the setting and labels the estimate', () => {
    const html = render(createElement(TtlAdvice, { devices: [flagged] }));
    expect(html).toContain('90%</span> of main-conversation cache writes were 5-minute');
    expect(html).toContain('&quot;promptCacheTtl&quot;: &quot;1h&quot;');
    expect(html).toContain('~/.claude/settings.json');
    expect(html).toContain('Estimate');
    expect(html).toContain('4 cache writes totalling 400K tokens');
    expect(html).toContain('about $0.42 saved');
  });
});
