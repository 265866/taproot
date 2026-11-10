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

  test('the open fold lists the hidden devices, and their Admin switches work', () => {
    const html = render({ initiallyUnfolded: true });
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain('dana&#x27;s device · 100.64.0.31');
    expect(html).toContain('dana&#x27;s device · fd7a:115c:a1e0::32');
    expect(html).toContain('dana&#x27;s device ·0003');
    expect(html).toContain('bob-phone');
    expect(html).not.toContain('from a request');
    const danaAdmin = html.match(
      /<input[^>]*aria-label="Admin for dana&#x27;s device · 100\.64\.0\.31"[^>]*>/
    );
    expect(danaAdmin?.[0]).toBeDefined();
    expect(danaAdmin?.[0]).not.toContain('disabled');
    const serverAdmin = html.match(/<input[^>]*aria-label="Admin for taproot-host"[^>]*>/);
    expect(serverAdmin?.[0]).toContain('disabled');
  });

  test('rows show no presence and keep OS and MagicDNS in the tooltip', () => {
    const html = render({ initiallyUnfolded: true });
    expect(html).toContain('title="Linux · alice-desktop.tailnet-0000.ts.net"');
    expect(html).toContain(
      'title="alice-mbp.tailnet-1111.ts.net · Linux · alice-mbp.tailnet-1111.ts.net"'
    );
    expect(html).toContain('>Work laptop<');
    expect(html).not.toContain('Online');
    expect(html).not.toContain('Last seen');
    expect(html).not.toContain('days ago');
    expect(html).not.toContain('Joined');
  });

  test('requests show who, which client, when, and how often, with Allow and Deny', () => {
    const html = render();
    expect(html).toContain('Dana&#x27;s PC');
    expect(html).toContain('dana@example.com · Claude Code · 14 minutes ago · 7 times');
    expect(html).toContain('aria-label="Allow Dana&#x27;s PC"');
    expect(html).toContain('aria-label="Deny Dana&#x27;s PC"');
  });

  test('a request from a nameless device shows its IP, else its ID', () => {
    const nameless = { ...data, requests: [{ ...data.requests[0], name: '' }] };
    const html = render({ data: nameless, draft: draftFrom(nameless) });
    expect(html).toContain('aria-label="Allow dana&#x27;s device · 100.64.0.31"');
    // The tailnet read did not list the device, so there is no IP to show.
    const unlisted = {
      ...nameless,
      devices: nameless.devices.map((d) =>
        d.id === nameless.requests[0].nodeId ? { ...d, addresses: [] } : d
      ),
    };
    expect(render({ data: unlisted, draft: draftFrom(unlisted) })).toContain(
      'aria-label="Allow dana&#x27;s device ·0001"'
    );
  });

  test('a request for the dashboard says so, and one for Proxy does not', () => {
    expect(render()).toContain('>wants dashboard<');
    const proxyOnly = { ...data, requests: [{ ...data.requests[0], asked: 'proxy' as const }] };
    expect(render({ data: proxyOnly })).not.toContain('wants dashboard');
  });

  test('a request from a labeled device shows the label, and Allow starts from it', () => {
    const labeled = {
      ...data,
      devices: data.devices.map((d) => (d.id === 'nBOBCNTRL' ? { ...d, label: 'Bob laptop' } : d)),
      requests: [{ ...data.requests[0], nodeId: 'nBOBCNTRL', name: 'Windows PC', outside: false }],
    };
    const html = render({ data: labeled, draft: draftFrom(labeled) });
    expect(html).toContain('aria-label="Allow Bob laptop"');
    expect(html).not.toContain('Windows PC');
  });

  test('devices with a permission have a rename button, and a staged name shows in its row', () => {
    const html = render();
    expect(html).toContain('aria-label="Rename alice-desktop"');
    expect(html).toContain('aria-label="Rename Work laptop"');
    expect(html).not.toContain('aria-label="Rename taproot-host"');
    // alice-work-laptop has no permission, so a name for it would not be saved.
    expect(html).toContain('aria-label="Proxy for alice-work-laptop"');
    expect(html).not.toContain('aria-label="Rename alice-work-laptop"');
    const draft = draftFrom(data);
    draft.labels.nBOBCNTRL = 'Bob laptop';
    const renamed = render({ draft });
    expect(renamed).toContain('aria-label="Rename Bob laptop"');
    expect(renamed).toContain('aria-label="Proxy for Bob laptop"');
    expect(renamed).toContain('1 unsaved change');
    // Switching the permissions off drops the staged name from the row and the change count.
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    const off = render({ draft });
    expect(off).toContain('aria-label="Proxy for bob-laptop"');
    expect(off).not.toContain('Bob laptop');
    expect(off).not.toContain('aria-label="Rename bob-laptop"');
    expect(off).toContain('1 unsaved change');
    // Switching one on makes the row renamable before the save.
    draft.devices.nWORKCNTRL = { proxy: true, admin: false };
    expect(render({ draft })).toContain('aria-label="Rename alice-work-laptop"');
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

  test('Deny dismisses the request, Allow closes it, and the gate posts its name, all with the request header', async () => {
    const client = createTaprootClient(`http://127.0.0.1:${server.port}/taproot/api/v1`);
    await client.dismissRequest('nDANA0001CNTRL');
    await client.allowRequest('nDANA0001CNTRL');
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
        path: '/taproot/api/v1/access/requests/nDANA0001CNTRL/allow',
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
