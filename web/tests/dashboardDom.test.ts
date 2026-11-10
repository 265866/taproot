/**
 * Drives the Dashboard's failure rows and rail in a DOM (happy-dom). happy-dom lays nothing out, so
 * the test stands in for layout: a one-line cell is cut while its text is longer than `CUT_AT`
 * characters and its row is closed, and a stubbed ResizeObserver reports a size whenever the test
 * says the browser would have laid out again.
 */
import { afterAll, describe, expect, test } from 'bun:test';
import { GlobalRegistrator } from '@happy-dom/global-registrator';

GlobalRegistrator.register({ url: 'http://localhost/' });

const { act, createElement } = await import('react');
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

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const CUT_AT = 30;
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
    return !openRow(this) && (this.textContent?.length ?? 0) > CUT_AT ? 200 : 100;
  },
});
window.Element.prototype.getClientRects = function (this: Element) {
  return [this.getBoundingClientRect()] as unknown as DOMRectList;
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

const failures = (over: Record<string, unknown>) => ({
  data: normalizeClientRequests({
    requests: [
      {
        ts: new Date(NOW - 5 * 60_000).toISOString(),
        deviceName: 'laptop',
        model: 'claude-opus-5-5',
        status: 529,
        failed: true,
        errorMessage: LONG_ERROR,
        requestId: '',
        ...over,
      },
    ],
  }),
  error: null,
});

const renderFailures = (over: Record<string, unknown>) => {
  const page = mount();
  page.render(
    createElement(RecentFailures, {
      failures: failures(over),
      summary,
      timeWindow: '24h',
      nowMs: NOW,
      onRetry: () => {},
    })
  );
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

  test('a row whose cells all fit offers nothing to expand', () => {
    const page = renderFailures({ errorMessage: 'status 529' });
    expect(page.container.querySelector('button[aria-expanded]')).toBeNull();
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
  const view = (railBeside: boolean) =>
    createElement(DashboardView, {
      timeWindow: '24h',
      setWindow: () => {},
      reads,
      attention,
      loggingToFile: true,
      railBeside,
      baseUrl: 'http://server:8317',
      hint: null,
      onRetry: () => {},
      onChanged: async () => {},
      Aside,
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
