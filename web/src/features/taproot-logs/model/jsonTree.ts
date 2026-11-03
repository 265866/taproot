/**
 * Helpers for the lazy JSON tree. The tree renders only expanded containers and a page of their
 * children at a time, so a conversation with thousands of messages costs a few hundred nodes.
 */

/** Children rendered per container before "show more". */
export const CHILD_PAGE = 100;
/** Characters of a string value shown before "show more". */
export const STRING_PREVIEW = 600;
/** Characters added each time a long string is extended. */
export const STRING_STEP = 20_000;

export type JsonKind = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null';

export function jsonKind(value: unknown): JsonKind {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  switch (typeof value) {
    case 'string':
      return 'string';
    case 'number':
      return 'number';
    case 'boolean':
      return 'boolean';
    default:
      return 'object';
  }
}

export function isContainer(value: unknown): value is Record<string, unknown> | unknown[] {
  return typeof value === 'object' && value !== null;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function childCount(value: unknown): number {
  if (Array.isArray(value)) return value.length;
  if (isObject(value)) return Object.keys(value).length;
  return 0;
}

/** The first `shown` children as [key, value] pairs; array keys are indexes. */
export function childPage(value: unknown, shown: number): [string, unknown][] {
  if (Array.isArray(value)) {
    return value.slice(0, shown).map((child, index) => [String(index), child]);
  }
  if (!isObject(value)) return [];
  const out: [string, unknown][] = [];
  for (const key in value) {
    if (!Object.prototype.hasOwnProperty.call(value, key)) continue;
    if (out.length >= shown) break;
    out.push([key, value[key]]);
  }
  return out;
}

/**
 * Root and small first-level containers open by default; the rest wait for a click. Message
 * arrays and long tool lists therefore start as one line each.
 */
export function openByDefault(depth: number, size: number): boolean {
  if (depth === 0) return true;
  return depth === 1 && size <= 12;
}

export type ContainerSummary =
  { kind: 'array'; count: number } | { kind: 'object'; hint: string; keys: string[]; more: number };

/**
 * A one-line preview of a collapsed container, such as `role: user · content` or a count,
 * so a collapsed message still says what it is.
 */
export function containerSummary(value: unknown): ContainerSummary {
  if (!isObject(value)) return { kind: 'array', count: childCount(value) };
  const hintKey = ['type', 'role', 'name'].find((key) => typeof value[key] === 'string');
  const keys = Object.keys(value).filter((key) => key !== hintKey);
  return {
    kind: 'object',
    hint: hintKey ? `${hintKey}: ${String(value[hintKey]).slice(0, 40)}` : '',
    keys: keys.slice(0, 4),
    more: Math.max(0, keys.length - 4),
  };
}

/** Pretty JSON for copying one subtree; the caller decides whether the subtree is small enough. */
export function prettyJson(value: unknown): string {
  return JSON.stringify(value, null, 2) ?? String(value);
}
