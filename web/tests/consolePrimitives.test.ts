import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement, type ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '@/i18n';
import { DataTable, DataTableCell, DataTableHead, StatusPill } from '@/components/ui/DataTable';
import { nextSegmentIndex } from '@/components/ui/radioGroupKeys';
import { Section } from '@/components/ui/Section';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { SeverityTag } from '@/components/ui/SeverityTag';
import { StatGroup, StatTile } from '@/components/ui/StatTile';
import { UpdatedAgo } from '@/components/ui/UpdatedAgo';
import { SEVERITY_TONE } from '@/components/ui/status';
import { PageHeader } from '@/components/layout/PageHeader';
import { agoParts, intervalParts } from '@/utils/time/ago';

const render = (element: ReactElement) => renderToStaticMarkup(element);
const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
// `bun test` loads SCSS modules as empty class maps, so styling is checked in the stylesheets.
const rule = (source: string, selector: string) => {
  const start = source.indexOf(`${selector} {`);
  expect({ selector, found: start >= 0 }).toEqual({ selector, found: true });
  return source.slice(start, source.indexOf('}', start));
};
const NOW = Date.UTC(2026, 10, 10, 12, 0, 0);

describe('StatTile', () => {
  test('shows its label, value, and sub-line', () => {
    const html = render(
      createElement(StatTile, { label: 'Failed', value: '5', sub: '1.1% of requests' })
    );
    expect(html).toContain('>Failed<');
    expect(html).toContain('>5<');
    expect(html).toContain('>1.1% of requests<');
    expect(html).not.toContain('aria-busy');
  });

  test('a tone colors the value with that status text ink', () => {
    const html = render(createElement(StatTile, { label: 'Failed', value: '5', tone: 'danger' }));
    expect(html).toContain('data-tone="danger"');
    const scss = read('src/components/ui/StatTile.module.scss');
    for (const tone of ['ok', 'warning', 'danger', 'info']) {
      expect(rule(scss, `.${tone}`)).toContain(`color: var(--status-${tone}-text);`);
    }
  });

  test('while loading it shows placeholders instead of a value', () => {
    const html = render(
      createElement(StatTile, { label: 'Requests', value: '451', sub: 'x', loading: true })
    );
    expect(html).toContain('aria-busy="true"');
    expect(html).toContain('>Requests<');
    expect(html).not.toContain('>451<');
    expect(html.match(/aria-hidden="true"/g)?.length).toBe(2);
  });

  test('a group names itself when given a label', () => {
    const html = render(createElement(StatGroup, { label: 'Last 24 hours', children: 'x' }));
    expect(html).toContain('role="group"');
    expect(html).toContain('aria-label="Last 24 hours"');
  });
});

describe('Section', () => {
  const content = createElement('p', null, 'rows');

  test('ready: a heading named by its title, with caption, action, and content', () => {
    const html = render(
      createElement(Section, {
        title: 'Recent failures',
        caption: '5 in 24h',
        action: createElement('a', { href: '#/request-logs' }, 'Open in Request Logs'),
        children: content,
      })
    );
    const id = html.match(/aria-labelledby="([^"]+)"/)?.[1];
    expect(id).toBeTruthy();
    expect(html).toContain(`<h2 id="${id}"`);
    expect(html).toContain('>5 in 24h<');
    expect(html).toContain('Open in Request Logs');
    expect(html).toContain('<p>rows</p>');
    expect(html).not.toContain('role="alert"');
    expect(html).not.toContain('Stale');
    expect(html).not.toContain('aria-busy');
  });

  test('first load: a skeleton that says it is loading', () => {
    const html = render(createElement(Section, { title: 'Traffic', loading: true }));
    expect(html).toContain('aria-busy="true"');
    expect(html).toContain('>Loading<');
  });

  test('reloading with content keeps the content and no skeleton', () => {
    const html = render(
      createElement(Section, { title: 'Traffic', loading: true, children: content })
    );
    expect(html).toContain('<p>rows</p>');
    expect(html).not.toContain('aria-busy');
  });

  test('failed with nothing loaded: the error takes the body, with Retry', () => {
    const html = render(
      createElement(Section, { title: 'Traffic', error: 'bad gateway', onRetry: () => undefined })
    );
    expect(html).toContain('role="alert"');
    expect(html).toContain('Couldn&#x27;t load this: bad gateway');
    expect(html).toContain('>Retry<');
    expect(html).not.toContain('Stale');
  });

  test('failed after a good load: the last data stays, marked stale, under the error', () => {
    const html = render(
      createElement(Section, {
        title: 'Traffic',
        error: 'timeout',
        onRetry: () => undefined,
        children: content,
      })
    );
    expect(html).toContain('role="alert"');
    expect(html).toContain('>Stale<');
    expect(html.indexOf('role="alert"')).toBeLessThan(html.indexOf('<p>rows</p>'));
  });

  test('can be marked stale without an error, and offers no Retry without a handler', () => {
    const stale = render(
      createElement(Section, { title: 'Traffic', stale: true, children: content })
    );
    expect(stale).toContain('>Stale<');
    const noRetry = render(createElement(Section, { title: 'Traffic', error: 'x' }));
    expect(noRetry).not.toContain('>Retry<');
  });

  test('server error text is masked while emails are hidden', () => {
    const html = render(
      createElement(Section, { title: 'Pools', error: 'refresh failed for alice@example.com' })
    );
    expect(html).not.toContain('alice@example.com');
    expect(html).toContain('refresh failed for a');
  });

  test('an empty message still says what failed', () => {
    const html = render(createElement(Section, { title: 'Pools', error: ' ' }));
    expect(html).toContain('Couldn&#x27;t load this.');
  });
});

