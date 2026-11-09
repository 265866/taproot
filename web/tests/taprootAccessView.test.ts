/**
 * Renders the loaded Access page as static markup. bun cannot load SCSS modules, so the page's
 * and its controls' stylesheets echo class names.
 */
import { afterAll, beforeAll, describe, expect, mock, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { createTaprootClient } from '@/services/api/taproot';
import type { AccessViewProps } from '@/features/taproot-access/TaprootAccessPage';
import { data, NOW, onDesktop } from './fixtures/accessDevices';

const classEcho = () => ({
  default: new Proxy({}, { get: (_, key) => (typeof key === 'string' ? key : undefined) }),
});
const src = join(import.meta.dir, '../src');
for (const sheet of [
  'features/taproot-access/TaprootAccessPage.module.scss',
  'components/ui/ToggleSwitch.module.scss',
  'components/ui/SelectionCheckbox.module.scss',
]) {
  mock.module(join(src, sheet), classEcho);
}
const { AccessView } = await import('@/features/taproot-access/TaprootAccessPage');
const { draftFrom } = await import('@/features/taproot-access/model/accessModel');

const render = (over: Partial<AccessViewProps> = {}) =>
  renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(AccessView, {
        data,
        me: onDesktop,
        draft: draftFrom(data),
        setDraft: () => {},
        query: '',
        setQuery: () => {},
        now: NOW,
        saving: false,
        saveError: '',
        onSave: () => {},
        onReset: () => {},
        onAllow: async () => null,
        onDeny: () => {},
        ...over,
      })
    )
  );

/** Each role="row" element's opening tag and the class of every cell in it. */
function rows(html: string) {
  const out: { tag: string; cells: string[] }[] = [];
  const rowRe =
    /<div class="([^"]*)" role="row">([\s\S]*?)(?=<div class="[^"]*" role="row"|<\/div><\/div>|$)/g;
  for (const m of html.matchAll(rowRe)) {
    const cells = [...m[2].matchAll(/<span([^>]*)>/g)]
      .filter((c) => /role="(?:cell|columnheader)"/.test(c[1]))
      .map((c) => /class="([^"]*)"/.exec(c[1])?.[1] ?? '');
    out.push({ tag: m[1], cells });
  }
  return out;
}

describe('the device table', () => {
  test('the header has the same columns as the rows, and its Stable ID cell hides with theirs', () => {
    const html = render({ initiallyUnfolded: true });
    const all = rows(html).filter((r) => r.tag.split(' ').includes('row'));
    const head = all.find((r) => r.tag.split(' ').includes('head'));
    expect(head?.cells).toEqual(['', 'id', '', '']);
    const deviceRows = all.filter((r) => r !== head);
    expect(deviceRows.length).toBeGreaterThan(10);
    for (const row of deviceRows) {
      expect(row.cells[1]).toBe('id');
      // A removed device's Forget button spans both switch columns.
      if (row.tag.split(' ').includes('gone'))
        expect(row.cells).toEqual(['device', 'id', 'forget']);
      else expect(row.cells.length).toBe(head?.cells.length ?? 0);
    }
    const sheetForget =
      /\.forget \{\s*grid-column: 3 \/ span 2;[\s\S]*?@media \(max-width: 820px\) \{\s*grid-column: auto \/ span 2;/;
    // At 820px and below, `.row .id` hides: the header row carries .row, so its cell hides too.
    const sheet = readFileSync(
      join(src, 'features/taproot-access/TaprootAccessPage.module.scss'),
      'utf8'
    );
    expect(sheet).toMatch(sheetForget);
    expect(sheet).toMatch(
      /\.row \{[^}]*@media \(max-width: 820px\) \{\s*grid-template-columns: minmax\(0, 1fr\) auto auto;\s*\.id \{\s*display: none;/
    );
  });

  test('sections in order, with the fold closed and its count shown', () => {
    const html = render();
    const order = [
      'Requests',
      '>Yours<',
      '>Servers<',
      '>Alice<',
      '>bob@github<',
      'other devices can reach',
      '>Removed<',
      'Addresses without Tailscale',
    ];
    let at = -1;
    for (const marker of order) {
      const next = html.indexOf(marker, at + 1);
      expect(next).toBeGreaterThan(at);
      at = next;
    }
    expect(html).toContain('6 other devices can reach this server and have no access');
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain('bob-phone');
    expect(html).toContain('alice@example.org');
    expect(html).toContain('other tailnet');
  });

  test('the open fold lists the hidden devices; outside devices get Admin only from a request', () => {
    const html = render({ initiallyUnfolded: true });
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain('dana&#x27;s device ·0002');
    expect(html).toContain('bob-phone');
    expect(html).toContain('from a request');
    expect(html).toContain('aria-label="Admin for dana&#x27;s device ·0001"');
  });

  test('rows show presence and keep OS and MagicDNS in the tooltip', () => {
    const html = render({ initiallyUnfolded: true });
    expect(html).toContain('title="Linux · alice-desktop.tailnet-0000.ts.net"');
    expect(html).toContain(
      'title="alice-mbp.tailnet-1111.ts.net · Linux · alice-mbp.tailnet-1111.ts.net"'
    );
    expect(html).toContain('>Work laptop<');
    expect(html).toContain('Online');
    expect(html).toContain('Last seen 2 days ago');
    expect(html).toContain('Last seen 45 days ago');
    expect(html).not.toContain('Joined');
  });

  test('requests show who, which client, when, and how often, with Allow and Deny', () => {
    const html = render();
    expect(html).toContain('Dana&#x27;s PC');
    expect(html).toContain('dana@example.com · Claude Code · 14 minutes ago · 7 times');
    expect(html).toContain('aria-label="Allow Dana&#x27;s PC"');
    expect(html).toContain('aria-label="Deny Dana&#x27;s PC"');
  });

  test('no explanations, and no save bar until something changes', () => {
    const html = render();
    expect(html).not.toContain('New devices start with nothing');
    expect(html).not.toContain('sends model requests through taproot');
    expect(html).not.toContain('unsaved');
    const draft = draftFrom(data);
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    expect(render({ draft })).toContain('1 unsaved change');
  });
});

describe('request calls', () => {
  let server: ReturnType<typeof Bun.serve>;
  const seen: { method: string; path: string; header: string | null; body: string }[] = [];
  beforeAll(() => {
    server = Bun.serve({
      port: 0,
      async fetch(request) {
        seen.push({
          method: request.method,
          path: new URL(request.url).pathname,
          header: request.headers.get('x-taproot-request'),
          body: await request.text(),
        });
        return Response.json({ ok: true });
      },
    });
  });
  afterAll(() => server.stop(true));

  test('Deny dismisses the request, and the gate posts its name, both with the request header', async () => {
    const client = createTaprootClient(`http://127.0.0.1:${server.port}/taproot/api/v1`);
    await client.dismissRequest('nDANA0001CNTRL');
    await client.requestAccess("Dana's PC");
    expect(seen).toEqual([
      {
        method: 'POST',
        path: '/taproot/api/v1/access/requests/nDANA0001CNTRL/dismiss',
        header: '1',
        body: '',
      },
      {
        method: 'POST',
        path: '/taproot/api/v1/access/request',
        header: '1',
        body: JSON.stringify({ name: "Dana's PC" }),
      },
    ]);
  });
});
