/**
 * JSON parsed for display without losing anything the stored text says. `JSON.parse` rounds
 * numbers past 2^53 (`12345678901234567890` becomes `12345678901234567000`), rewrites lexemes
 * (`1.50`, `1e3`), keeps only the last of duplicate keys, and moves integer-like keys first. An
 * operator debugging a rejected request must see what the client sent, so objects keep their
 * entries in order, duplicates included, and numbers keep their text.
 */

export class JsonNumber {
  constructor(readonly text: string) {}
}

export class JsonObject {
  constructor(readonly entries: readonly [string, JsonValue][]) {}
}

export type JsonValue = string | boolean | null | JsonNumber | JsonObject | JsonValue[];

const NUMBER = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;

/** Parses strict JSON; throws a SyntaxError on anything `JSON.parse` would reject. */
export function parseJsonValue(text: string): JsonValue {
  let pos = 0;
  // The next backslash at or after `pos`, found once rather than searched for at every string.
  let nextBackslash = -2;

  const fail = (): never => {
    throw new SyntaxError(`Invalid JSON at position ${pos}`);
  };

  const skipSpace = () => {
    for (;;) {
      const code = text.charCodeAt(pos);
      if (code === 0x20 || code === 0x0a || code === 0x0d || code === 0x09) pos++;
      else return;
    }
  };

  const parseString = (): string => {
    const start = pos;
    const close = text.indexOf('"', start + 1);
    if (close < 0) fail();
    if (nextBackslash !== -1 && nextBackslash <= start) nextBackslash = text.indexOf('\\', start);
    if (nextBackslash === -1 || nextBackslash > close) {
      pos = close + 1;
      const value = text.slice(start + 1, close);
      for (let i = 0; i < value.length; i++) if (value.charCodeAt(i) < 0x20) fail();
      return value;
    }
    // Escapes: find the closing quote, then let the native parser decode the string alone.
    let end = start + 1;
    for (;;) {
      const code = text.charCodeAt(end);
      if (Number.isNaN(code)) fail();
      if (code === 0x5c) end += 2;
      else if (code === 0x22) break;
      else end++;
    }
    pos = end + 1;
    return JSON.parse(text.slice(start, end + 1)) as string;
  };

  const parseValue = (): JsonValue => {
    skipSpace();
    const code = text.charCodeAt(pos);
    if (code === 0x22) return parseString();
    if (code === 0x7b) {
      pos++;
      const entries: [string, JsonValue][] = [];
      skipSpace();
      if (text.charCodeAt(pos) === 0x7d) {
        pos++;
        return new JsonObject(entries);
      }
      for (;;) {
        skipSpace();
        if (text.charCodeAt(pos) !== 0x22) fail();
        const key = parseString();
        skipSpace();
        if (text.charCodeAt(pos) !== 0x3a) fail();
        pos++;
        entries.push([key, parseValue()]);
        skipSpace();
        const next = text.charCodeAt(pos++);
        if (next === 0x7d) return new JsonObject(entries);
        if (next !== 0x2c) fail();
      }
    }
    if (code === 0x5b) {
      pos++;
      const items: JsonValue[] = [];
      skipSpace();
      if (text.charCodeAt(pos) === 0x5d) {
        pos++;
        return items;
      }
      for (;;) {
        items.push(parseValue());
        skipSpace();
        const next = text.charCodeAt(pos++);
        if (next === 0x5d) return items;
        if (next !== 0x2c) fail();
      }
    }
    if (text.startsWith('true', pos)) {
      pos += 4;
      return true;
    }
    if (text.startsWith('false', pos)) {
      pos += 5;
      return false;
    }
    if (text.startsWith('null', pos)) {
      pos += 4;
      return null;
    }
    NUMBER.lastIndex = pos;
    const match = NUMBER.exec(text);
    if (!match) return fail();
    pos += match[0].length;
    return new JsonNumber(match[0]);
  };

  const value = parseValue();
  skipSpace();
  if (pos !== text.length) fail();
  return value;
}