describe('SeverityTag', () => {
  test('each severity has its word and a status tone', () => {
    const words = {
      blocking: 'Blocking',
      waiting: 'Waiting',
      overdue: 'Overdue',
      at_risk: 'At risk',
      ok: 'OK',
    };
    for (const [severity, word] of Object.entries(words) as [keyof typeof words, string][]) {
      const html = render(createElement(SeverityTag, { severity }));
      expect(html).toContain(`>${word}<`);
      expect(html).toContain(`data-severity="${severity}"`);
      expect(html).toContain(`data-tone="${SEVERITY_TONE[severity]}"`);
    }
    const scss = read('src/components/ui/SeverityTag.module.scss');
    for (const tone of ['ok', 'warning', 'danger', 'info']) {
      expect(rule(scss, `.${tone}`)).toContain(`color: var(--status-${tone}-text);`);
      expect(rule(scss, `.${tone}`)).toContain(`background: var(--status-${tone}-bg);`);
    }
  });

  test('blocking is danger, overdue and at risk warn, waiting is neutral', () => {
    expect(SEVERITY_TONE).toEqual({
      blocking: 'danger',
      overdue: 'warning',
      at_risk: 'warning',
      waiting: 'info',
      ok: 'ok',
    });
  });
});

describe('DataTable', () => {
  test('column headers are scoped, numbers right-aligned, models monospace', () => {
    const html = render(
      createElement(
        DataTable,
        null,
        createElement(
          'thead',
          null,
          createElement('tr', null, createElement(DataTableHead, { numeric: true }, 'Attempts'))
        ),
        createElement(
          'tbody',
          null,
          createElement(
            'tr',
            null,
            createElement(DataTableCell, { mono: true, muted: true }, 'claude-opus-5-5'),
            createElement(DataTableCell, { truncate: true }, 'Overloaded'),
            createElement(DataTableCell, { numeric: true }, 3),
            createElement(DataTableCell, null, createElement(StatusPill, null, '529'))
          )
        )
      )
    );
    expect(html).toContain('<th scope="col">Attempts</th>');
    expect(html).toContain('>claude-opus-5-5</td>');
    expect(html).toContain('data-tone="danger">529</span>');
    const scss = read('src/components/ui/DataTable.module.scss');
    expect(rule(scss, '.table .numeric')).toContain('text-align: right;');
    expect(rule(scss, '.mono')).toContain('font-family: $font-mono;');
    expect(rule(scss, '.truncate')).toContain('text-overflow: ellipsis;');
    expect(rule(scss, '  td')).toContain('font-variant-numeric: tabular-nums;');
  });

  test('a status pill takes another tone', () => {
    expect(render(createElement(StatusPill, { tone: 'ok' }, '200'))).toContain('data-tone="ok"');
  });
});

