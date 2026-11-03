import type { Backup } from '@/services/api/taproot';

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
 * than the probe interval shows up as a few successful probes instead.
 */
export type RestartPhase = 'waiting' | 'down' | 'back' | 'gave_up';

export interface RestartWatch {
  phase: RestartPhase;
  probes: number;
}

export const RESTART_PROBE_MS = 2000;
export const RESTART_MAX_PROBES = 45;
/** Successful probes, without ever seeing the server down, that count as "already back". */
const QUIET_RESTART_PROBES = 3;

export function startRestartWatch(): RestartWatch {
  return { phase: 'waiting', probes: 0 };
}

export function stepRestartWatch(watch: RestartWatch, reachable: boolean): RestartWatch {
  if (watch.phase === 'back' || watch.phase === 'gave_up') return watch;
  const probes = watch.probes + 1;
  if (reachable && (watch.phase === 'down' || probes >= QUIET_RESTART_PROBES)) {
    return { phase: 'back', probes };
  }
  if (probes >= RESTART_MAX_PROBES) return { phase: 'gave_up', probes };
  return { phase: reachable ? watch.phase : 'down', probes };
}
