/**
 * Drives the option primitives in a DOM (happy-dom): the phone bottom sheet (a modal dialog that
 * traps Tab, closes on Escape, Done, or the backdrop, and gives focus back), the desktop choice
 * menu and popover, the radio list, and the Dashboard's compact Needs-you rows. The sheet leaves
 * history alone, so the system back gesture leaves the page as with every other modal.
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
const { useKeepFocus } = await import('@/features/dashboard/hooks/useKeepFocus');
const { useUsageParams } = await import('@/features/taproot-usage/hooks/useUsageParams');
const { Select } = await import('@/components/ui/Select');
const { useEffect, useRef } = await import('react');

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

describe('the bottom sheet and history', () => {
  const opener = () => document.getElementById('opener')!;
  const closers = [
    () =>
      click(Array.from(sheet()!.querySelectorAll('button')).find((b) => b.textContent === 'Done')!),
    () => key(document.activeElement ?? document.body, 'Escape'),
    () =>
      act(() => {
        sheet()!.parentElement!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
      }),
  ];

  test('opening and closing it adds no history entry and no popstate listener', () => {
    window.history.replaceState({ idx: 0 }, '', '/#/accounts');
    const length = window.history.length;
    const state = window.history.state;
    const addListener = jest.spyOn(window, 'addEventListener');
    const push = jest.spyOn(window.history, 'pushState');
    const replace = jest.spyOn(window.history, 'replaceState');
    const back = jest.spyOn(window.history, 'back');
    try {
      const view = mount(createElement(SheetHarness));
      for (const close of closers) {
        click(opener());
        expect(sheet()).not.toBeNull();
        expect(window.history.length).toBe(length);
        close();
      }
      view.unmount();
      expect(addListener.mock.calls.filter(([type]) => type === 'popstate')).toEqual([]);
      expect(push).not.toHaveBeenCalled();
      expect(replace).not.toHaveBeenCalled();
      expect(back).not.toHaveBeenCalled();
      expect(window.history.length).toBe(length);
      expect(window.history.state).toEqual(state);
    } finally {
      addListener.mockRestore();
      push.mockRestore();
      replace.mockRestore();
      back.mockRestore();
    }
  });

  test('a sheet that unmounts while open, as when back leaves its page, releases the scroll lock', () => {
    const view = mount(createElement(SheetHarness));
    click(opener());
    expect(document.body.style.position).toBe('fixed');
    expect(document.body.classList.contains('modal-open')).toBe(true);
    view.unmount();
    expect(document.body.style.position).toBe('');
    expect(document.body.style.overflow).toBe('');
    expect(document.body.classList.contains('modal-open')).toBe(false);
    expect(sheet()).toBeNull();
  });

  test('the Accounts View sheet does not reopen after the phone turns away and back', () => {
    const control = { setPhone: (_phone: boolean) => {} };
    function Turning() {
      const [phone, set] = useState(true);
      useEffect(() => {
        control.setPhone = set;
      }, [set]);
      return createElement(ViewControls, {
        phone,
        metricBeside: true,
        view: 'remaining',
        burnHours: 3,
        future: 'same',
        range: defaultRange(Date.parse('2026-11-10T12:00:00Z')),
        clock: Date.parse('2026-11-10T12:00:00Z'),
        onViewChange: () => {},
        onBurnHoursChange: () => {},
        onFutureChange: () => {},
        onRangeChange: () => {},
        providers: null,
        onProviderChange: () => {},
      });
    }
    const view = mount(createElement(Turning));
    click(view.host.querySelector('button[aria-haspopup="dialog"]'));
    expect(sheet()).not.toBeNull();
    act(() => control.setPhone(false));
    act(() => control.setPhone(true));
    expect(
      view.host.querySelector('button[aria-haspopup="dialog"]')!.getAttribute('aria-expanded')
    ).toBe('false');
    view.unmount();
  });
});

describe('the compact Needs-you rows as items come and go', () => {
  const item = (id: string, title: string) => ({
    id,
    kind: 'backup_overdue',
    severity: 'overdue',
    title,
    detail: `${title} detail`,
    action: { type: 'backup_now' },
  });
  test('the row first shown at the top opens; when it leaves, the next does not open on its own', () => {
    const control = { setItems: (_items: ReturnType<typeof item>[]) => {} };
    function Changing() {
      const [items, set] = useState([item('a', 'First'), item('b', 'Second')]);
      useEffect(() => {
        control.setItems = set;
      }, [set]);
      return createElement(NeedsYou, {
        attention: { data: normalizeAttention({ items }), error: null },
        firstRun: false,
        loggingToFile: true,
        nowMs: 0,
        onRetry: () => {},
        fixes: { runs: {}, run: async () => {} },
        compact: true,
      });
    }
    const view = mount(createElement(Changing));
    const states = () =>
      Array.from(view.host.querySelectorAll('button[aria-expanded]')).map((b) =>
        b.getAttribute('aria-expanded')
      );
    expect(states()).toEqual(['true', 'false']);
    act(() => control.setItems([item('b', 'Second')]));
    expect(states()).toEqual(['false']);
    view.unmount();
  });
});

describe('keeping the keyboard on its control when the Digest layout switches', () => {
  test('a fix button remounted in the compact row takes the focus back', () => {
    const control = { setCompact: (_compact: boolean) => {} };
    function Page() {
      const [compact, set] = useState(false);
      useEffect(() => {
        control.setCompact = set;
      }, [set]);
      const page = useRef<HTMLDivElement>(null);
      const rail = useKeepFocus(page, compact);
      const fix = createElement(
        'button',
        { 'aria-label': 'Back up now: Backup' },
        compact ? 'Back up' : 'Back up now'
      );
      return createElement(
        'div',
        { ref: page },
        createElement(
          'div',
          { ref: rail },
          compact ? createElement('span', null, fix) : createElement('p', null, fix)
        )
      );
    }
    const view = mount(createElement(Page));
    const before = view.host.querySelector('button')!;
    before.focus();
    act(() => control.setCompact(true));
    const after = view.host.querySelector('button')!;
    expect(after).not.toBe(before);
    expect(document.activeElement).toBe(after);
    view.unmount();
  });
});

describe("Usage's page state setter", () => {
  test('stays the same function as the URL changes, so memoized views skip a re-render', () => {
    const seen: unknown[] = [];
    const renders = { count: 0 };
    const control = { update: (_patch: { group: 'account' }) => {} };
    function Probe() {
      const [, set] = useUsageParams();
      useEffect(() => {
        renders.count += 1;
      });
      useEffect(() => {
        seen.push(set);
        control.update = set;
      }, [set]);
      return null;
    }
    const view = mount(createElement(Probe));
    act(() => control.update({ group: 'account' }));
    expect(renders.count).toBeGreaterThan(1);
    expect(seen).toHaveLength(1);
    view.unmount();
  });
});

describe('the select listbox', () => {
  test('is at least as wide as its longest option, within the viewport', () => {
    const scrollWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollWidth');
    Object.defineProperty(HTMLElement.prototype, 'scrollWidth', {
      configurable: true,
      get(this: HTMLElement) {
        return this.getAttribute('role') === 'option' ? 140 : 0;
      },
    });
    try {
      const view = mount(
        createElement(Select, {
          value: 'default',
          onChange: () => {},
          ariaLabel: 'Sort',
          options: [
            { value: 'default', label: 'Default' },
            { value: 'least', label: 'Least remaining' },
          ],
        })
      );
      click(view.host.querySelector('button[aria-haspopup="listbox"]'));
      const list = document.querySelector<HTMLElement>('[role="listbox"]')!;
      expect(parseFloat(list.style.width)).toBeGreaterThanOrEqual(140);
      expect(parseFloat(list.style.width)).toBeLessThanOrEqual(window.innerWidth - 16);
      view.unmount();
    } finally {
      if (scrollWidth) Object.defineProperty(HTMLElement.prototype, 'scrollWidth', scrollWidth);
      else delete (HTMLElement.prototype as { scrollWidth?: number }).scrollWidth;
    }
  });
});