describe('SegmentedControl', () => {
  const options = ['1h', '24h', '7d', '30d'].map((value) => ({ value, label: value }));

  test('is a named radio group with one Tab stop on the selected option', () => {
    const html = render(
      createElement(SegmentedControl<string>, {
        options: [
          ...options.slice(0, 1),
          { value: '24h', label: '24h', ariaLabel: 'Last 24 hours' },
          ...options.slice(2),
        ],
        value: '24h',
        onChange: () => undefined,
        ariaLabel: 'Time window',
      })
    );
    expect(html).toContain('role="radiogroup" aria-label="Time window"');
    expect(html.match(/role="radio"/g)?.length).toBe(4);
    expect(html.match(/aria-checked="true"/g)?.length).toBe(1);
    expect(html).toMatch(
      /role="radio" aria-checked="true" aria-label="Last 24 hours" tabindex="0"/
    );
    expect(html.match(/tabindex="-1"/g)?.length).toBe(3);
  });

  test('an unknown value still leaves one Tab stop', () => {
    const html = render(
      createElement(SegmentedControl<string>, {
        options,
        value: '90d',
        onChange: () => undefined,
        ariaLabel: 'Time window',
      })
    );
    expect(html.match(/tabindex="0"/g)?.length).toBe(1);
  });

  test('arrows step and wrap, Home and End jump, other keys do nothing', () => {
    expect(nextSegmentIndex(1, 'ArrowRight', 4)).toBe(2);
    expect(nextSegmentIndex(1, 'ArrowDown', 4)).toBe(2);
    expect(nextSegmentIndex(3, 'ArrowRight', 4)).toBe(0);
    expect(nextSegmentIndex(1, 'ArrowLeft', 4)).toBe(0);
    expect(nextSegmentIndex(0, 'ArrowUp', 4)).toBe(3);
    expect(nextSegmentIndex(2, 'Home', 4)).toBe(0);
    expect(nextSegmentIndex(0, 'End', 4)).toBe(3);
    expect(nextSegmentIndex(1, 'Enter', 4)).toBeNull();
    expect(nextSegmentIndex(1, 'Tab', 4)).toBeNull();
    expect(nextSegmentIndex(0, 'ArrowRight', 0)).toBeNull();
  });
});

describe('UpdatedAgo', () => {
  test('counts seconds, then minutes, then hours', () => {
    expect(agoParts(NOW, NOW)).toEqual({ unit: 'just_now', count: 0 });
    expect(agoParts(NOW - 999, NOW)).toEqual({ unit: 'just_now', count: 0 });
    expect(agoParts(NOW - 8_000, NOW)).toEqual({ unit: 'seconds', count: 8 });
    expect(agoParts(NOW - 59_999, NOW)).toEqual({ unit: 'seconds', count: 59 });
    expect(agoParts(NOW - 60_000, NOW)).toEqual({ unit: 'minutes', count: 1 });
    expect(agoParts(NOW - 3_599_999, NOW)).toEqual({ unit: 'minutes', count: 59 });
    expect(agoParts(NOW - 3_600_000, NOW)).toEqual({ unit: 'hours', count: 1 });
  });

  test('a load stamped after the clock last ticked reads as just now', () => {
    expect(agoParts(NOW + 400, NOW)).toEqual({ unit: 'just_now', count: 0 });
  });

  test('intervals read in seconds unless they are whole minutes', () => {
    expect(intervalParts(30_000)).toEqual({ unit: 'seconds', count: 30 });
    expect(intervalParts(90_000)).toEqual({ unit: 'seconds', count: 90 });
    expect(intervalParts(300_000)).toEqual({ unit: 'minutes', count: 5 });
  });

  test('renders the full stamp, the compact one, or nothing', () => {
    const full = render(
      createElement(UpdatedAgo, { updatedAt: NOW - 8_000, intervalMs: 30_000, now: NOW })
    );
    expect(full).toContain('updated 8 s ago · refreshes every 30 s');
    expect(full).toContain(`dateTime="${new Date(NOW - 8_000).toISOString()}"`);
    const compact = render(
      createElement(UpdatedAgo, {
        updatedAt: NOW - 120_000,
        intervalMs: 30_000,
        compact: true,
        now: NOW,
      })
    );
    expect(compact).toContain('>2 min ago<');
    expect(render(createElement(UpdatedAgo, { updatedAt: null, now: NOW }))).toBe('');
  });
});

describe('PageHeader', () => {
  test('a banner with the page name, its stamp, and a named refresh button', () => {
    const html = render(
      createElement(PageHeader, {
        title: 'Dashboard',
        updatedAt: Date.now() - 8_000,
        intervalMs: 30_000,
        onRefresh: () => undefined,
        refreshLabel: 'Refresh All',
      })
    );
    expect(html.startsWith('<header')).toBe(true);
    expect(html).toContain('>Dashboard<');
    expect(html).toContain('refreshes every 30 s');
    expect(html).toContain('aria-label="Refresh All"');
  });

  test('without a stamp it shows only the name and the button', () => {
    const html = render(
      createElement(PageHeader, {
        title: 'Usage',
        onRefresh: () => undefined,
        refreshing: true,
        refreshLabel: 'Refresh All',
      })
    );
    expect(html).not.toContain('<time');
    expect(html).toContain('aria-disabled="true"');
  });
});
