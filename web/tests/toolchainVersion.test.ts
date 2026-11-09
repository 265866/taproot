import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';

const readProjectFile = (path: string) =>
  readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');
const packageJson = JSON.parse(readProjectFile('package.json')) as { packageManager: string };
const bunVersion = packageJson.packageManager.match(/^bun@(\d+\.\d+\.\d+)$/)?.[1];

describe('Bun toolchain version', () => {
  test('pins an exact Bun release', () => {
    expect(bunVersion).toBeDefined();
  });

  test('the taproot image builds the dashboard with the pinned Bun release', () => {
    expect(readProjectFile('../Containerfile.taproot')).toContain(`ARG BUN_VERSION=${bunVersion}`);
  });

  test('CI reads the Bun release from package.json', () => {
    expect(readProjectFile('../.github/workflows/ci.yml')).toContain(
      'bun-version-file: web/package.json'
    );
  });

  // Renovate moves the pin, so the README names the field instead of a version that goes stale.
  test('the dashboard README points to the pin without repeating the version', () => {
    const readme = readProjectFile('README.md');
    expect(readme).toContain('`packageManager` field in `package.json`');
    expect(readme).not.toMatch(/bun@\d/);
  });
});
