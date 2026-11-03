import { describe, expect, test } from 'bun:test';
import { readdirSync } from 'node:fs';
import i18n from '@/i18n';
import { mergeLocaleModules } from '@/i18n/merge';
import { en } from '@/i18n/resources';

describe('per-feature English catalogs', () => {
  test('merges every file under locales/en into one resource', async () => {
    const files = readdirSync('src/i18n/locales/en').filter((name) => name.endsWith('.json'));
    expect(files.length).toBeGreaterThan(10);
    for (const name of files) {
      const tree = (await Bun.file(`src/i18n/locales/en/${name}`).json()) as object;
      for (const key of Object.keys(tree)) expect(en).toHaveProperty(key);
    }
  });

  test('the app instance resolves keys from different files with English fallback', () => {
    expect(i18n.language).toBe('en');
    expect(i18n.t('nav.quota')).toBe('Quota');
    expect(i18n.t('taproot_usage.title')).toBe('Usage');
    expect(i18n.t('auth_files.alias_editor.add_model')).toBe('Add model');
  });

  test('two files claiming one top-level key fail loudly', () => {
    expect(() =>
      mergeLocaleModules({ './a.json': { nav: { a: 'A' } }, './b.json': { nav: { b: 'B' } } })
    ).toThrow('i18n key "nav" is defined by both ./a.json and ./b.json');
  });

  test('distinct keys merge without reordering their contents', () => {
    expect(mergeLocaleModules({ './b.json': { b: { x: '1' } }, './a.json': { a: 'A' } })).toEqual({
      a: 'A',
      b: { x: '1' },
    });
  });
});
