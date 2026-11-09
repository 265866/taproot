import { beforeAll, describe, expect, test } from 'bun:test';
import i18n from '@/i18n';
import { createTaprootClient, type Price } from '@/services/api/taproot';
import { resetPricesConfirmation } from '@/features/taproot-usage/logic/resetPrices';

const shipped: Price[] = [
  {
    modelPattern: 'gpt-6-sol',
    input: 2,
    output: 10,
    cacheRead: 0.2,
    cacheWrite5m: 2.5,
    cacheWrite1h: 5,
    source: 'OpenAI standard tier',
    updatedAt: '2026-11-08T00:00:00Z',
  },
];

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('reset to shipped prices', () => {
  test('the confirmation warns that the reset reprices all past usage', () => {
    const options = resetPricesConfirmation(i18n.t, {
      reset: async () => ({ prices: shipped }),
      onReset: () => undefined,
      onError: () => undefined,
    });
    expect(options.variant).toBe('danger');
    expect(options.title).toBe('Reset to shipped prices?');
    expect(options.confirmText).toBe('Reset prices');
    expect(String(options.message)).toContain('reprices all past usage');
    expect(String(options.message)).toContain('Rows you added or edited are lost');
  });

  test('confirming resets once and hands over the shipped rows', async () => {
    let calls = 0;
    let received: Price[] | null = null;
    const errors: string[] = [];
    const options = resetPricesConfirmation(i18n.t, {
      reset: async () => {
        calls += 1;
        return { prices: shipped };
      },
      onReset: (prices) => {
        received = prices;
      },
      onError: (message) => errors.push(message),
    });
    expect(calls).toBe(0);
    await options.onConfirm();
    expect(calls).toBe(1);
    expect(received).toEqual(shipped);
    expect(errors).toEqual([]);
  });

  test('a failed reset reports the error and keeps the table', async () => {
    let reset = false;
    const errors: string[] = [];
    const options = resetPricesConfirmation(i18n.t, {
      reset: async () => {
        throw new Error('internal error');
      },
      onReset: () => {
        reset = true;
      },
      onError: (message) => errors.push(message),
    });
    await options.onConfirm();
    expect(reset).toBe(false);
    expect(errors).toEqual(['internal error']);
  });

  test('the client posts to /prices/reset', async () => {
    const seen: Array<{ url: string; method: string | undefined }> = [];
    const client = createTaprootClient('/t', async (input, init) => {
      seen.push({ url: String(input), method: init?.method });
      return Response.json({ prices: shipped });
    });
    expect(await client.resetPrices()).toEqual({ prices: shipped });
    expect(seen).toEqual([{ url: '/t/prices/reset', method: 'POST' }]);
  });
});
