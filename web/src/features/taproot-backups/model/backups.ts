import { TaprootApiError, type Backup } from '@/services/api/taproot';

/** Restore asks the operator to type the backup id; surrounding spaces from a paste are fine. */
export function confirmationMatches(typed: string, backupId: string): boolean {
  return typed.trim() === backupId;
}

export interface BackupTotals {
  count: number;
  bytes: number;
  newest: Backup | null;
}

/** The list arrives newest first, but the summary does not rely on it. */
export function summarizeBackups(backups: Backup[]): BackupTotals {
  let newest: Backup | null = null;
  let bytes = 0;
  for (const backup of backups) {
    bytes += backup.bytes;
    if (!newest || Date.parse(backup.createdAt) > Date.parse(newest.createdAt)) newest = backup;
  }
  return { count: backups.length, bytes, newest };
}

/**
 * Watching the server come back after a restore that scheduled a restart. The process exits
 * right after answering, so the first probes usually fail; a supervisor that restarts it faster
 * than the probe interval shows up as a few successful probes instead. A server that answers but
 * refuses this browser, as after a restore that took this device's Admin, is `locked_out` rather
 * than down.
 */
export type RestartPhase = 'waiting' | 'down' | 'back' | 'locked_out' | 'gave_up';

/** One probe's result: answered, refused this browser (403), or no answer. */
export type RestartProbe = 'ok' | 'refused' | 'down';

export interface RestartWatch {
  phase: RestartPhase;
  probes: number;
  /** Refusals in a row. */
  refusals: number;
}

export const RESTART_PROBE_MS = 2000;
export const RESTART_MAX_PROBES = 45;
/** Probes in a row, without ever seeing the server down, that count as "already back". */
const QUIET_RESTART_PROBES = 3;
/** Refusals in a row that count as locked out, so one refusal while starting up does not. */
const LOCKED_OUT_PROBES = 3;

export function startRestartWatch(): RestartWatch {
  return { phase: 'waiting', probes: 0, refusals: 0 };
}

export function isRestartWatchFinished(watch: RestartWatch): boolean {
  return watch.phase === 'back' || watch.phase === 'locked_out' || watch.phase === 'gave_up';
}

export function stepRestartWatch(watch: RestartWatch, probe: RestartProbe): RestartWatch {
  if (isRestartWatchFinished(watch)) return watch;
  const probes = watch.probes + 1;
  const refusals = probe === 'refused' ? watch.refusals + 1 : 0;
  if (probe === 'ok' && (watch.phase === 'down' || probes >= QUIET_RESTART_PROBES)) {
    return { phase: 'back', probes, refusals };
  }
  if (refusals >= LOCKED_OUT_PROBES) return { phase: 'locked_out', probes, refusals };
  if (probes >= RESTART_MAX_PROBES) return { phase: 'gave_up', probes, refusals };
  return { phase: probe === 'down' ? 'down' : watch.phase, probes, refusals };
}

/**
 * Classifies a failed probe. A 403 is the server refusing this browser, except
 * `server_node_unknown`, which the guard answers while it has not read tailscaled yet.
 */
export function restartProbeFailure(err: unknown): RestartProbe {
  return err instanceof TaprootApiError && err.status === 403 && err.code !== 'server_node_unknown'
    ? 'refused'
    : 'down';
}

/** What the restore dialog does with a refused restore. */
export type RestoreRefusal =
  | { kind: 'would_lock_out' }
  | { kind: 'incompatible'; message: string }
  | { kind: 'restart_pending' }
  | { kind: 'other'; message: string };

export function restoreRefusal(err: unknown): RestoreRefusal {
  const message = err instanceof Error ? err.message : String(err);
  if (!(err instanceof TaprootApiError)) return { kind: 'other', message };
  switch (err.code) {
    case 'would_lock_out':
      return { kind: 'would_lock_out' };
    case 'incompatible_backup':
      return { kind: 'incompatible', message };
    case 'restart_pending':
      return { kind: 'restart_pending' };
    default:
      return { kind: 'other', message };
  }
}
