/**
 * The pool chart is one named image. Recharts' surface renders only once the container has a
 * measured size, which static rendering never gives it, so this checks the hidden wrapper that
 * holds the surface rather than the surface itself.
 */

import { describe, expect, mock, test } from 'bun:test';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '@/i18n';

mock.module(join(import.meta.dir, '../src/features/taproot-quota/quota.module.scss'), () => ({
  default: new Proxy({}, { get: (_, key) => (typeof key === 'string' ? key : undefined) }),
}));

// The query string loads the real module: the Accounts page helper mocks this path for the run.
const { PoolChart } = await import('../src/features/taproot-quota/components/PoolChart.tsx?real');

describe('PoolChart accessibility', () => {
  test('exposes only the labelled outer image', () => {
    const now = Date.UTC(2026, 10, 10);
    const markup = renderToStaticMarkup(
      createElement(PoolChart, {
        rows: [],
        view: 'remaining',
        from: now - 7 * 86_400_000,
        to: now,
        now,
        yMax: 100,
        accent: 'red',
        ariaLabel: 'Claude pool remaining',
      })
    );

    expect(markup).toContain('role="img" aria-label="Claude pool remaining"');
    expect(markup).toMatch(/<div[^>]*class="recharts-responsive-container"[^>]*aria-hidden="true"/);
  });
});
