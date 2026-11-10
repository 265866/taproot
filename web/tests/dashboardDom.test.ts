/**
 * Drives the Dashboard's failure rows and rail in a DOM (happy-dom). happy-dom lays nothing out, so
 * the test stands in for layout: a one-line cell is cut while its text is longer than `CUT_AT`
 * characters and its row is closed, and a stubbed ResizeObserver reports a size whenever the test
 * says the browser would have laid out again.
 */
import { afterAll, describe, expect, test } from 'bun:test';
import { GlobalRegistrator } from '@happy-dom/global-registrator';

GlobalRegistrator.register({ url: 'http://localhost/' });

const { act, createElement, useLayoutEffect, useState } = await import('react');
const { createRoot } = await import('react-dom/client');
const { MemoryRouter } = await import('react-router-dom');
await import('@/i18n');
const {
  normalizeAttention,
  normalizeClientRequests,
  normalizePools,
  normalizeRequestSummary,
  normalizeStatus,
} = await import('@/services/api/taprootStatus');
const { taprootApi } = await import('@/services/api/taproot');
const { windowRange } = await import('@/features/dashboard/model/window');
const { RecentFailures } = await import('@/features/dashboard/components/Lists');
const { DashboardView } = await import('@/features/dashboard/DashboardPage');
const { railLayout, reservedHeight, storeReservedHeight } =
  await import('@/features/dashboard/hooks/useReservedHeight');

const actGlobal = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
actGlobal.IS_REACT_ACT_ENVIRONMENT = true;

/** How many characters a one-line cell shows; a wider window shows more. */
let cutAt = 30;
/** Which elements the stylesheet would hide at the current width; nothing by default. */
let hidden: (element: Element) => boolean = () => false;
const openRow = (element: Element) =>
  element.closest('[aria-expanded="true"]') !== null ||
  element.closest('tr')?.querySelector('[aria-expanded="true"]') != null;
Object.defineProperty(window.HTMLElement.prototype, 'clientWidth', {
  configurable: true,
  get: () => 100,
});
Object.defineProperty(window.HTMLElement.prototype, 'scrollWidth', {
  configurable: true,
  get(this: HTMLElement) {
    return !openRow(this) && (this.textContent?.length ?? 0) > cutAt ? 200 : 100;
  },
});
window.Element.prototype.getClientRects = function (this: Element) {
  return (hidden(this) ? [] : [this.getBoundingClientRect()]) as unknown as DOMRectList;
};

type Callback = () => void;
const observers = new Set<{ callback: Callback }>();
class StubResizeObserver {
  constructor(private callback: Callback) {}
  observe() {
    observers.add(this);
  }
  unobserve() {}
  disconnect() {
    observers.delete(this);
  }
}
globalThis.ResizeObserver = StubResizeObserver as unknown as typeof ResizeObserver;
/** The browser lays out again: every observer reports its element's size. */
const layout = () =>
  act(() => {
    for (const observer of [...observers]) observer.callback();
  });

const realCreateBackup = taprootApi.createBackup;
afterAll(() => {
  taprootApi.createBackup = realCreateBackup;
  delete actGlobal.IS_REACT_ACT_ENVIRONMENT;
  GlobalRegistrator.unregister();
});

const mount = () => {
  const container = document.createElement('div');
  document.body.append(container);
  const root = createRoot(container);
  return {
    container,
    render: (element: ReturnType<typeof createElement>) =>
      act(() => root.render(createElement(MemoryRouter, null, element))),
    unmount: () => act(() => root.unmount()),
  };
};

const click = (element: Element) =>
  act(() => {
    (element as HTMLElement).click();
  });

