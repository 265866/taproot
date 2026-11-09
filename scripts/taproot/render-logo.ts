#!/usr/bin/env bun
// Renders the favicon mark (web/src/assets/taprootMark.ts) to the 256x256 PNG
// that Discord alerts use as the avatar and thumbnail, and records the SVG's
// SHA-256 next to it. internal/taproot/alerts/logo_test.go fails when the mark
// changes without a new render. Run it from anywhere in the repository:
//
//   bun scripts/taproot/render-logo.ts
//
// resvg-js is installed into a temporary directory, so it is not a dependency
// of the dashboard.
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { TAPROOT_FAVICON_DATA_URL } from '../../web/src/assets/taprootMark';

const RESVG = '@resvg/resvg-js@2.6.2';
const SIZE = 256;
const repo = join(import.meta.dir, '..', '..');
const out = join(repo, 'internal', 'taproot', 'alerts', 'assets');

const svg = decodeURIComponent(TAPROOT_FAVICON_DATA_URL.replace('data:image/svg+xml,', ''));
const tmp = mkdtempSync(join(tmpdir(), 'taproot-logo-'));
try {
  writeFileSync(join(tmp, 'package.json'), '{"private":true}\n');
  const install = Bun.spawnSync(['bun', 'add', '--exact', RESVG], { cwd: tmp, stderr: 'pipe' });
  if (install.exitCode !== 0) {
    throw new Error(`bun add ${RESVG} failed:\n${install.stderr.toString()}`);
  }
  const { Resvg } = await import(join(tmp, 'node_modules', '@resvg', 'resvg-js', 'index.js'));
  const png = new Resvg(svg, { fitTo: { mode: 'width', value: SIZE } }).render().asPng();
  writeFileSync(join(out, 'taproot.png'), png);
  const sum = new Bun.CryptoHasher('sha256').update(svg).digest('hex');
  writeFileSync(join(out, 'taproot.svg.sha256'), `${sum}\n`);
  console.log(`wrote ${join(out, 'taproot.png')} (${png.length} bytes), svg sha256 ${sum}`);
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
