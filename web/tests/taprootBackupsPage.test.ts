/** Renders the Backups page before its first load answers. bun cannot load SCSS modules. */
import { expect, mock, test } from 'bun:test';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';

const classEcho = () => ({
  default: new Proxy({}, { get: (_, key) => (typeof key === 'string' ? key : undefined) }),
});
const src = join(import.meta.dir, '../src');
for (const sheet of [
  'features/taproot-backups/TaprootBackupsPage.module.scss',
  'features/taproot-backups/NextBackup.module.scss',
]) {
  mock.module(join(src, sheet), classEcho);
}
const { TaprootBackupsPage } = await import('@/features/taproot-backups/TaprootBackupsPage');

test('the summary shows no size until the list has loaded', () => {
  const html = renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(MemoryRouter, null, createElement(TaprootBackupsPage))
    )
  );
  expect(html).toContain('Loading backups');
  expect(html).not.toContain('0 B');
});