const NOW = Date.parse('2026-11-10T12:34:56Z');
const SUMMARY = normalizeRequestSummary({
  totals: { requests: 10, failed: 1, failureRate: 0.1, retries: 0, costUSD: 1 },
  buckets: [],
});
const summary = { data: { range: windowRange('24h', NOW), summary: SUMMARY }, error: null };
const LONG_ERROR = 'no enabled account serves model gemini-3-pro after three attempts';
const LONG_MODEL = 'claude-opus-5-5-with-a-very-long-suffix';

const failure = (over: Record<string, unknown>) => ({
  ts: new Date(NOW - 5 * 60_000).toISOString(),
  deviceName: 'laptop',
  model: 'claude-opus-5-5',
  status: 529,
  failed: true,
  errorMessage: LONG_ERROR,
  requestId: '',
  ...over,
});
const failures = (...rows: Record<string, unknown>[]) => ({
  data: normalizeClientRequests({ requests: rows.map(failure) }),
  error: null,
});

const failuresView = (rows: Record<string, unknown>[]) =>
  createElement(RecentFailures, {
    failures: failures(...rows),
    summary,
    timeWindow: '24h',
    nowMs: NOW,
    onRetry: () => {},
  });

const renderFailures = (over: Record<string, unknown>) => {
  const page = mount();
  page.render(failuresView([over]));
  layout();
  return page;
};

describe('a failure row without a log', () => {
  test('in the table, collapsing a cut error keeps the focus on its toggle', () => {
    const page = renderFailures({});
    const toggle = page.container.querySelector('table button[aria-expanded]') as HTMLElement;
    expect(toggle).not.toBeNull();
    toggle.focus();
    click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    // Open, the error wraps and fits; the browser lays the row out again.
    layout();
    click(toggle);
    expect(document.activeElement).toBe(toggle);
    expect(toggle.isConnected).toBe(true);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    layout();
    expect(document.activeElement).toBe(toggle);
    page.unmount();
  });

  test('on a phone, collapsing the row keeps the focus on it', () => {
    const page = renderFailures({});
    const row = page.container.querySelector('ul button[aria-expanded]') as HTMLElement;
    expect(row).not.toBeNull();
    row.focus();
    click(row);
    layout();
    click(row);
    expect(document.activeElement).toBe(row);
    expect(row.isConnected).toBe(true);
    page.unmount();
  });

  test('a cut model opens the row though its error fits, and the open row shows it whole', () => {
    const page = renderFailures({ errorMessage: 'status 529', model: LONG_MODEL });
    const toggle = page.container.querySelector('table button[aria-expanded]') as HTMLElement;
    expect(toggle).not.toBeNull();
    const modelCell = [...page.container.querySelectorAll('table td span')].find(
      (span) => span.textContent === LONG_MODEL
    ) as HTMLElement;
    expect(modelCell.scrollWidth).toBeGreaterThan(modelCell.clientWidth);
    click(toggle);
    // The row is open, which the styles read to wrap every cell in it.
    expect(modelCell.closest('tr')?.querySelector('[aria-expanded="true"]')).toBe(toggle);
    expect(modelCell.scrollWidth).toBe(modelCell.clientWidth);
    page.unmount();
  });

  test('a cut device opens the row too, and a row with a log never needs to', () => {
    const cutDevice = renderFailures({
      errorMessage: 'status 529',
      deviceName: 'a-device-with-a-very-long-name-indeed',
    });
    expect(cutDevice.container.querySelector('table button[aria-expanded]')).not.toBeNull();
    cutDevice.unmount();
    const logged = renderFailures({ model: LONG_MODEL, requestId: 'req-1' });
    expect(logged.container.querySelector('table button[aria-expanded]')).toBeNull();
    logged.unmount();
  });

  test('a row opened in a window since widened keeps its toggle while it holds the focus', () => {
    const page = renderFailures({});
    const toggle = page.container.querySelector('table button[aria-expanded]') as HTMLElement;
    toggle.focus();
    click(toggle);
    // The window widens while the row is open: closed, the error would fit on its line.
    cutAt = 1_000;
    try {
      layout();
      click(toggle);
      layout();
      expect(toggle.isConnected).toBe(true);
      expect(document.activeElement).toBe(toggle);
      expect(toggle.getAttribute('aria-expanded')).toBe('false');
      // Once the focus moves on, nothing is left to expand, and the toggle goes.
      act(() => toggle.blur());
      layout();
      expect(page.container.querySelector('table button[aria-expanded]')).toBeNull();
    } finally {
      cutAt = 30;
      page.unmount();
    }
  });

  test('a row whose cells all fit offers nothing to expand', () => {
    const page = renderFailures({ errorMessage: 'status 529' });
    expect(page.container.querySelector('button[aria-expanded]')).toBeNull();
    page.unmount();
  });
});

