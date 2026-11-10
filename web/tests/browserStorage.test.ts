import { afterEach, describe, expect, test } from 'bun:test';
import { readStoredWindow } from '@/features/dashboard/model/window';
import { browserStorage } from '@/utils/browserStorage';

const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');

afterEach(() => {
  if (original) Object.defineProperty(globalThis, 'localStorage', original);
  else delete (globalThis as { localStorage?: Storage }).localStorage;
});

describe('browserStorage', () => {
  test('is undefined when the browser blocks site data and reading localStorage throws', () => {
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        throw new DOMException('The operation is insecure.', 'SecurityError');
      },
    });
    expect(browserStorage()).toBeUndefined();
    expect(readStoredWindow(browserStorage())).toBe('24h');
  });

  test('returns the storage when it is available', () => {
    const storage = { getItem: () => '7d' } as unknown as Storage;
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage });
    expect(browserStorage()).toBe(storage);
    expect(readStoredWindow(browserStorage())).toBe('7d');
  });
});
