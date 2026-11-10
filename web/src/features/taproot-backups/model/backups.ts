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
 * keeps refusing this browser is `locked_out` rather than down. Probing goes on while it refuses,
 * so a refusal that clears, such as a host name the new process accepts once it has read
 * tailscaled, still ends as `back`.
 */
export type RestartPhase = 'waiting' | 'down' | 'back' | 'locked_out' | 'gave_up';

/** A probe the server answered with a 403: the guard's code and message. */
export interface RestartRefusal {
  code: string;
  message: string;
}

/** One probe's result: answered, refused this browser, or no answer. */
export type RestartProbe = 'ok' | 'down' | RestartRefusal;

export interface RestartWatch {
  phase: RestartPhase;
  probes: number;
  /** Refusals in a row. */
  refusals: number;
  /** The latest refusal, while refusals continue. */
  refusal: RestartRefusal | null;
}

export const RESTART_PROBE_MS = 2000;
export const RESTART_MAX_PROBES = 45;
/** Probes in a row, without ever seeing the server down, that count as "already back". */
const QUIET_RESTART_PROBES = 3;
/** Refusals in a row that count as locked out, so one refusal while starting up does not. */
const LOCKED_OUT_PROBES = 3;
/**
 * A starting server answers `unknown_host` to its own MagicDNS name until it has read
 * tailscaled, so that refusal counts only after this many in a row.
 */
const UNKNOWN_HOST_PROBES = 10;

export function startRestartWatch(): RestartWatch {
  return { phase: 'waiting', probes: 0, refusals: 0, refusal: null };
}

export function isRestartWatchFinished(watch: RestartWatch): boolean {
  return watch.phase === 'back' || watch.phase === 'gave_up' || watch.probes >= RESTART_MAX_PROBES;
}

export function stepRestartWatch(watch: RestartWatch, probe: RestartProbe): RestartWatch {
  if (isRestartWatchFinished(watch)) return watch;
  const probes = watch.probes + 1;
  if (probe === 'ok') {
    const back = watch.phase === 'down' || watch.phase === 'locked_out';
    return {
      phase: back || probes >= QUIET_RESTART_PROBES ? 'back' : watch.phase,
      probes,
      refusals: 0,
      refusal: null,
    };
  }
  if (probe === 'down') {
    return {
      phase: probes >= RESTART_MAX_PROBES ? 'gave_up' : 'down',
      probes,
      refusals: 0,
      refusal: null,
    };
  }
  const refusals = watch.refusals + 1;
  const needed = probe.code === 'unknown_host' ? UNKNOWN_HOST_PROBES : LOCKED_OUT_PROBES;
  let phase = watch.phase;
  if (refusals >= needed) phase = 'locked_out';
  else if (probes >= RESTART_MAX_PROBES) phase = 'gave_up';
  return { phase, probes, refusals, refusal: probe };
}

/**
 * Classifies a failed probe. A 403 is the server refusing this browser, except
 * `server_node_unknown`, which the guard answers while it has not read tailscaled yet.
 */
export function restartProbeFailure(err: unknown): RestartProbe {
  return err instanceof TaprootApiError && err.status === 403 && err.code !== 'server_node_unknown'
    ? { code: err.code, message: err.message }
    : 'down';
}

/** The banner copy for a server that keeps refusing this browser, by the guard's code. */
export function lockedOutKey(refusal: RestartRefusal | null): string {
  switch (refusal?.code) {
    case 'guard_disabled':
      return 'restart_locked_out_guard_disabled';
    case 'unknown_host':
      return 'restart_locked_out_unknown_host';
    default:
      return 'restart_locked_out';
  }
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
