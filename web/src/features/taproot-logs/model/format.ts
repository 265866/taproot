import type { RequestLogStats } from '@/services/api/taproot';

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const;

/** 1024-based sizes with three significant digits: 980 B, 12.3 KB, 1.25 GB. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  if (unit === 0) return `${Math.round(value)} B`;
  const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(digits)} ${UNITS[unit]}`;
}

export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

export interface StorageSummary {
  /** Raw bytes per byte on disk; null until something is stored. */
  ratio: number | null;
  /** Sum of the writer's drop and failure counters since the server started. */
  problems: number;
}

export function summarizeStorage(stats: RequestLogStats): StorageSummary {
  return {
    ratio: stats.bytesOnDisk > 0 && stats.rawBytes > 0 ? stats.rawBytes / stats.bytesOnDisk : null,
    problems:
      stats.droppedBodies + stats.droppedRecords + stats.truncatedResponses + stats.writeErrors,
  };
}

export function formatRatio(ratio: number | null): string {
  if (ratio === null) return '—';
  return `${ratio >= 10 ? Math.round(ratio) : ratio.toFixed(1)}×`;
}

export type StatusTone = 'success' | 'info' | 'warn' | 'error';

export function statusTone(status: number): StatusTone {
  if (status >= 500 || status === 0) return 'error';
  if (status >= 400) return 'warn';
  if (status >= 300) return 'info';
  return 'success';
}

/** Session ids carry a client prefix ("claude:<uuid>"); the short form keeps table cells narrow. */
export function shortSessionId(sessionId: string): string {
  const bare = sessionId.replace(/^[a-z]+:/, '');
  return bare.length > 13 ? `${bare.slice(0, 8)}…${bare.slice(-4)}` : bare;
}
