import { describe, expect, test } from 'bun:test';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expectedAccounts, isScenario, SCENARIOS, shotsFor } from './shots';

const docs = join(import.meta.dir, '../../../docs');
const names = (scenario: (typeof SCENARIOS)[number]) => shotsFor(scenario, 'req-1').map((s) => s.name);

describe('shotsFor', () => {
  test('the normal demo captures the Dashboard first, then every other page', () => {
    const shots = shotsFor('normal', 'req-1');
    expect(shots[0]).toMatchObject({ name: 'dashboard', route: '/', fullHeight: true });
    expect(shots.map((s) => s.name)).toContain('accounts');
    expect(shots.find((s) => s.name === 'request-log-detail')?.route).toBe('/request-logs?requestId=req-1');
  });

  test('the degraded and first-run demos capture only their Dashboard state', () => {
    expect(shotsFor('degraded')).toEqual([{ name: 'dashboard-degraded', route: '/', fullHeight: true }]);
    expect(shotsFor('first-run')).toEqual([{ name: 'dashboard-first-run', route: '/', fullHeight: true }]);
  });

  test('the normal demo needs a request log for the detail shot', () => {
    expect(() => shotsFor('normal')).toThrow('request log');
  });

  test('no two shots of any scenario write the same file', () => {
    const all = SCENARIOS.flatMap(names);
    expect(new Set(all).size).toBe(all.length);
  });

  test('the first-run demo waits for no accounts', () => {
    expect(expectedAccounts('first-run')).toBe(0);
    expect(expectedAccounts('normal')).toBe(5);
    expect(expectedAccounts('degraded')).toBe(5);
  });

  test('only the run.sh scenarios with screenshots are accepted', () => {
    expect(SCENARIOS.every(isScenario)).toBe(true);
    expect(isScenario('all-clear')).toBe(false);
    expect(isScenario('')).toBe(false);
  });
});

describe('docs/screenshots', () => {
  const captured = new Set(SCENARIOS.flatMap(names).flatMap((n) => [`${n}.png`, `${n}-mobile.png`]));

  test('every image the docs show is one the capture writes', () => {
    const referenced = new Set<string>();
    const files = [join(docs, '../README.md'), ...readdirSync(docs).filter((f) => f.endsWith('.md')).map((f) => join(docs, f))];
    for (const file of files) {
      const text = readFileSync(file, 'utf8');
      for (const m of text.matchAll(/screenshots\/([a-z0-9-]+\.png)/g)) referenced.add(m[1]);
    }
    expect([...referenced].filter((f) => !captured.has(f))).toEqual([]);
  });

  test('every image the capture writes is in the repository', () => {
    expect([...captured].filter((f) => !existsSync(join(docs, 'screenshots', f)))).toEqual([]);
  });
});
