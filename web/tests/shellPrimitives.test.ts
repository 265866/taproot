import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import '@/i18n';
import { AutocompleteInput } from '@/components/ui/AutocompleteInput';
import { Input } from '@/components/ui/Input';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { PageErrorBoundary, PageErrorView } from '@/components/common/PageErrorBoundary';
import { hasOpenLayer, popLayer, pushLayer } from '@/components/ui/layerStack';
import { formatFileSize } from '@/utils/format';

const read = (path: string) => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

describe('formatFileSize', () => {
  test('prints whole bytes and binary units', () => {
    expect(formatFileSize(0)).toBe('0 B');
    expect(formatFileSize(1023)).toBe('1023 B');
    expect(formatFileSize(1536)).toBe('1.50 KB');
    expect(formatFileSize(5 * 1024 ** 2)).toBe('5.00 MB');
  });

  test('never prints an undefined unit', () => {
    expect(formatFileSize(0.5)).toBe('1 B');
    expect(formatFileSize(2 * 1024 ** 4)).toBe('2.00 TB');
    expect(formatFileSize(3 * 1024 ** 6)).toBe('3072.00 PB');
  });

  test('a negative or non-finite size is unknown', () => {
    for (const value of [-10, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(formatFileSize(value)).toBe('—');
    }
  });
});

describe('Input', () => {
  test("a caller's aria-describedby joins the error and hint instead of replacing them", () => {
    const html = renderToStaticMarkup(
      createElement(Input, {
        id: 'name',
        hint: 'Shown on the card',
        error: 'Required',
        'aria-describedby': 'extra',
      })
    );
    expect(html).toContain('aria-describedby="extra name-error name-hint"');
    expect(html).toContain('aria-invalid="true"');
  });
});

describe('AutocompleteInput', () => {
  test('is a combobox that names its list and describes its hint and error', () => {
    const html = renderToStaticMarkup(
      createElement(AutocompleteInput, {
        id: 'model',
        label: 'Model',
        value: '',
        onChange: () => undefined,
        options: ['gpt-5'],
        hint: 'Pick one',
        error: 'Unknown model',
      })
    );
    expect(html).toContain('role="combobox"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain('aria-controls="model-listbox"');
    expect(html).toContain('aria-autocomplete="list"');
    expect(html).toContain('aria-describedby="model-error model-hint"');
    expect(html).toContain('<label for="model">');
  });
});

describe('LoadingSpinner', () => {
  test('announces that something is loading', () => {
    expect(renderToStaticMarkup(createElement(LoadingSpinner))).toContain('Loading...');
  });
});

describe('page error boundary', () => {
  // Server rendering has no error boundaries, so the lifecycle is checked directly.
  test('catches a page error and clears it when the page changes', () => {
    const error = new Error('Failed to fetch dynamically imported module');
    const caught = { ...PageErrorBoundary.getDerivedStateFromError(error), resetKey: '/system' };
    expect(caught.error).toBe(error);
    expect(
      PageErrorBoundary.getDerivedStateFromProps({ resetKey: '/system', children: null }, caught)
    ).toBeNull();
    expect(
      PageErrorBoundary.getDerivedStateFromProps({ resetKey: '/usage', children: null }, caught)
    ).toEqual({ error: null, resetKey: '/usage' });
  });

  test('the recovery card names the failure and offers a reload', () => {
    const html = renderToStaticMarkup(
      createElement(PageErrorView, {
        error: new Error('Failed to fetch dynamically imported module'),
      })
    );
    expect(html).toContain('role="alert"');
    expect(html).toContain('This page failed to load');
    expect(html).toContain('Failed to fetch dynamically imported module');
    expect(html).toContain('Reload');
  });

  test('the recovery card leaves out an empty reason', () => {
    const html = renderToStaticMarkup(createElement(PageErrorView, { error: {} }));
    expect(html).not.toContain('connection-gate-hint');
  });

  test('the router and the page area both use it', () => {
    expect(read('src/App.tsx')).toContain('errorElement: <AppErrorScreen />');
    expect(read('src/router/MainRoutes.tsx')).toContain('<PageErrorBoundary resetKey=');
    expect(read('src/router/MainRoutes.tsx')).not.toContain('fallback={null}');
  });
});

describe('layer stack', () => {
  test('reports open layers, for shortcuts that must stay behind them', () => {
    expect(hasOpenLayer()).toBe(false);
    const id = pushLayer();
    expect(hasOpenLayer()).toBe(true);
    popLayer(id);
    expect(hasOpenLayer()).toBe(false);
  });
});
