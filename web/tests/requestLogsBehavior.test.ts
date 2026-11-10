import { describe, expect, test } from 'bun:test';
import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import '../src/i18n/index';
import { en } from '@/i18n/resources';
import type { RequestLogMeta } from '@/services/api/taproot';
import { usePrivacyStore } from '@/stores/usePrivacyStore';
import { BodyViewer } from '@/features/taproot-logs/components/BodyViewer';
import { JsonTree } from '@/features/taproot-logs/components/JsonTree';
import { RequestLogFilters } from '@/features/taproot-logs/components/RequestLogFilters';
import { RequestLogTable } from '@/features/taproot-logs/components/RequestLogTable';
import { isFormattableJson, parseJsonBody } from '@/features/taproot-logs/model/body';
import { encodeUrlValue } from '@/utils/urlValue';
import { childPage } from '@/features/taproot-logs/model/jsonTree';
import { JsonNumber, JsonObject, parseJsonValue } from '@/features/taproot-logs/model/jsonValue';
import {
  INITIAL_LIST,
  moreFailed,
  moreLoaded,
  startMore,
  startWalk,
  walkFailed,
  walkLoaded,
} from '@/features/taproot-logs/model/listState';
import {
  DEFAULT_QUERY,
  encodePlainValues,
  parseQuery,
  writeQuery,
} from '@/features/taproot-logs/model/query';
import { parseUpstream } from '@/features/taproot-logs/model/upstream';
import { THEMES, contrast, mustColor, readWeb } from './helpers/themeTokens';

const row = (id: string): RequestLogMeta => ({
  requestId: id,
  timestamp: '2026-11-10T10:00:00Z',
  durationMs: 12,
  method: 'POST',
  url: '/v1/messages',
  path: '/v1/messages',
  status: 200,
  model: 'claude-sonnet-4-5',
  streaming: false,
  errorOnly: false,
  requestBytes: 10,
  responseBytes: 20,
  recordBytes: 40,
});

describe('request log list state', () => {
  const loaded = walkLoaded({ logs: [row('a'), row('b')], nextCursor: 'c1' }, 1000);

  test('a failed refresh of the same filters keeps the list and its cursor', () => {
    const failed = walkFailed(startWalk(loaded), true, 'unavailable');
    expect(failed.rows.map((r) => r.requestId)).toEqual(['a', 'b']);
    expect(failed.nextCursor).toBe('c1');
    expect(failed).toMatchObject({ loading: false, loaded: true, error: 'unavailable' });
  });

  test('the cursor keeps its time anchor until a refresh succeeds', () => {
    expect(walkFailed(startWalk(loaded), true, 'unavailable').anchor).toBe(1000);
    expect(walkLoaded({ logs: [row('c')], nextCursor: 'c2' }, 2000).anchor).toBe(2000);
  });

  test('a failed walk for new filters does not show the old filters rows', () => {
    const failed = walkFailed(startWalk(loaded), false, 'boom');
    expect(failed.rows).toEqual([]);
    expect(failed.nextCursor).toBeNull();
    expect(failed.error).toBe('boom');
  });

  test('a failed first load is loaded, empty, and failed', () => {
    expect(walkFailed(startWalk(INITIAL_LIST), false, 'boom')).toMatchObject({
      rows: [],
      loaded: true,
      loading: false,
      error: 'boom',
    });
  });

  test('a failed next page keeps the list and is reported apart from the first page', () => {
    const failed = moreFailed(startMore(loaded), 'database is locked');
    expect(failed).toMatchObject({ error: '', moreError: 'database is locked', nextCursor: 'c1' });
    expect(failed.rows).toHaveLength(2);
    const retried = startMore(failed);
    expect(retried.moreError).toBe('');
    const more = moreLoaded(retried, { logs: [row('c')], nextCursor: null });
    expect(more.rows.map((r) => r.requestId)).toEqual(['a', 'b', 'c']);
  });
});

