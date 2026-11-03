import { describe, expect, test } from 'bun:test';
import {
  DEFAULT_QUERY,
  activeFilterCount,
  parseQuery,
  toFilter,
  writeQuery,
} from '@/features/taproot-logs/model/query';
import {
  PARSE_LIMIT_BYTES,
  parseJsonBody,
  parseSse,
  sniffBody,
  textWindow,
} from '@/features/taproot-logs/model/body';
import { parseUpstream } from '@/features/taproot-logs/model/upstream';
import {
  childCount,
  childPage,
  containerSummary,
  openByDefault,
} from '@/features/taproot-logs/model/jsonTree';
import {
  formatBytes,
  formatDurationMs,
  formatRatio,
  shortSessionId,
  statusTone,
  summarizeStorage,
} from '@/features/taproot-logs/model/format';
import type { RequestLogStats } from '@/services/api/taproot';

describe('request log query in the URL', () => {
  test('reads filters and ignores unrelated or invalid params', () => {
    const params = new URLSearchParams(
      'tab=requests&requestId=abc&range=7d&status=5XX&model=claude-opus-4-1&authIndex=8dc6f68dec36dcc4&sessionId=claude:s1&q=%20hello%20'
    );
    expect(parseQuery(params)).toEqual({
      range: '7d',
      status: '5xx',
      model: 'claude-opus-4-1',
      authIndex: '8dc6f68dec36dcc4',
      sessionId: 'claude:s1',
      q: 'hello',
    });
    expect(parseQuery(new URLSearchParams('range=forever&status=teapot'))).toEqual(DEFAULT_QUERY);
    expect(parseQuery(new URLSearchParams('status=429')).status).toBe('429');
    expect(parseQuery(new URLSearchParams())).toEqual(DEFAULT_QUERY);
  });

  test('writes only non-default filters and keeps other params', () => {
    const base = new URLSearchParams('requestId=abc&model=old');
    const next = writeQuery(base, { ...DEFAULT_QUERY, status: 'error', q: '  x ' });
    expect(next.toString()).toBe('requestId=abc&status=error&q=x');
    // The input is not mutated.
    expect(base.toString()).toBe('requestId=abc&model=old');
    expect(writeQuery(new URLSearchParams('range=1h'), DEFAULT_QUERY).toString()).toBe('');
  });

  test('round-trips through the URL', () => {
    const query = { ...DEFAULT_QUERY, range: '1h' as const, sessionId: 'claude:x', authIndex: 'a' };
    expect(parseQuery(writeQuery(new URLSearchParams(), query))).toEqual(query);
  });

  test('counts active filters', () => {
    expect(activeFilterCount(DEFAULT_QUERY)).toBe(0);
    expect(activeFilterCount({ ...DEFAULT_QUERY, range: '24h', model: 'm', q: ' ' })).toBe(2);
  });

  test('turns presets into a from bound anchored at now', () => {
    const now = Date.UTC(2026, 10, 3, 12, 0, 0);
    expect(
      toFilter({ ...DEFAULT_QUERY, range: '24h', status: '4xx' }, now, { limit: 100 })
    ).toEqual({
      limit: 100,
      from: '2026-11-02T12:00:00.000Z',
      status: '4xx',
    });
    expect(toFilter({ ...DEFAULT_QUERY, range: '7d' }, now).from).toBe('2026-10-27T12:00:00.000Z');
    expect(toFilter({ ...DEFAULT_QUERY, range: '1h' }, now).from).toBe('2026-11-03T11:00:00.000Z');
    // "All time" sends no time bound at all.
    expect(toFilter(DEFAULT_QUERY, now, { cursor: 'c' })).toEqual({ cursor: 'c' });
  });
});

