/**
 * Drives the option primitives in a DOM (happy-dom): the phone bottom sheet (a modal dialog that
 * traps Tab, closes on Escape, Done, or the backdrop, and gives focus back), the desktop choice
 * menu and popover, the radio list, and the Dashboard's compact Needs-you rows.
 */
import { afterAll, afterEach, describe, expect, jest, test } from 'bun:test';
import { GlobalRegistrator } from '@happy-dom/global-registrator';

GlobalRegistrator.register({ url: 'http://localhost/' });

const { act, createElement, useState } = await import('react');
const { createRoot } = await import('react-dom/client');
const { MemoryRouter } = await import('react-router-dom');
await import('@/i18n');
const { BottomSheet, SheetButton, SheetGroup } = await import('@/components/ui/BottomSheet');
const { ChoiceMenu, PopoverButton, PopoverGroup } = await import('@/components/ui/Dropdown');
const { RadioList } = await import('@/components/ui/RadioList');
const { SegmentedControl } = await import('@/components/ui/SegmentedControl');
const { normalizeAttention } = await import('@/services/api/taprootStatus');
const { NeedsYou } = await import('@/features/dashboard/components/NeedsYou');
const { ViewControls } = await import('@/features/accounts/components/ViewControls');
const { defaultRange } = await import('@/features/taproot-quota/model/range');

const actGlobal = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
actGlobal.IS_REACT_ACT_ENVIRONMENT = true;
afterAll(() => {
  delete actGlobal.IS_REACT_ACT_ENVIRONMENT;
  GlobalRegistrator.unregister();
});

const mounted = new Set<() => void>();
afterEach(() => {
  for (const unmount of [...mounted]) unmount();
});

const mount = (element: ReturnType<typeof createElement>) => {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root = createRoot(host);
  act(() => root.render(createElement(MemoryRouter, null, element)));
  const unmount = () => {
    if (!mounted.delete(unmount)) return;
    act(() => root.unmount());
    host.remove();
  };
  mounted.add(unmount);
  return { host, unmount };
};