describe('request log table footer', () => {
  const render = (error: string) =>
    renderToStaticMarkup(
      createElement(
        MemoryRouter,
        null,
        createElement(RequestLogTable, {
          rows: [row('a')],
          detailHref: (id) => `/request-logs?requestId=${id}`,
          accountLabel: (index) => index,
          hasMore: true,
          loading: false,
          error,
          onLoadMore: () => {},
        })
      )
    );

  test('a failed page shows its error and a Retry button where the user is', () => {
    const html = render('database is locked');
    expect(html).toContain('Could not load more request logs: database is locked');
    expect(html).toContain('>Retry<');
    expect(html).not.toContain('Load more');
  });

  test('without a failure the footer offers Load more', () => {
    expect(render('')).toContain('Load more');
  });

  test('the observer is not rebuilt for every load attempt', () => {
    const source = readWeb('src/features/taproot-logs/components/RequestLogTable.tsx');
    expect(source).toContain('const autoLoad = hasMore && !error;');
    expect(source).toContain('}, [autoLoad, rows.length]);');
    expect(source).not.toContain('}, [hasMore, onLoadMore]);');
  });
});

describe('request logs page states', () => {
  const page = readWeb('src/features/taproot-logs/TaprootLogsPage.tsx');

  test('a failed first load shows the error with Retry, not an empty table', () => {
    expect(page).toContain(') : logs.rows.length === 0 ? (');
    expect(page).toContain('onClick={logs.reload}');
    expect(page).toContain("t('taproot_logs.list.retry')");
  });

  test('the list waits while a detail is open', () => {
    expect(page).toContain('useRequestLogs(query, !requestId)');
  });

  test('closing a detail the list opened goes back instead of adding an entry', () => {
    expect(page).toContain('navigate(-1)');
    expect(page).toContain('setParams(next, { replace: true });');
    expect(readWeb('src/features/taproot-logs/components/RequestLogTable.tsx')).toContain(
      'state={DETAIL_STATE}'
    );
  });
});

describe('formatted JSON shows what was stored', () => {
  const body =
    '{"metadata":{"user_id":"x","10":"ten","2":"two","a":"first"},' +
    '"seed":12345678901234567890,"dup":1,"dup":2,"f":1.50,"e":1e3,"n":-0.0}';

  test('numbers keep their text, keys their order, and duplicates their entries', () => {
    const value = parseJsonValue(body);
    expect(value).toBeInstanceOf(JsonObject);
    const entries = (value as JsonObject).entries;
    expect(entries.map(([key]) => key)).toEqual(['metadata', 'seed', 'dup', 'dup', 'f', 'e', 'n']);
    expect((entries[0][1] as JsonObject).entries.map(([key]) => key)).toEqual([
      'user_id',
      '10',
      '2',
      'a',
    ]);
    expect(entries[1][1]).toEqual(new JsonNumber('12345678901234567890'));
    expect(entries[4][1]).toEqual(new JsonNumber('1.50'));
    expect(entries[5][1]).toEqual(new JsonNumber('1e3'));
    expect(entries[6][1]).toEqual(new JsonNumber('-0.0'));
    expect(childPage(value, 10).map((child) => child.duplicate)).toEqual([
      false,
      false,
      false,
      true,
      false,
      false,
      false,
    ]);
  });

  test('the tree renders the stored values', () => {
    const parsed = parseJsonBody(body);
    if (!parsed.ok) throw new Error('expected JSON');
    const html = renderToStaticMarkup(createElement(JsonTree, { value: parsed.value }));
    expect(html).toContain('12345678901234567890');
    expect(html).not.toContain('12345678901234567000');
    expect(html).toContain('>1.50<');
    expect(html).toContain('>1e3<');
    expect(html.match(/>dup</g)).toHaveLength(2);
    expect(html).toContain('duplicate key');
    expect(html.indexOf('>user_id<')).toBeLessThan(html.indexOf('>10<'));
  });

  test('strings decode their escapes', () => {
    expect(parseJsonValue('["a\\"b\\\\c\\u00e9\\n", "plain", "x\\/y"]')).toEqual([
      'a"b\\cé\n',
      'plain',
      'x/y',
    ]);
  });

  test('accepts and rejects exactly what JSON.parse does', () => {
    const cases = [
      '{}',
      '[]',
      ' {"a" : [ true , false , null ] } ',
      '"\\ud83d\\ude00"',
      '0',
      '-1.5e-3',
      '[1,]',
      '{"a":1,}',
      '01',
      '1.',
      '.5',
      '+1',
      'NaN',
      "{'a':1}",
      '{"a"}',
      '"unterminated',
      '"bad \\x escape"',
      '"raw\ttab"',
      '[1] x',
      '',
      'tru',
      '[DONE]',
      '{"a":1}{"b":2}',
    ];
    for (const text of cases) {
      let native = true;
      try {
        JSON.parse(text);
      } catch {
        native = false;
      }
      let ours = true;
      try {
        parseJsonValue(text);
      } catch {
        ours = false;
      }
      expect({ text, ok: ours }).toEqual({ text, ok: native });
    }
  });
});