describe('body classification', () => {
  test('sniffs the first non-space bytes', () => {
    expect(sniffBody('')).toBe('empty');
    expect(sniffBody(' \n\t')).toBe('empty');
    expect(sniffBody('  {"a":1}')).toBe('json');
    expect(sniffBody('[1]')).toBe('json');
    expect(sniffBody('event: message_start\ndata: {}\n\n')).toBe('sse');
    expect(sniffBody('data: {"x":1}\n\n')).toBe('sse');
    expect(sniffBody(': keepalive\n\n')).toBe('sse');
    expect(sniffBody('upstream timed out')).toBe('text');
  });

  test('parses JSON, reports invalid JSON, and refuses bodies over the limit without parsing', () => {
    expect(parseJsonBody('{"model":"m","n":[1,2]}')).toEqual({
      ok: true,
      value: { model: 'm', n: [1, 2] },
    });
    expect(parseJsonBody('{"truncated":')).toEqual({ ok: false, reason: 'invalid' });
    const huge = '{"a":"' + 'x'.repeat(PARSE_LIMIT_BYTES) + '"}';
    expect(parseJsonBody(huge)).toEqual({ ok: false, reason: 'too_large' });
  });

  test('splits event streams, joins multi-line data, and caps the kept events', () => {
    const stream =
      'event: message_start\ndata: {"a":1}\n\n' +
      ': comment only\n\n' +
      'data: line one\ndata: line two\n\n' +
      'event: ping\r\ndata: {}\r\n\r\n' +
      'event: message_stop\ndata: {"type":"message_stop"}\n\n';
    const all = parseSse(stream);
    expect(all.total).toBe(4);
    expect(all.events).toEqual([
      { event: 'message_start', data: '{"a":1}' },
      { event: 'message', data: 'line one\nline two' },
      { event: 'ping', data: '{}' },
      { event: 'message_stop', data: '{"type":"message_stop"}' },
    ]);
    const capped = parseSse(stream, 2);
    expect(capped.total).toBe(4);
    expect(capped.events.map((e) => e.event)).toEqual(['message_start', 'message']);
    expect(parseSse('')).toEqual({ events: [], total: 0 });
  });

  test('windows text without splitting a surrogate pair', () => {
    expect(textWindow('abcdef', 4)).toEqual({ visible: 'abcd', remaining: 2 });
    expect(textWindow('abc', 10)).toEqual({ visible: 'abc', remaining: 0 });
    // "a😀b": the emoji is two UTF-16 units at indexes 1 and 2.
    expect(textWindow('a\u{1F600}b', 2)).toEqual({ visible: 'a', remaining: 3 });
    expect(textWindow('', 5)).toEqual({ visible: '', remaining: 0 });
  });
});

describe('upstream sections', () => {
  const request = [
    '=== API REQUEST 1 ===',
    'Timestamp: 2026-11-02T20:06:00.79-08:00',
    'Upstream URL: http://127.0.0.1:18614/v1/messages?beta=true',
    'HTTP Method: POST',
    '',
    'Headers:',
    'Anthropic-Version: 2023-06-01',
    'Authorization: [redacted]',
    '',
    'Body:',
    '{"model":"claude-opus-4-1"}',
    '',
    '=== API REQUEST 2 ===',
    'Timestamp: 2026-11-02T20:06:01-08:00',
    '',
    'Body:',
    'line one',
    'Error: not a field once the body started',
    '',
  ].join('\n');

  test('splits attempts into fields, headers, and body', () => {
    expect(parseUpstream(request)).toEqual([
      {
        title: 'API REQUEST 1',
        fields: [
          ['Timestamp', '2026-11-02T20:06:00.79-08:00'],
          ['Upstream URL', 'http://127.0.0.1:18614/v1/messages?beta=true'],
          ['HTTP Method', 'POST'],
        ],
        headers: [
          ['Anthropic-Version', '2023-06-01'],
          ['Authorization', '[redacted]'],
        ],
        body: '{"model":"claude-opus-4-1"}',
      },
      {
        title: 'API REQUEST 2',
        fields: [['Timestamp', '2026-11-02T20:06:01-08:00']],
        headers: [],
        body: 'line one\nError: not a field once the body started',
      },
    ]);
  });

  test('keeps an unnumbered response block whose body follows the timestamp', () => {
    const response =
      '=== API RESPONSE ===\nTimestamp: t0\n{"type":"error"}\n\n\n=== API RESPONSE 1 ===\nTimestamp: t1\n\nStatus: 429\nHeaders:\nAnthropic-Ratelimit-Unified-7d-Utilization: 0.61\n\nBody:\n{"e":1}\n';
    const blocks = parseUpstream(response);
    expect(blocks.map((b) => b.title)).toEqual(['API RESPONSE', 'API RESPONSE 1']);
    expect(blocks[0]).toMatchObject({ fields: [['Timestamp', 't0']], body: '{"type":"error"}' });
    expect(blocks[1]).toMatchObject({
      fields: [
        ['Timestamp', 't1'],
        ['Status', '429'],
      ],
      headers: [['Anthropic-Ratelimit-Unified-7d-Utilization', '0.61']],
      body: '{"e":1}',
    });
  });

  test('falls back to verbatim text when the layout is unknown', () => {
    expect(parseUpstream('')).toEqual([]);
    expect(parseUpstream('  just text \n')).toEqual([
      { title: '', fields: [], headers: [], body: 'just text' },
    ]);
    expect(parseUpstream('preamble\n=== API REQUEST 1 ===\nBody:\nx')).toEqual([
      { title: '', fields: [], headers: [], body: 'preamble' },
      { title: 'API REQUEST 1', fields: [], headers: [], body: 'x' },
    ]);
  });
});