describe('a poll that brings a new failure', () => {
  test('an open row stays open and keeps the focus', () => {
    const older = { ts: new Date(NOW - 9 * 60_000).toISOString() };
    const newer = { ts: new Date(NOW - 60_000).toISOString(), requestId: 'req-new' };
    const page = mount();
    page.render(failuresView([older]));
    layout();
    const toggle = page.container.querySelector('table button[aria-expanded]') as HTMLElement;
    const card = page.container.querySelector('ul button[aria-expanded]') as HTMLElement;
    click(card);
    toggle.focus();
    click(toggle);

    page.render(failuresView([newer, older]));
    layout();
    expect(page.container.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(toggle.isConnected).toBe(true);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(document.activeElement).toBe(toggle);
    expect(card.isConnected).toBe(true);
    expect(card.getAttribute('aria-expanded')).toBe('true');
    page.unmount();
  });

  test('identical failures each get their own key', () => {
    const page = mount();
    const errors: unknown[] = [];
    const realError = console.error;
    console.error = (...args: unknown[]) => errors.push(args[0]);
    try {
      page.render(failuresView([{}, {}]));
    } finally {
      console.error = realError;
    }
    expect(page.container.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(errors.filter((error) => String(error).includes('same key'))).toEqual([]);
    page.unmount();
  });
});

describe('the rail crossing the rail breakpoint', () => {
  const TITLE = 'The last scheduled backup failed';
  const attention = {
    data: normalizeAttention({
      items: [
        {
          id: 'backup',
          kind: 'backup_overdue',
          severity: 'overdue',
          title: TITLE,
          since: new Date(NOW - 5 * 60_000).toISOString(),
          action: { type: 'backup_now' },
        },
      ],
    }),
    error: null,
    loadedAt: NOW,
  };
  const reads = {
    status: {
      data: normalizeStatus({ state: 'normal', headline: 'Serving normally' }),
      error: null,
    },
    pools: {
      data: normalizePools({ pools: [{ provider: 'claude', totalAccounts: 2 }] }),
      error: null,
    },
    summary,
    failures: { data: [], error: null },
    anyRequest: { data: true, error: null },
    access: { data: null, error: null },
    updatedAt: NOW,
    checkedAt: NOW,
  };
  const Aside = ({ label, children }: { label: string; children: unknown }) =>
    createElement('aside', { 'aria-label': label }, children as never);
  /** As the shell's `PageAside` does, mounts its content a render after it mounts itself. */
  const LateAside = ({ label, children }: { label: string; children: unknown }) => {
    const [ready, setReady] = useState(false);
    useLayoutEffect(() => setReady(true), []);
    return ready ? createElement('aside', { 'aria-label': label }, children as never) : null;
  };
  const view = (
    railBeside: boolean,
    { aside = Aside, given = reads }: { aside?: typeof Aside; given?: typeof reads } = {}
  ) =>
    createElement(DashboardView, {
      timeWindow: '24h',
      setWindow: () => {},
      reads: given,
      attention,
      loggingToFile: true,
      railBeside,
      baseUrl: 'http://server:8317',
      hint: null,
      onRetry: () => {},
      onChanged: async () => {},
      Aside: aside,
    });
  const fixButton = (container: Element) =>
    [...container.querySelectorAll('button')].find((button) =>
      button.getAttribute('aria-label')?.endsWith(TITLE)
    ) as HTMLButtonElement;

  test('the focus stays on the same fix as the rail moves, both ways', () => {
    const page = mount();
    page.render(view(true));
    const beside = fixButton(page.container);
    expect(beside.closest('aside')).not.toBeNull();
    beside.focus();

    page.render(view(false));
    const inline = fixButton(page.container);
    expect(inline).not.toBe(beside);
    expect(inline.closest('aside')).toBeNull();
    expect(document.activeElement).toBe(inline);

    page.render(view(true));
    expect(document.activeElement).toBe(fixButton(page.container));
    expect((document.activeElement as Element).closest('aside')).not.toBeNull();
    page.unmount();
  });

  test('the focus stays on the same fix when the shell mounts the rail a render late', () => {
    const page = mount();
    page.render(view(false, { aside: LateAside }));
    fixButton(page.container).focus();
    page.render(view(true, { aside: LateAside }));
    expect((document.activeElement as Element).closest('aside')).not.toBeNull();
    expect(document.activeElement === fixButton(page.container)).toBe(true);
    page.unmount();
  });

  test("the focus moves to the visible verdict's See failures, both ways", () => {
    const degraded = {
      ...reads,
      status: {
        data: normalizeStatus({ state: 'degraded', headline: 'Claude is failing' }),
        error: null,
      },
    };
    // The stylesheet shows the page's verdict from the breakpoint up and the rail's below it.
    const inRail = (element: Element) => element.closest('[aria-label="Right now"]') !== null;
    const inVerdict = (element: Element) =>
      element.closest('section')?.querySelector('h2')?.textContent === 'Claude is failing';
    const wide = (element: Element) => inVerdict(element) && inRail(element);
    const narrow = (element: Element) => inVerdict(element) && !inRail(element);
    const seeFailures = (rail: boolean) =>
      [...page.container.querySelectorAll('a')].find(
        (link) => link.textContent === 'See failures' && inRail(link) === rail
      ) as HTMLAnchorElement;
    const page = mount();
    try {
      hidden = wide;
      page.render(view(true, { given: degraded }));
      seeFailures(false).focus();

      hidden = narrow;
      page.render(view(false, { given: degraded }));
      expect(document.activeElement === seeFailures(true)).toBe(true);

      hidden = wide;
      page.render(view(true, { given: degraded }));
      expect(document.activeElement === seeFailures(false)).toBe(true);
    } finally {
      hidden = () => false;
      page.unmount();
    }
  });

  test('a control the user left is not refocused when the layout changes', async () => {
    const page = mount();
    page.render(view(true));
    const fix = fixButton(page.container);
    fix.focus();
    act(() => fix.blur());
    // The user's next action is at least a task away.
    await Promise.resolve();
    page.render(view(false));
    expect(document.activeElement === document.body).toBe(true);
    page.unmount();
  });

  test("a fix's result stays as the rail moves", async () => {
    taprootApi.createBackup = async () => ({
      id: 'b1',
      createdAt: new Date(NOW).toISOString(),
      bytes: 2_400_000,
      reason: 'manual',
      files: 3,
    });
    const page = mount();
    page.render(view(false));
    await act(async () => {
      fixButton(page.container).click();
    });
    expect(page.container.textContent).toContain('Backed up (2.29 MB).');

    page.render(view(true));
    expect(page.container.textContent).toContain('Backed up (2.29 MB).');
    page.render(view(false));
    expect(page.container.textContent).toContain('Backed up (2.29 MB).');
    page.unmount();
  });
});

describe("the rail's reserved heights", () => {
  const KEY = 'taproot.dashboard.rail-heights';
  const loadingReads = {
    status: { data: null, error: null },
    pools: { data: null, error: null },
    summary: { data: null, error: null },
    failures: { data: null, error: null },
    anyRequest: { data: null, error: null },
    access: { data: null, error: null },
    updatedAt: null,
    checkedAt: null,
  };
  const railView = (reads: typeof loadingReads, attention: unknown) =>
    createElement(DashboardView, {
      timeWindow: '24h',
      setWindow: () => {},
      reads: reads as never,
      attention: attention as never,
      loggingToFile: true,
      railBeside: false,
      baseUrl: 'http://server:8317',
      hint: null,
      onRetry: () => {},
      onChanged: async () => {},
    });
  const section = (container: Element, title: string) =>
    [...container.querySelectorAll('section')].find(
      (element) => element.querySelector('h2')?.textContent === title
    ) as HTMLElement;

  test('each layout keeps its own heights, and unreadable storage reserves nothing', () => {
    expect(railLayout((query) => query === '(min-width: 1192px)')).toBe('beside');
    expect(railLayout((query) => query === '(max-width: 768px)')).toBe('phone');
    expect(railLayout(() => false)).toBe('inline');
    localStorage.removeItem(KEY);
    expect(reservedHeight(localStorage, 'inline', 'needs')).toBeNull();
    storeReservedHeight(localStorage, 'inline', 'needs', 490);
    storeReservedHeight(localStorage, 'phone', 'needs', 656);
    expect(reservedHeight(localStorage, 'inline', 'needs')).toBe(490);
    expect(reservedHeight(localStorage, 'phone', 'needs')).toBe(656);
    expect(reservedHeight(localStorage, 'inline', 'pools')).toBeNull();
    localStorage.setItem(KEY, '{not json');
    expect(reservedHeight(localStorage, 'inline', 'needs')).toBeNull();
    localStorage.setItem(KEY, JSON.stringify({ 'inline.needs': 'tall' }));
    expect(reservedHeight(localStorage, 'inline', 'needs')).toBeNull();
    localStorage.removeItem(KEY);
  });

  test('a loading section holds the height it last showed data at, and records the new one', () => {
    localStorage.setItem(KEY, JSON.stringify({ 'inline.needs': 490, 'inline.pools': 492 }));
    const page = mount();
    page.render(railView(loadingReads, { data: null, error: null, loadedAt: null }));
    expect(section(page.container, 'Needs you').style.minHeight).toBe('490px');
    expect(section(page.container, 'Pools').style.minHeight).toBe('492px');

    const realRect = window.Element.prototype.getBoundingClientRect;
    window.Element.prototype.getBoundingClientRect = function (this: Element) {
      const rect = realRect.call(this);
      return this.tagName === 'SECTION' ? { ...rect.toJSON(), height: 300 } : rect;
    } as typeof realRect;
    try {
      page.render(
        railView(
          {
            ...loadingReads,
            pools: {
              data: normalizePools({ pools: [{ provider: 'claude', totalAccounts: 2 }] }),
              error: null,
            },
          } as never,
          { data: normalizeAttention({ items: [] }), error: null, loadedAt: NOW }
        )
      );
    } finally {
      window.Element.prototype.getBoundingClientRect = realRect;
    }
    expect(section(page.container, 'Needs you').style.minHeight).toBe('');
    expect(section(page.container, 'Pools').style.minHeight).toBe('');
    expect(JSON.parse(localStorage.getItem(KEY) ?? '{}')).toEqual({
      'inline.needs': 300,
      'inline.pools': 300,
    });
    localStorage.removeItem(KEY);
    page.unmount();
  });

  test('a section that failed to load reserves nothing', () => {
    localStorage.setItem(KEY, JSON.stringify({ 'inline.needs': 490 }));
    const page = mount();
    page.render(railView(loadingReads, { data: null, error: 'pools down', loadedAt: null }));
    expect(section(page.container, 'Needs you').style.minHeight).toBe('');
    localStorage.removeItem(KEY);
    page.unmount();
  });
});