describe('deeply nested JSON', () => {
  const depth = 100_000;
  test('valid JSON too deep for the formatter says so instead of calling it invalid', () => {
    const text = '['.repeat(depth) + ']'.repeat(depth);
    expect(parseJsonBody(text)).toEqual({ ok: false, reason: 'too_deep' });
    expect(en.taproot_logs.body.too_deep_to_format).toContain('too deeply');
  });

  test('invalid JSON that is also deep is still invalid', () => {
    expect(parseJsonBody('['.repeat(depth))).toEqual({ ok: false, reason: 'invalid' });
  });
});

describe('stream events', () => {
  test('[DONE] gets no Formatted toggle that would do nothing', () => {
    expect(isFormattableJson('[DONE]')).toBe(false);
    expect(isFormattableJson('{"type":"ping"}')).toBe(true);
    const html = renderToStaticMarkup(
      createElement(BodyViewer, {
        text: 'data: {"id":1}\n\ndata: {"id":2}\n\ndata: [DONE]\n\n',
      })
    );
    expect(html).toContain('[DONE]');
    // The body's own Formatted/Raw pair, plus one toggle for each JSON event.
    expect(html.match(/aria-pressed=/g)).toHaveLength(4);
  });
});

describe('upstream blocks', () => {
  test('an unnumbered response holds its payload as body, even when it is an event stream', () => {
    const text =
      '=== API RESPONSE ===\nTimestamp: 2026-11-10T10:00:00Z\n' +
      'event: message_start\ndata: {"type":"message_start"}\n\n';
    expect(parseUpstream(text)).toEqual([
      {
        title: 'API RESPONSE',
        fields: [['Timestamp', '2026-11-10T10:00:00Z']],
        headers: [],
        body: 'event: message_start\ndata: {"type":"message_start"}',
      },
    ]);
  });

  test('an unnumbered request without a timestamp is all body', () => {
    expect(parseUpstream('=== API REQUEST ===\nmodel: m\n{"a":1}\n')).toEqual([
      { title: 'API REQUEST', fields: [], headers: [], body: 'model: m\n{"a":1}' },
    ]);
  });

  test('an error response keeps its status and its message as body', () => {
    expect(
      parseUpstream('=== API ERROR RESPONSE ===\nHTTP Status: 529\nupstream: overloaded\n')
    ).toEqual([
      {
        title: 'API ERROR RESPONSE',
        fields: [['HTTP Status', '529']],
        headers: [],
        body: 'upstream: overloaded',
      },
    ]);
  });

  test('numbered attempts still split into fields, headers, and body', () => {
    const [block] = parseUpstream(
      '=== API RESPONSE 1 ===\nTimestamp: t\nStatus: 200\n\nHeaders:\nContent-Type: x\n\nBody:\nevent: e\n'
    );
    expect(block.fields).toEqual([
      ['Timestamp', 't'],
      ['Status', '200'],
    ]);
    expect(block.headers).toEqual([['Content-Type', 'x']]);
    expect(block.body).toBe('event: e');
  });
});

