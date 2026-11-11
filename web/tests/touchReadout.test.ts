/**
 * A chart's readout on a touch screen: it goes away a while after the finger lifts, and at once
 * on a scroll or a touch elsewhere, while mouse use is left alone.
 */
import { afterAll, afterEach, beforeEach, describe, expect, jest, test } from 'bun:test';
import { GlobalRegistrator } from '@happy-dom/global-registrator';

GlobalRegistrator.register({ url: 'http://localhost/' });

const { act, createElement } = await import('react');
const { createRoot } = await import('react-dom/client');
const { useTouchReadout, TOUCH_READOUT_HOLD_MS } = await import('@/hooks/useTouchReadout');
const actGlobal = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
actGlobal.IS_REACT_ACT_ENVIRONMENT = true;

afterAll(async () => {
  delete actGlobal.IS_REACT_ACT_ENVIRONMENT;
  await GlobalRegistrator.unregister();
});

let hides = 0;

function Harness() {
  const [ref, isHidden] = useTouchReadout<HTMLDivElement>(() => {
    hides += 1;
  });
  return createElement(
    'div',
    null,
    createElement(
      'div',
      { id: 'chart', ref, 'data-hidden': String(isHidden) },
      createElement('span', { id: 'bar' })
    ),
    createElement('button', { id: 'elsewhere' })
  );
}

let unmount: () => void = () => {};
const chart = () => document.getElementById('chart')!;
const hidden = () => chart().dataset.hidden === 'true';
const fire = (target: EventTarget, type: string, init: Record<string, unknown> = {}) =>
  act(() => {
    const event = new Event(type, { bubbles: true });
    Object.assign(event, init);
    target.dispatchEvent(event);
  });
const tick = (ms: number) =>
  act(() => {
    jest.advanceTimersByTime(ms);
  });
const touch = (target: EventTarget = chart()) => {
  fire(target, 'touchstart');
  fire(target, 'touchmove');
  fire(target, 'touchend');
};

beforeEach(() => {
  jest.useFakeTimers();
  hides = 0;
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root = createRoot(host);
  act(() => root.render(createElement(Harness)));
  unmount = () => {
    unmount = () => {};
    act(() => root.unmount());
    host.remove();
  };
});

afterEach(() => {
  unmount();
  jest.useRealTimers();
});

describe('a chart readout on a touch screen', () => {
  test('hides once the hold passes after the finger lifts', () => {
    touch();
    tick(TOUCH_READOUT_HOLD_MS - 1);
    expect(hidden()).toBe(false);
    expect(hides).toBe(0);
    tick(1);
    expect(hidden()).toBe(true);
    expect(hides).toBe(1);
  });

  test('another touch before then starts the hold again', () => {
    touch();
    tick(TOUCH_READOUT_HOLD_MS - 500);
    fire(chart(), 'touchstart');
    tick(TOUCH_READOUT_HOLD_MS);
    expect(hidden()).toBe(false);
    fire(chart(), 'touchend');
    tick(TOUCH_READOUT_HOLD_MS - 1);
    expect(hidden()).toBe(false);
    tick(1);
    expect(hidden()).toBe(true);
    expect(hides).toBe(1);
  });

  test('a scroll hides it at once, and moves of the same touch keep it hidden', () => {
    fire(chart(), 'touchstart');
    fire(chart(), 'touchmove');
    fire(document.getElementById('bar')!, 'scroll');
    expect(hidden()).toBe(true);
    expect(hides).toBe(1);
    // A drag that scrolls the page goes on sending touchmove.
    fire(chart(), 'touchmove');
    fire(chart(), 'touchend');
    tick(TOUCH_READOUT_HOLD_MS * 2);
    expect(hidden()).toBe(true);
    expect(hides).toBe(1);
  });

  test('a touch elsewhere hides it at once; one on the chart does not', () => {
    touch();
    fire(document.getElementById('bar')!, 'pointerdown');
    expect(hidden()).toBe(false);
    fire(document.getElementById('elsewhere')!, 'pointerdown');
    expect(hidden()).toBe(true);
    expect(hides).toBe(1);
  });

  test('the next touch shows it again only when the chart moves its readout', () => {
    touch();
    tick(TOUCH_READOUT_HOLD_MS);
    expect(hidden()).toBe(true);
    fire(chart(), 'touchstart');
    expect(hidden()).toBe(true);
    // A tap's emulated mousemove, or a drag's touchmove.
    fire(chart(), 'mousemove');
    expect(hidden()).toBe(false);
  });
});

describe('mouse use', () => {
  test('hover, scrolls, and clicks elsewhere never hide it', () => {
    fire(chart(), 'pointermove', { pointerType: 'mouse' });
    fire(chart(), 'mousemove');
    fire(document, 'scroll');
    fire(document.getElementById('elsewhere')!, 'pointerdown');
    tick(TOUCH_READOUT_HOLD_MS * 2);
    expect(hidden()).toBe(false);
    expect(hides).toBe(0);
  });

  test('a mouse brings it back after a touch hid it', () => {
    touch();
    fire(document, 'scroll');
    expect(hidden()).toBe(true);
    fire(chart(), 'mousemove');
    expect(hidden()).toBe(true);
    fire(chart(), 'pointermove', { pointerType: 'touch' });
    fire(chart(), 'mousemove');
    expect(hidden()).toBe(true);
    fire(chart(), 'pointermove', { pointerType: 'mouse' });
    fire(chart(), 'mousemove');
    expect(hidden()).toBe(false);
  });

  test('hides when the chart drops the node the touch began on', () => {
    const bar = document.getElementById('bar')!;
    fire(bar, 'touchstart');
    fire(bar, 'touchmove');
    act(() => bar.remove());
    fire(bar, 'touchend');
    tick(TOUCH_READOUT_HOLD_MS);
    expect(hidden()).toBe(true);
    expect(hides).toBe(1);
  });

  test('a cancelled touch starts the hold too', () => {
    fire(chart(), 'touchstart');
    fire(chart(), 'touchmove');
    fire(chart(), 'touchcancel');
    tick(TOUCH_READOUT_HOLD_MS);
    expect(hidden()).toBe(true);
  });

  test('lifting one of two fingers keeps it while the other drags', () => {
    fire(chart(), 'touchstart');
    fire(chart(), 'touchstart');
    fire(chart(), 'touchend', { touches: [{}] });
    for (let i = 0; i < 7; i += 1) {
      tick(500);
      fire(chart(), 'touchmove');
    }
    expect(hidden()).toBe(false);
    fire(chart(), 'touchend', { touches: [] });
    tick(TOUCH_READOUT_HOLD_MS);
    expect(hidden()).toBe(true);
  });

  test('nothing fires after the chart unmounts', () => {
    touch();
    unmount();
    tick(TOUCH_READOUT_HOLD_MS * 2);
    expect(hides).toBe(0);
  });

  test('a pen brings it back after a touch hid it', () => {
    touch();
    tick(TOUCH_READOUT_HOLD_MS);
    expect(hidden()).toBe(true);
    fire(chart(), 'pointermove', { pointerType: 'pen' });
    fire(chart(), 'mousemove');
    expect(hidden()).toBe(false);
  });
});
