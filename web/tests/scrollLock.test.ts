import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { lockScroll, unlockScroll } from '@/components/ui/scrollLock';

const element = () => ({
  classList: { add() {}, remove() {} },
  style: {} as Record<string, string>,
});

describe('scroll lock restore', () => {
  let scrolls: number[] = [];
  const saved = {
    document: globalThis.document,
    window: globalThis.window,
    HTMLElement: globalThis.HTMLElement,
  };

  beforeEach(() => {
    scrolls = [];
    Object.assign(globalThis, {
      HTMLElement: class {},
      document: {
        body: element(),
        documentElement: { ...element(), scrollTop: 0 },
        querySelector: () => null,
      },
      window: {
        scrollY: 150,
        pageYOffset: 150,
        scrollTo: ({ top }: { top: number }) => scrolls.push(top),
      },
    });
  });

  afterEach(() => {
    Object.assign(globalThis, saved);
  });

  test('the last unlock restores the saved scroll', () => {
    lockScroll();
    lockScroll();
    unlockScroll();
    unlockScroll();
    expect(scrolls).toEqual([150]);
  });

  test('an unlock that asks not to restore wins over a later nested unlock', () => {
    // App Logs leaves while fullscreen with a modal open: the panel releases first, the modal last.
    lockScroll();
    lockScroll();
    unlockScroll({ restore: false });
    unlockScroll();
    expect(scrolls).toEqual([]);
  });

  test('the next lock and unlock restore again', () => {
    lockScroll();
    unlockScroll({ restore: false });
    lockScroll();
    unlockScroll();
    expect(scrolls).toEqual([150]);
  });
});