describe('request log search and emails', () => {
  test('the search goes into the address bar encoded and comes back decoded', () => {
    const query = { ...DEFAULT_QUERY, q: 'user:alice@example.com' };
    const params = writeQuery(new URLSearchParams(), query);
    expect(params.toString()).not.toContain('alice');
    expect(parseQuery(params).q).toBe('user:alice@example.com');
    // Links written before encoding, and the app log's short ids, still work.
    expect(parseQuery(new URLSearchParams('q=309630fa')).q).toBe('309630fa');
  });

  test('a search holding an email shows as a masked chip while emails are hidden', () => {
    usePrivacyStore.setState({ showEmails: false });
    const html = renderToStaticMarkup(
      createElement(RequestLogFilters, {
        query: { ...DEFAULT_QUERY, q: 'alice@example.com' },
        onChange: () => {},
        accounts: [],
        models: [],
      })
    );
    expect(html).not.toContain('alice@example.com');
    expect(html).toContain('Clear Search');
  });
});

describe('links from before encoding', () => {
  test('a plain search or session id is rewritten encoded, keeping the other params', () => {
    const next = encodePlainValues(
      new URLSearchParams('q=alice%40example.com&sessionId=s-1&status=429&requestId=r1')
    );
    expect(next?.toString()).not.toContain('alice');
    expect(next?.get('q')).toBe(encodeUrlValue('alice@example.com'));
    expect(next?.get('sessionId')).toBe(encodeUrlValue('s-1'));
    expect(next?.get('status')).toBe('429');
    expect(next?.get('requestId')).toBe('r1');
  });

  test('an encoded address bar is left alone', () => {
    const params = writeQuery(new URLSearchParams(), { ...DEFAULT_QUERY, q: 'alice@example.com' });
    expect(encodePlainValues(params)).toBeNull();
    expect(encodePlainValues(new URLSearchParams('range=1h'))).toBeNull();
  });
});

describe('log colors meet WCAG AA', () => {
  const root = join(import.meta.dir, '../src/features/taproot-logs');
  const sheets = (dir: string): string[] =>
    readdirSync(dir, { withFileTypes: true }).flatMap((entry) =>
      entry.isDirectory()
        ? sheets(join(dir, entry.name))
        : entry.name.endsWith('.scss')
          ? [join(dir, entry.name)]
          : []
    );
  const inks = new Set<string>();
  for (const file of sheets(root)) {
    const source = readWeb(file.slice(join(import.meta.dir, '..').length + 1));
    for (const [, token] of source.matchAll(/(?:^|[\s;{])color:\s*var\(--([\w-]+)\)/g)) {
      inks.add(token);
    }
  }

  test('finds the stylesheets', () => {
    expect(inks.size).toBeGreaterThan(5);
  });

  for (const [mode, tokens] of Object.entries(THEMES)) {
    test(`${mode}: every text color reads at 4.5:1 on the cards and the page`, () => {
      const failures: string[] = [];
      for (const ink of inks) {
        for (const surface of ['bg-primary', 'bg-secondary', 'bg-tertiary']) {
          const ratio = contrast(mustColor(tokens, ink), mustColor(tokens, surface));
          if (ratio < 4.5) failures.push(`--${ink} on --${surface}: ${ratio.toFixed(2)}`);
        }
      }
      expect(failures).toEqual([]);
    });
  }
});

describe('request log copy', () => {
  const logs = en.taproot_logs;

  test('says what to change, with the full setting name', () => {
    expect(logs.list.empty_description).toContain('observability.logs.request-log');
    expect(logs.app.cpa_file_logging_required_desc).toContain('observability.logs.logging-to-file');
    expect(logs.app.preview_too_large).toContain('error log file');
  });

  test('uses no upstream jargon', () => {
    expect(JSON.stringify(logs)).not.toMatch(/\bCPA\b/);
  });
});
