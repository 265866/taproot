import { describe, expect, test } from 'bun:test';
import {
  createFullscreenScrollLock,
  shouldExitLogFullscreen,
} from '../src/features/taproot-logs/appLogs/model/logFullscreen';

describe('log fullscreen Escape handling', () => {
  test('an unhandled Escape exits fullscreen', () => {
    expect(shouldExitLogFullscreen({ key: 'Escape', defaultPrevented: false }, false)).toBe(true);
  });

  test('Escape consumed by a nested Select does not exit fullscreen', () => {
    expect(shouldExitLogFullscreen({ key: 'Escape', defaultPrevented: true }, false)).toBe(false);
    // After the Select closes, the next unhandled Escape can exit fullscreen.
    expect(shouldExitLogFullscreen({ key: 'Escape', defaultPrevented: false }, false)).toBe(true);
  });

  test('an open modal retains Escape priority', () => {
    for (const defaultPrevented of [false, true]) {
      expect(shouldExitLogFullscreen({ key: 'Escape', defaultPrevented }, true)).toBe(false);
    }
  });

  test('other keys never exit fullscreen', () => {
    for (const key of ['Enter', 'Tab', 'ArrowDown']) {
      expect(shouldExitLogFullscreen({ key, defaultPrevented: false }, false)).toBe(false);
    }
  });
});

describe('log fullscreen scroll lock', () => {
  const setup = () => {
    const events: string[] = [];
    const lock = createFullscreenScrollLock(
      () => events.push('lock'),
      ({ restore }) => events.push(restore ? 'unlock' : 'unlock-no-restore')
    );
    const apply = (next: boolean) => events.push(`fullscreen=${next}`);
    return { events, lock, apply };
  };

  test('entering locks before the state changes, so the lock saves the page scroll', () => {
    const { events, lock, apply } = setup();
    lock.set(true, apply);
    expect(events).toEqual(['lock', 'fullscreen=true']);
  });

  test('leaving does not lock, and the release after the state change unlocks once', () => {
    const { events, lock, apply } = setup();
    lock.set(true, apply);
    // The effect for the new state runs after the commit and must not lock a second time.
    lock.acquire();
    lock.set(false, apply);
    lock.release();
    lock.release();
    expect(events).toEqual(['lock', 'fullscreen=true', 'fullscreen=false', 'unlock']);
  });

  test('the effect takes the lock again after a cleanup released it while still fullscreen', () => {
    // StrictMode runs an effect's cleanup and then the effect again on mount.
    const { events, lock, apply } = setup();
    lock.set(true, apply);
    lock.release();
    lock.acquire();
    lock.release();
    expect(events).toEqual(['lock', 'fullscreen=true', 'unlock', 'lock', 'unlock']);
  });

  test('leaving the page while fullscreen unlocks without restoring the old scroll', () => {
    const { events, lock, apply } = setup();
    lock.set(true, apply);
    lock.release(false);
    lock.release(false);
    expect(events).toEqual(['lock', 'fullscreen=true', 'unlock-no-restore']);
  });
});
