import { afterEach, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { enterScrollOffset } from '@/components/common/pageScrollContainer';
import { isRouterHistoryEntry, locationFromHash } from '@/hooks/useUnsavedChangesGuard';
import { ADMIN_REVOKED_EVENT, reportAccessDenial } from '@/services/api/accessEvents';
import { createTaprootClient } from '@/services/api/taproot';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

describe('typed-hash scroll', () => {
  const saved = new Map([
    ['default', 1269],
    ['k1', 400],
  ]);

  test('a page the router opened gets its saved offset back', () => {
    expect(enterScrollOffset(saved, 'k1')).toBe(400);
    expect(enterScrollOffset(saved, 'k2')).toBe(0);
  });

  test('a typed hash starts at the top instead of inheriting the last typed page', () => {
    expect(enterScrollOffset(saved, 'default')).toBe(0);
  });
});

describe('unsaved changes outside the router', () => {
  test('tells router entries from typed or edited hashes', () => {
    expect(isRouterHistoryEntry({ usr: null, key: 'abc', idx: 3 })).toBe(true);
    expect(isRouterHistoryEntry(null)).toBe(false);
    expect(isRouterHistoryEntry({})).toBe(false);
  });

  test('reads the destination of an edited hash', () => {
    expect(locationFromHash('#/usage?tab=pricing')).toMatchObject({
      pathname: '/usage',
      search: '?tab=pricing',
    });
    expect(locationFromHash('')).toMatchObject({ pathname: '/', search: '' });
  });

  test('guards reload and tab close while a draft is unsaved', () => {
    const source = read('src/hooks/useUnsavedChangesGuard.ts');
    expect(source).toContain("window.addEventListener('beforeunload', handleBeforeUnload)");
  });

  test('the edited-hash guard listens before the router, so it can hold the navigation', () => {
    // Window listeners run in the order they were added; the router adds its own on creation.
    const app = read('src/App.tsx');
    const install = app.indexOf('installEditedHashGuard(window);');
    expect(install).toBeGreaterThan(0);
    expect(install).toBeLessThan(app.indexOf('createHashRouter(['));
  });
});

describe('access lost mid-session', () => {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');
  const events: string[] = [];
  const installWindow = () =>
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: { dispatchEvent: (event: Event) => events.push(event.type) },
    });

  afterEach(() => {
    events.length = 0;
    if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow);
    else Reflect.deleteProperty(globalThis, 'window');
  });

  test('a 403 not_admin and a 401 each tell the connection store', () => {
    installWindow();
    reportAccessDenial(403, 'not_admin');
    reportAccessDenial(401, undefined);
    reportAccessDenial(403, 'forbidden');
    reportAccessDenial(500, 'not_admin');
    expect(events).toEqual([ADMIN_REVOKED_EVENT, 'unauthorized']);
  });

  test("taproot's own API reports a revoked Admin like the management API", async () => {
    installWindow();
    const client = createTaprootClient('/taproot/api/v1', async () =>
      Response.json({ error: { code: 'not_admin', message: 'no Admin' } }, { status: 403 })
    );
    await expect(client.accounts()).rejects.toMatchObject({ status: 403, code: 'not_admin' });
    expect(events).toEqual([ADMIN_REVOKED_EVENT]);
  });

  test('the connection store reconnects so the gate can ask for access', () => {
    const source = read('src/stores/useAuthStore.ts');
    expect(source).toContain('window.addEventListener(ADMIN_REVOKED_EVENT');
    expect(source).toContain('void useAuthStore.getState().connect();');
  });
});
