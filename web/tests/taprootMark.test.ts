import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { TAPROOT_MARK_DATA_URL } from '../src/assets/taprootMark';

const webRoot = join(import.meta.dir, '..');

describe('taproot mark', () => {
  test('index.html uses the mark as its favicon', () => {
    const html = readFileSync(join(webRoot, 'index.html'), 'utf8');
    const icon = html.match(/<link\s+rel="icon"\s+type="image\/svg\+xml"\s+href="([^"]+)"/);
    expect(icon?.[1]).toBe(TAPROOT_MARK_DATA_URL);
  });
});
