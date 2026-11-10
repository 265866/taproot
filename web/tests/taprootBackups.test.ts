import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import {
  RESTART_MAX_PROBES,
  confirmationMatches,
  isRestartWatchFinished,
  lockedOutKey,
  restartProbeFailure,
  restoreRefusal,
  startRestartWatch,
  stepRestartWatch,
  summarizeBackups,
  type RestartProbe,
  type RestartRefusal,
  type RestartWatch,
} from '@/features/taproot-backups/model/backups';
import { en } from '@/i18n/resources';
import { TaprootApiError, createTaprootClient, type Backup } from '@/services/api/taproot';

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
  const run = (results: RestartProbe[]) =>
    results.reduce<RestartWatch>(stepRestartWatch, startRestartWatch());
  const phase = (results: RestartProbe[]) => run(results).phase;

  test('down then reachable means back', () => {
    expect(run(['down', 'down', 'ok'])).toMatchObject({ phase: 'back', probes: 3 });
  });
  test('a restart faster than the probe interval counts as back after three good probes', () => {
    expect(run(['ok', 'ok'])).toMatchObject({ phase: 'waiting', probes: 2 });
    expect(run(['ok', 'ok', 'ok'])).toMatchObject({ phase: 'back', probes: 3 });
  });
  test('stays down while unreachable and gives up at the limit', () => {
    expect(run(['down', 'down'])).toMatchObject({ phase: 'down', probes: 2 });
    const never = run(Array.from({ length: RESTART_MAX_PROBES }, (): RestartProbe => 'down'));
    expect(never).toMatchObject({ phase: 'gave_up', probes: RESTART_MAX_PROBES });
    // Finished watches do not move again.
    expect(stepRestartWatch(never, 'ok')).toBe(never);
    const back = run(['down', 'ok']);
    expect(stepRestartWatch(back, 'down')).toBe(back);
  });
  const refused: RestartRefusal = { code: 'not_admin', message: 'no Admin' };
  const guardOff: RestartRefusal = { code: 'guard_disabled', message: 'guard off' };
  const unknownHost: RestartRefusal = { code: 'unknown_host', message: 'unknown host' };
  const times = (n: number, probe: RestartProbe) => Array.from({ length: n }, () => probe);

  test('a server that keeps refusing this browser is locked out, not down', () => {
    expect(phase(['down', refused, refused, refused])).toBe('locked_out');
    // One refusal while the server starts up is not a lockout.
    expect(phase(['down', refused, 'ok'])).toBe('back');
    expect(phase(['down', refused, 'down', refused, refused])).toBe('down');
    expect(run(['down', guardOff, guardOff, guardOff])).toMatchObject({
      phase: 'locked_out',
      refusal: guardOff,
    });
  });
  test('a lockout keeps probing, so a refusal that clears still ends as back', () => {
    const locked = run(['down', refused, refused, refused]);
    expect(isRestartWatchFinished(locked)).toBe(false);
    expect(stepRestartWatch(locked, 'ok')).toMatchObject({ phase: 'back', refusal: null });
    const forGood = run(times(RESTART_MAX_PROBES, refused));
    expect(forGood).toMatchObject({ phase: 'locked_out', probes: RESTART_MAX_PROBES });
    expect(isRestartWatchFinished(forGood)).toBe(true);
    expect(stepRestartWatch(forGood, 'ok')).toBe(forGood);
  });
  test('an unknown host counts as a lockout only after it lasts', () => {
    // A starting server refuses its own MagicDNS name until it has read tailscaled.
    expect(phase(['down', ...times(9, unknownHost)])).toBe('down');
    expect(phase(['down', ...times(9, unknownHost), 'ok'])).toBe('back');
    expect(run(['down', ...times(10, unknownHost)])).toMatchObject({
      phase: 'locked_out',
      refusal: unknownHost,
    });
  });
  test('the lockout banner is worded by the refusal', () => {
    expect(lockedOutKey(guardOff)).toBe('restart_locked_out_guard_disabled');
    expect(lockedOutKey(unknownHost)).toBe('restart_locked_out_unknown_host');
    expect(lockedOutKey(refused)).toBe('restart_locked_out');
    expect(lockedOutKey(null)).toBe('restart_locked_out');
    const copy = en.taproot_backups as Record<string, string>;
    expect(copy.restart_locked_out).toContain('{{message}}');
    expect(copy.restart_locked_out).not.toContain('Admin');
    expect(copy.restart_locked_out_guard_disabled).toContain('access.enabled');
    expect(copy.restart_locked_out_unknown_host).toContain('{{host}}');
    expect(copy.restart_locked_out_unknown_host).toContain('access.extra-hosts');
  });
  test('a 403 is a refusal with its code, except while the guard cannot tell the server yet', () => {
    expect(restartProbeFailure(new TaprootApiError(403, 'not_admin', 'no'))).toEqual({
      code: 'not_admin',
      message: 'no',
    });
    expect(restartProbeFailure(new TaprootApiError(403, 'http_error', '403 Forbidden'))).toEqual({
      code: 'http_error',
      message: '403 Forbidden',
    });
    expect(restartProbeFailure(new TaprootApiError(403, 'server_node_unknown', 'later'))).toBe(
      'down'
    );
    expect(restartProbeFailure(new TaprootApiError(502, 'http_error', 'bad gateway'))).toBe('down');
    expect(restartProbeFailure(new TypeError('Failed to fetch'))).toBe('down');
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

describe('refused restores', () => {
  test('maps each 409 to its own message', () => {
    expect(restoreRefusal(new TaprootApiError(409, 'would_lock_out', 'x'))).toEqual({
      kind: 'would_lock_out',
    });
    expect(
      restoreRefusal(new TaprootApiError(409, 'incompatible_backup', 'incompatible backup: old'))
    ).toEqual({ kind: 'incompatible', message: 'incompatible backup: old' });
    expect(restoreRefusal(new TaprootApiError(409, 'restart_pending', 'x'))).toEqual({
      kind: 'restart_pending',
    });
    expect(restoreRefusal(new TaprootApiError(422, 'corrupt_backup', 'corrupt'))).toEqual({
      kind: 'other',
      message: 'corrupt',
    });
    for (const key of [
      'restore_lockout',
      'restore_anyway',
      'restore_incompatible',
      'restore_restart_pending',
      'restart_locked_out',
      'restart_locked_out_guard_disabled',
      'restart_locked_out_unknown_host',
    ]) {
      expect((en.taproot_backups as Record<string, string>)[key]?.length).toBeGreaterThan(0);
    }
  });

  test('restore sends allowLockout only when asked', async () => {
    const bodies: (string | undefined)[] = [];
    const client = createTaprootClient('/taproot/api/v1', (async (
      _url: RequestInfo | URL,
      init?: RequestInit
    ) => {
      bodies.push(init?.body as string | undefined);
      return new Response(
        '{"restored":true,"preRestoreBackupId":"p","restart":"scheduled","message":"m"}'
      );
    }) as typeof fetch);
    await client.restoreBackup('b1');
    await client.restoreBackup('b1', { allowLockout: true });
    expect(bodies).toEqual([undefined, '{"allowLockout":true}']);
  });
});
