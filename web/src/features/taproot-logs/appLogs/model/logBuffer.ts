import type { LogsQuery, LogsResponse } from '@/services/api/logs';

export const LOG_PAGE_SIZE = 10000;
export const MAX_LOG_TEXT_UNITS = 8 * 1024 * 1024;
export const INITIAL_VISIBLE_LINES = 100;

export interface LogBuffer {
  buffer: string[];
  bufferStart: number;
  nextId: number;
  textUnits: number;
  evicted: number;
  cursor?: string;
}

export const emptyLogBuffer = (): LogBuffer => ({
  buffer: [],
  bufferStart: 0,
  nextId: 0,
  textUnits: 0,
  evicted: 0,
});

export function buildLogsQuery(cursor?: string): LogsQuery {
  return cursor ? { limit: LOG_PAGE_SIZE, cursor } : { limit: LOG_PAGE_SIZE };
}

/** Cursor pages contain new records, including legitimate identical adjacent lines. */
export function applyLogPage(
  current: LogBuffer,
  response: LogsResponse,
  requestedCursor?: string,
  maxLines = LOG_PAGE_SIZE,
  maxTextUnits = MAX_LOG_TEXT_UNITS
): LogBuffer {
  const append = Boolean(requestedCursor) && !response.cursorReset;
  const incoming = response.lines;
  const buffer = append ? [...current.buffer, ...incoming] : [...incoming];
  let textUnits =
    (append ? current.textUnits : 0) + incoming.reduce((n, line) => n + line.length + 1, 0);
  let drop = 0;
  // Keep one oversized line intact; never silently truncate its contents.
  while (
    buffer.length - drop > 1 &&
    (buffer.length - drop > maxLines || textUnits > maxTextUnits)
  ) {
    textUnits -= buffer[drop].length + 1;
    drop++;
  }
  const start = append ? current.bufferStart : current.nextId;
  return {
    buffer: buffer.slice(drop),
    bufferStart: start + drop,
    nextId: current.nextId + incoming.length,
    textUnits,
    evicted: (append ? current.evicted : 0) + drop,
    cursor: response.nextCursor,
  };
}

export function shouldCatchUp(response: LogsResponse, requestedCursor?: string): boolean {
  return Boolean(
    requestedCursor &&
    response.nextCursor &&
    response.nextCursor !== requestedCursor &&
    !response.cursorReset &&
    response.lines.length >= LOG_PAGE_SIZE
  );
}

const LINE_TIME = /^\[?(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})/;

/**
 * The server's UTC offset in minutes, or null when a page cannot tell. Log lines carry the
 * server's local time without a zone, and the page's `latest-timestamp` is the newest of them read
 * in that zone, so the two differ by exactly the offset.
 */
export function serverUtcOffsetMinutes(
  lines: readonly string[],
  latestUnix: number | undefined
): number | null {
  if (!latestUnix) return null;
  let newest = Number.NEGATIVE_INFINITY;
  for (const line of lines) {
    const m = LINE_TIME.exec(line);
    if (!m) continue;
    const seconds = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]) / 1000;
    if (seconds > newest) newest = seconds;
  }
  if (!Number.isFinite(newest)) return null;
  // Zones are whole quarter hours; anything far off means the page's lines are not the newest.
  const minutes = (newest - latestUnix) / 60;
  const rounded = Math.round(minutes / 15) * 15;
  return Math.abs(minutes - rounded) < 1 && Math.abs(rounded) <= 14 * 60 ? rounded : null;
}

/** `UTC+05:30`, `UTC−03:00`, `UTC+00:00`. */
export function formatUtcOffset(minutes: number): string {
  const sign = minutes < 0 ? '−' : '+';
  const abs = Math.abs(minutes);
  const hh = String(Math.floor(abs / 60)).padStart(2, '0');
  const mm = String(abs % 60).padStart(2, '0');
  return `UTC${sign}${hh}:${mm}`;
}