describe('JSON tree paging', () => {
  test('pages array and object children', () => {
    const messages = Array.from({ length: 250 }, (_, i) => ({ i }));
    expect(childCount(messages)).toBe(250);
    const page = childPage(messages, 100);
    expect(page).toHaveLength(100);
    expect(page[0]).toEqual(['0', { i: 0 }]);
    expect(page[99]).toEqual(['99', { i: 99 }]);
    expect(childPage({ a: 1, b: 2, c: 3 }, 2)).toEqual([
      ['a', 1],
      ['b', 2],
    ]);
    expect(childPage('scalar', 10)).toEqual([]);
    expect(childCount(null)).toBe(0);
    expect(childCount({})).toBe(0);
  });

  test('opens the root and small first-level containers only', () => {
    expect(openByDefault(0, 5000)).toBe(true);
    expect(openByDefault(1, 12)).toBe(true);
    expect(openByDefault(1, 13)).toBe(false);
    expect(openByDefault(2, 1)).toBe(false);
  });

  test('summarizes collapsed containers by their identifying key', () => {
    expect(containerSummary([1, 2, 3])).toEqual({ kind: 'array', count: 3 });
    expect(containerSummary({ role: 'user', content: [] })).toEqual({
      kind: 'object',
      hint: 'role: user',
      keys: ['content'],
      more: 0,
    });
    expect(containerSummary({ a: 1, b: 2, c: 3, d: 4, e: 5, f: 6 })).toEqual({
      kind: 'object',
      hint: '',
      keys: ['a', 'b', 'c', 'd'],
      more: 2,
    });
    // "type" wins over "role" and "name".
    expect(containerSummary({ name: 'n', type: 'text' })).toMatchObject({ hint: 'type: text' });
  });
});

describe('formatting', () => {
  test('formats sizes in 1024 units with three significant digits', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(-5)).toBe('0 B');
    expect(formatBytes(980)).toBe('980 B');
    expect(formatBytes(1536)).toBe('1.50 KB');
    expect(formatBytes(13_103_584)).toBe('12.5 MB');
    expect(formatBytes(20 * 1024 ** 3)).toBe('20.0 GB');
    expect(formatBytes(150 * 1024 ** 2)).toBe('150 MB');
  });

  test('formats durations', () => {
    expect(formatDurationMs(0)).toBe('0 ms');
    expect(formatDurationMs(133)).toBe('133 ms');
    expect(formatDurationMs(2345)).toBe('2.35 s');
    expect(formatDurationMs(42_000)).toBe('42.0 s');
    expect(formatDurationMs(125_000)).toBe('2m 5s');
    expect(formatDurationMs(-1)).toBe('—');
  });

  const stats: RequestLogStats = {
    bytesOnDisk: 1_000_000,
    rawBytes: 150_000_000,
    oldest: null,
    newest: null,
    count: 10,
    segments: 3,
    openSegments: 1,
    queueBytes: 0,
    droppedBodies: 2,
    droppedRecords: 1,
    truncatedResponses: 0,
    writeErrors: 4,
  };

  test('derives the compression ratio and the problem count', () => {
    expect(summarizeStorage(stats)).toEqual({ ratio: 150, problems: 7 });
    expect(formatRatio(150)).toBe('150×');
    expect(formatRatio(1)).toBe('1.0×');
    expect(formatRatio(2.46)).toBe('2.5×');
    // Nothing stored yet: no ratio rather than 0× or NaN.
    expect(summarizeStorage({ ...stats, bytesOnDisk: 0, rawBytes: 0 }).ratio).toBeNull();
    expect(formatRatio(null)).toBe('—');
  });

  test('maps statuses to tones and shortens session ids', () => {
    expect([200, 204, 304, 400, 429, 500, 0].map(statusTone)).toEqual([
      'success',
      'success',
      'info',
      'warn',
      'warn',
      'error',
      'error',
    ]);
    expect(shortSessionId('claude:22222222-aaaa-4bbb-8ccc-000000000002')).toBe('22222222…0002');
    expect(shortSessionId('codex:abc')).toBe('abc');
  });
});