const key = (target: EventTarget, name: string, shiftKey = false) => {
  const event = new KeyboardEvent('keydown', {
    key: name,
    shiftKey,
    bubbles: true,
    cancelable: true,
  });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
};
/** What a browser does with Tab: the sheet's handler may take it; otherwise focus moves on. */
const tab = (shiftKey = false) => {
  const event = key(document.activeElement ?? document.body, 'Tab', shiftKey);
  if (event.defaultPrevented) return;
  const order = Array.from(
    document.querySelectorAll<HTMLElement>('button:not([disabled]), [tabindex="0"]')
  ).filter((el) => el.tabIndex >= 0);
  const active = document.activeElement as HTMLElement;
  const at = order.indexOf(active);
  if (at >= 0) {
    order[(at + (shiftKey ? -1 : 1) + order.length) % order.length]?.focus();
    return;
  }
  // From a container that is not in the order (a dialog), the next element after it in the page.
  const after = (el: HTMLElement) =>
    (active.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
  (shiftKey ? order.filter((el) => !after(el)).pop() : order.find(after))?.focus();
};
const click = (el: Element | null) =>
  act(() => {
    (el as HTMLElement).click();
  });
const sheet = () => document.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');

function SheetHarness() {
  const [open, setOpen] = useState(false);
  const [hours, setHours] = useState('3');
  return createElement(
    'div',
    null,
    createElement('button', { id: 'before' }, 'Before'),
    createElement(
      SheetButton,
      { id: 'opener', expanded: open, onClick: () => setOpen(true) },
      'View'
    ),
    createElement(
      BottomSheet,
      { open, onClose: () => setOpen(false), title: 'View' },
      createElement(
        SheetGroup,
        { label: 'Burn rate over', labelId: 'burn' },
        createElement(SegmentedControl, {
          ariaLabelledBy: 'burn',
          size: 'large',
          value: hours,
          onChange: setHours,
          options: ['1', '3', '6'].map((h) => ({ value: h, label: `${h}h` })),
        })
      ),
      createElement('button', { id: 'last' }, 'Last')
    )
  );
}

describe('the bottom sheet', () => {
  test('opens as a labelled modal dialog with focus inside, and its handle is only decoration', () => {
    const view = mount(createElement(SheetHarness));
    const opener = document.getElementById('opener')!;
    opener.focus();
    click(opener);
    const dialog = sheet()!;
    expect(dialog).not.toBeNull();
    expect(opener.getAttribute('aria-expanded')).toBe('true');
    expect(opener.getAttribute('aria-haspopup')).toBe('dialog');
    expect(document.getElementById(dialog.getAttribute('aria-labelledby')!)?.textContent).toBe(
      'View'
    );
    expect(dialog.contains(document.activeElement)).toBe(true);
    const handle = dialog.firstElementChild as HTMLElement;
    expect(handle.getAttribute('aria-hidden')).toBe('true');
    expect(handle.tabIndex).toBe(-1);
    expect(handle.children.length).toBe(0);
    view.unmount();
  });

  test('Tab and Shift+Tab stay inside it', () => {
    const view = mount(createElement(SheetHarness));
    click(document.getElementById('opener'));
    for (let i = 0; i < 8; i++) {
      tab();
      expect(sheet()!.contains(document.activeElement)).toBe(true);
    }
    for (let i = 0; i < 8; i++) {
      tab(true);
      expect(sheet()!.contains(document.activeElement)).toBe(true);
    }
    view.unmount();
  });

  test('Escape, Done, and the backdrop close it, and focus returns to the button that opened it', () => {
    jest.useFakeTimers();
    try {
      const view = mount(createElement(SheetHarness));
      const opener = document.getElementById('opener')!;
      const closeWith = (how: () => void) => {
        opener.focus();
        click(opener);
        expect(sheet()).not.toBeNull();
        how();
        act(() => {
          jest.advanceTimersByTime(250);
        });
        expect(sheet()).toBeNull();
        expect(document.activeElement).toBe(opener);
        expect(opener.getAttribute('aria-expanded')).toBe('false');
      };
      closeWith(() => key(document.activeElement ?? document.body, 'Escape'));
      closeWith(() =>
        click(
          Array.from(sheet()!.querySelectorAll('button')).find((b) => b.textContent === 'Done')!
        )
      );
      closeWith(() =>
        act(() => {
          sheet()!.parentElement!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
        })
      );
      view.unmount();
    } finally {
      jest.useRealTimers();
    }
  });

  test('a choice inside it applies at once', () => {
    const view = mount(createElement(SheetHarness));
    click(document.getElementById('opener'));
    const six = Array.from(sheet()!.querySelectorAll('[role="radio"]')).find(
      (b) => b.textContent === '6h'
    )!;
    click(six);
    expect(six.getAttribute('aria-checked')).toBe('true');
    view.unmount();
  });
});

describe('the choice menu', () => {
  function MenuHarness() {
    const [value, setValue] = useState('model');
    return createElement(ChoiceMenu<string>, {
      label: 'Stack by',
      ariaLabel: 'Stack the chart by',
      value,
      onChange: setValue,
      options: ['model', 'account', 'provider'].map((v) => ({ value: v, label: v })),
    });
  }

  test('opens on the chosen item, the arrows move, a choice closes it and focus returns', () => {
    const view = mount(createElement(MenuHarness));
    const trigger = view.host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!;
    expect(trigger.getAttribute('aria-label')).toBe('Stack the chart by: model');
    click(trigger);
    const menu = view.host.querySelector('[role="menu"]')!;
    expect(document.activeElement?.textContent).toBe('model');
    key(menu, 'ArrowDown');
    expect(document.activeElement?.textContent).toBe('account');
    click(document.activeElement);
    expect(view.host.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(trigger.getAttribute('aria-label')).toBe('Stack the chart by: account');
    view.unmount();
  });

  test('Escape closes it without choosing', () => {
    const view = mount(createElement(MenuHarness));
    const trigger = view.host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!;
    click(trigger);
    key(document.activeElement!, 'Escape');
    expect(view.host.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(trigger.getAttribute('aria-label')).toBe('Stack the chart by: model');
    view.unmount();
  });
});

describe('the projection popover and its radio list', () => {
  function PopoverHarness() {
    const [future, setFuture] = useState('same');
    return createElement(
      PopoverButton,
      { ariaLabel: 'Projection', label: 'Projection', value: future },
      createElement(
        PopoverGroup,
        { label: 'Future usage', labelId: 'future' },
        createElement(RadioList<string>, {
          variant: 'popover',
          ariaLabelledBy: 'future',
          value: future,
          onChange: setFuture,
          options: ['same', 'heavy', 'light'].map((v) => ({ value: v, label: v })),
        })
      )
    );
  }

  test('opens on the chosen option; arrows choose; Escape closes it and returns focus', () => {
    const view = mount(createElement(PopoverHarness));
    const trigger = view.host.querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"]')!;
    click(trigger);
    expect(view.host.querySelector('[role="dialog"]')?.getAttribute('aria-label')).toBe(
      'Projection'
    );
    expect(document.activeElement?.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement?.textContent).toBe('same');
    key(document.activeElement!, 'ArrowDown');
    expect(document.activeElement?.textContent).toBe('heavy');
    expect(document.activeElement?.getAttribute('aria-checked')).toBe('true');
    expect(trigger.textContent).toContain('heavy');
    key(document.activeElement!, 'Escape');
    expect(view.host.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    view.unmount();
  });
});

describe("the Accounts View sheet's groups", () => {
  const props = (metricBeside: boolean) => ({
    phone: true,
    metricBeside,
    view: 'remaining' as const,
    burnHours: 3 as const,
    future: 'same' as const,
    range: defaultRange(Date.parse('2026-11-10T12:00:00Z')),
    clock: Date.parse('2026-11-10T12:00:00Z'),
    onViewChange: () => {},
    onBurnHoursChange: () => {},
    onFutureChange: () => {},
    onRangeChange: () => {},
    providers: null,
    onProviderChange: () => {},
  });
  const groups = () =>
    Array.from(sheet()!.querySelectorAll('[class*="groupHead"] > span:first-child, [id]'))
      .map((el) => el.textContent)
      .filter(Boolean);

  test('where the metric fits beside View, the sheet holds burn, future usage, and dates', () => {
    const view = mount(createElement(ViewControls, props(true)));
    click(view.host.querySelector('button[aria-haspopup="dialog"]'));
    const text = sheet()!.textContent ?? '';
    expect(text).toContain('Burn rate over');
    expect(text).toContain('Future usage');
    expect(text).toContain('Dates');
    expect(text).not.toContain('Chart shows');
    // R4: the sidebar switch is the only Show emails control.
    expect(text).not.toContain('Show emails');
    expect(groups().length).toBeGreaterThan(0);
    view.unmount();
  });

  test('at 320 px the metric moves into the sheet', () => {
    const view = mount(createElement(ViewControls, props(false)));
    expect(view.host.querySelector('[role="radiogroup"]')).toBeNull();
    click(view.host.querySelector('button[aria-haspopup="dialog"]'));
    expect(sheet()!.textContent).toContain('Chart shows');
    expect(sheet()!.textContent).not.toContain('Show emails');
    view.unmount();
  });
});

describe('the compact Needs-you rows', () => {
  const attention = {
    data: normalizeAttention({
      items: [
        {
          id: 'a',
          kind: 'account_signed_out',
          severity: 'blocking',
          title: 'Codex: an account is signed out',
          detail: 'Refresh token rejected.',
          subject: { type: 'account', id: 'x' },
          action: { type: 'sign_in', target: 'codex' },
        },
        {
          id: 'b',
          kind: 'backup_overdue',
          severity: 'overdue',
          title: 'Last backup was 31 h ago',
          detail: 'The scheduled run failed.',
          action: { type: 'backup_now' },
        },
        {
          id: 'c',
          kind: 'device_waiting',
          severity: 'waiting',
          title: 'A device asks for access',
          action: { type: 'review_access' },
        },
      ],
    }),
    error: null,
  };
  const render = () =>
    mount(
      createElement(NeedsYou, {
        attention,
        firstRun: false,
        loggingToFile: true,
        nowMs: 0,
        onRetry: () => {},
        fixes: { runs: {}, run: async () => {} },
        compact: true,
      })
    );
  const expanders = (host: HTMLElement) =>
    Array.from(host.querySelectorAll<HTMLButtonElement>('button[aria-expanded]'));
  const detailOf = (button: HTMLButtonElement) =>
    document.getElementById(button.getAttribute('aria-controls')!)!;

  test('the first opens on its own, the rest stay closed, and a row without detail is no button', () => {
    const view = render();
    const [first, second] = expanders(view.host);
    expect(expanders(view.host)).toHaveLength(2);
    expect(first.getAttribute('aria-expanded')).toBe('true');
    expect(detailOf(first).hidden).toBe(false);
    expect(detailOf(first).textContent).toContain('Refresh token rejected.');
    expect(second.getAttribute('aria-expanded')).toBe('false');
    expect(detailOf(second).hidden).toBe(true);
    view.unmount();
  });

  test('a tap opens and closes a row in place, keeping focus on it', () => {
    const view = render();
    const [first, second] = expanders(view.host);
    second.focus();
    click(second);
    expect(second.getAttribute('aria-expanded')).toBe('true');
    expect(detailOf(second).hidden).toBe(false);
    expect(document.activeElement).toBe(second);
    click(first);
    expect(first.getAttribute('aria-expanded')).toBe('false');
    expect(detailOf(first).hidden).toBe(true);
    view.unmount();
  });

  test('fixes show the short label and keep the full one in their name', () => {
    const view = render();
    const signIn = Array.from(view.host.querySelectorAll('a')).find(
      (a) => a.textContent === 'Sign in'
    )!;
    expect(signIn.getAttribute('aria-label')).toBe(
      'Sign in again: Codex: an account is signed out'
    );
    const backUp = Array.from(view.host.querySelectorAll('button')).find(
      (b) => b.textContent === 'Back up'
    )!;
    expect(backUp.getAttribute('aria-label')).toBe('Back up now: Last backup was 31 h ago');
    view.unmount();
  });
});
