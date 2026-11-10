/**
 * Helpers for the lazy JSON tree. The tree renders only expanded containers and a page of their
 * children at a time, so a conversation with thousands of messages costs a few hundred nodes.
 */

import { JsonNumber, JsonObject, type JsonValue } from './jsonValue';

/** Children rendered per container before "show more". */
export const CHILD_PAGE = 100;
/** Characters of a string value shown before "show more". */
export const STRING_PREVIEW = 600;
/** Characters added each time a long string is extended. */
export const STRING_STEP = 20_000;

export type JsonKind = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null';

export function jsonKind(value: JsonValue): JsonKind {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (value instanceof JsonObject) return 'object';
  if (value instanceof JsonNumber) return 'number';
  return typeof value === 'string' ? 'string' : 'boolean';
}

export function isContainer(value: JsonValue): value is JsonObject | JsonValue[] {
  return Array.isArray(value) || value instanceof JsonObject;
}

/** How a scalar reads in the tree: numbers as written, strings without their quotes. */
export function scalarText(value: JsonValue): string {
  if (value instanceof JsonNumber) return value.text;
  return String(value);
}

export function childCount(value: JsonValue): number {
  if (Array.isArray(value)) return value.length;
  if (value instanceof JsonObject) return value.entries.length;
  return 0;
}

/** A child of a container: array keys are indexes; `duplicate` marks a key seen before it. */
export interface JsonChild {
  key: string;
  value: JsonValue;
  duplicate: boolean;
}

/** The first `shown` children. */
export function childPage(value: JsonValue, shown: number): JsonChild[] {
  if (Array.isArray(value)) {
    return value
      .slice(0, shown)
      .map((child, index) => ({ key: String(index), value: child, duplicate: false }));
  }
  if (!(value instanceof JsonObject)) return [];
  const seen = new Set<string>();
  const out: JsonChild[] = [];
  for (const [key, child] of value.entries) {
    if (out.length >= shown) break;
    out.push({ key, value: child, duplicate: seen.has(key) });
    seen.add(key);
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
export function containerSummary(value: JsonValue): ContainerSummary {
  if (!(value instanceof JsonObject)) return { kind: 'array', count: childCount(value) };
  let hint: [string, string] | undefined;
  for (const name of ['type', 'role', 'name']) {
    const entry = value.entries.find(([key, child]) => key === name && typeof child === 'string');
    if (entry) {
      hint = [name, entry[1] as string];
      break;
    }
  }
  const keys = value.entries.map(([key]) => key).filter((key) => key !== hint?.[0]);
  return {
    kind: 'object',
    hint: hint ? `${hint[0]}: ${hint[1].slice(0, 40)}` : '',
    keys: keys.slice(0, 4),
    more: Math.max(0, keys.length - 4),
  };
}
