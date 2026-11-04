import { afterEach, describe, expect, test } from 'bun:test';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { EmptyState } from '../src/components/ui/EmptyState';
import { usePrivacyStore } from '../src/stores/usePrivacyStore';

afterEach(() => {
  usePrivacyStore.setState({ showEmails: false });
});

test('EmptyState masks server text in its title and description', () => {
  const render = () =>
    renderToStaticMarkup(
      createElement(EmptyState, {
        title: 'Could not load: auths/claude-ab12cd34-zebrafinch.alpha@sl.example.run.json',
        description: 'quokkabeta@example.com is locked',
      })
    );
  const hidden = render();
  expect(hidden).not.toContain('zebrafinch');
  expect(hidden).not.toContain('quokkabeta');
  usePrivacyStore.getState().toggleShowEmails();
  expect(render()).toContain('zebrafinch.alpha@sl.example.run');
});

// Error text comes from the server and can name an auth file. Settings shows Tailscale logins on
// purpose, and form fields show their own validation, so they are out of scope.
const EXEMPT = [
  'features/taproot-settings/',
  'components/ui/Input.tsx',
  'components/ui/AutocompleteInput.tsx',
  'features/config/components/fields/',
  'features/config/components/blocks/',
];

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return sourceFiles(path);
    return path.endsWith('.tsx') ? [path] : [];
  });
}

describe('error banners', () => {
  test('every rendered error message goes through the masker', () => {
    const unmasked: string[] = [];
    for (const file of sourceFiles('src')) {
      const rel = file
        .split('\\')
        .join('/')
        .replace(/^src\//, '');
      if (EXEMPT.some((prefix) => rel.startsWith(prefix))) continue;
      const lines = readFileSync(file, 'utf8').split('\n');
      lines.forEach((line, index) => {
        const child = line.trim();
        const call = lines
          .slice(index, index + 4)
          .join(' ')
          .split('})}')[0];
        const translatedError = /(^|>|=)\{t\(/.test(child) && call.includes('message:');
        const rawError =
          /^\{[\w.]*error(Message|\.message)?\}$/i.test(child) ||
          />\{[\w.]*error(Message|\.message)?\}</i.test(child);
        const errorProp = /(description|title)=\{[\w.]*error\}/i.test(child);
        if ((translatedError || rawError || errorProp) && !child.includes('mask(')) {
          unmasked.push(`${rel}:${index + 1}: ${child}`);
        }
      });
    }
    expect(unmasked).toEqual([]);
  });
});
