/**
 * Classifying and windowing request and response bodies. Claude Code resends the whole
 * conversation every turn, so one body can be tens of megabytes: nothing here may render or copy
 * a body whole unless the caller asked for it.
 */

import { parseJsonValue, type JsonValue } from './jsonValue';

/** Bodies above this start collapsed and are parsed only when opened. */
export const COLLAPSE_BYTES = 256 * 1024;
/** Above this, parsing JSON on the main thread would stall the tab for too long. */
export const PARSE_LIMIT_BYTES = 48 * 1024 * 1024;
/** Characters of raw text shown at first, and added by each "show more". */
export const TEXT_CHUNK = 64 * 1024;
/** Server-sent events shown at first, and added by each "show more". */
export const SSE_PAGE = 100;

export type BodyKind = 'empty' | 'json' | 'sse' | 'text';

/** A cheap look at the first bytes; `json` still has to parse. */
export function sniffBody(text: string): BodyKind {
  const start = text.search(/\S/);
  if (start < 0) return 'empty';
  const first = text[start];
  if (first === '{' || first === '[') return 'json';
  if (/^(event|data|id|retry):/.test(text.slice(start, start + 8)) || text.startsWith(':', start)) {
    return 'sse';
  }
  return 'text';
}

export type ParsedJson =
  { ok: true; value: JsonValue } | { ok: false; reason: 'invalid' | 'too_large' };

export function parseJsonBody(text: string): ParsedJson {
  if (text.length > PARSE_LIMIT_BYTES) return { ok: false, reason: 'too_large' };
  try {
    return { ok: true, value: parseJsonValue(text) };
  } catch {
    return { ok: false, reason: 'invalid' };
  }
}

/** Whether a stream event's data can be shown formatted; `[DONE]` starts like JSON but is not. */
export function isFormattableJson(text: string): boolean {
  if (sniffBody(text) !== 'json' || text.length > PARSE_LIMIT_BYTES) return false;
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

export interface SseEvent {
  event: string;
  data: string;
}

/** Splits an event stream into events, keeping at most `limit` and counting the rest. */
export function parseSse(text: string, limit = Number.POSITIVE_INFINITY) {
  const events: SseEvent[] = [];
  let total = 0;
  for (const block of text.split(/\r?\n\r?\n/)) {
    if (!block.trim()) continue;
    let event = '';
    const data: string[] = [];
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith('event:')) event = line.slice(6).trim();
      else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
    }
    if (!event && data.length === 0) continue;
    total += 1;
    if (events.length < limit) events.push({ event: event || 'message', data: data.join('\n') });
  }
  return { events, total };
}

/** The first `shown` characters, never splitting a surrogate pair. */
export function textWindow(text: string, shown: number) {
  if (shown >= text.length) return { visible: text, remaining: 0 };
  let end = Math.max(0, shown);
  const code = text.charCodeAt(end - 1);
  if (code >= 0xd800 && code <= 0xdbff) end -= 1;
  return { visible: text.slice(0, end), remaining: text.length - end };
}
