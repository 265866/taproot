import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import {
  RESTART_MAX_PROBES,
  confirmationMatches,
  startRestartWatch,
  stepRestartWatch,
  summarizeBackups,
  type RestartWatch,
} from '@/features/taproot-backups/model/backups';
import { en } from '@/i18n/resources';
import type { Backup } from '@/services/api/taproot';

const backup = (id: string, createdAt: string, bytes: number): Backup => ({
  id,
  createdAt,
  bytes,
  reason: 'manual',
  files: 6,
});

describe('restore confirmation', () => {
  const id = '20261102T031300Z-scheduled';
  test('needs the exact id, ignoring surrounding whitespace', () => {
    expect(confirmationMatches(id, id)).toBe(true);
    expect(confirmationMatches(`  ${id}\n`, id)).toBe(true);
  });
  test('rejects partial, different-case, and empty input', () => {
    expect(confirmationMatches('', id)).toBe(false);
    expect(confirmationMatches('20261102T031300Z', id)).toBe(false);
    expect(confirmationMatches(id.toLowerCase(), id)).toBe(false);
    expect(confirmationMatches('restore', id)).toBe(false);
  });
});

describe('backup summary', () => {
  test('totals sizes and finds the newest regardless of order', () => {
    const list = [
      backup('a', '2026-11-01T03:13:00Z', 1000),
      backup('b', '2026-11-03T03:13:00Z', 2500),
      backup('c', '2026-11-02T03:13:00Z', 500),
    ];
    expect(summarizeBackups(list)).toEqual({ count: 3, bytes: 4000, newest: list[1] });
  });
  test('handles no backups', () => {
    expect(summarizeBackups([])).toEqual({ count: 0, bytes: 0, newest: null });
  });
});

describe('restart watch after a restore', () => {
  const run = (results: boolean[]) =>
    results.reduce<RestartWatch>(stepRestartWatch, startRestartWatch());

  test('down then reachable means back', () => {
    expect(run([false, false, true])).toEqual({ phase: 'back', probes: 3 });
  });
  test('a restart faster than the probe interval counts as back after three good probes', () => {
    expect(run([true, true])).toEqual({ phase: 'waiting', probes: 2 });
    expect(run([true, true, true])).toEqual({ phase: 'back', probes: 3 });
  });
  test('stays down while unreachable and gives up at the limit', () => {
    expect(run([false, false])).toEqual({ phase: 'down', probes: 2 });
    const never = run(Array.from({ length: RESTART_MAX_PROBES }, () => false));
    expect(never).toEqual({ phase: 'gave_up', probes: RESTART_MAX_PROBES });
    // Finished watches do not move again.
    expect(stepRestartWatch(never, true)).toBe(never);
    const back = run([false, true]);
    expect(stepRestartWatch(back, false)).toBe(back);
  });
  test('the give-up copy matches the probe budget', () => {
    // 45 probes two seconds apart.
    expect(en.taproot_backups.restart_gave_up).toContain('90 seconds');
    const source = readFileSync(
      new URL('../src/features/taproot-backups/model/backups.ts', import.meta.url),
      'utf8'
    );
    expect(source).toContain('RESTART_PROBE_MS = 2000');
    expect(RESTART_MAX_PROBES).toBe(45);
  });
});
