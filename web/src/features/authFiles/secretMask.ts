const SECRET_KEY = /token|secret|password|passwd|cookie|api[_-]?key|private[_-]?key|authorization/i;
// Keys that name a token's kind, lifetime, or endpoint rather than hold one.
const NOT_SECRET_KEY = /type|expir|endpoint|ur[il]$|_at$|_in$/i;
const JSON_STRING = /"(?:[^"\\]|\\.)*"/y;
const KEY_COLON = /\s*:\s*/y;

export const SECRET_PLACEHOLDER = '••••••••';

const stringEnd = (text: string, start: number): number => {
  JSON_STRING.lastIndex = start;
  return JSON_STRING.test(text) ? JSON_STRING.lastIndex : text.length;
};

/** Where the JSON value starting at `start` ends: a string, a nested array or object, or a scalar. */
function valueEnd(text: string, start: number): number {
  let depth = 0;
  for (let i = start; i < text.length; i++) {
    const char = text[i];
    if (char === '"') {
      const end = stringEnd(text, i);
      if (depth === 0) return end;
      i = end - 1;
    } else if (char === '{' || char === '[') {
      depth++;
    } else if (char === '}' || char === ']') {
      if (depth === 0) return i;
      if (--depth === 0) return i + 1;
    } else if (depth === 0 && (char === ',' || char === '\n')) {
      return i;
    }
  }
  return text.length;
}

/**
 * Hides every value, whatever its type, under a token, secret, password, cookie, or key field in
 * credential JSON, nested or not. It works on text, so it also covers content that is not valid JSON.
 */
export function maskCredentialSecrets(text: string): string {
  let out = '';
  let copied = 0;
  let i = text.indexOf('"');
  while (i !== -1) {
    const end = stringEnd(text, i);
    KEY_COLON.lastIndex = end;
    const key = KEY_COLON.test(text) ? text.slice(i + 1, end - 1) : null;
    if (key !== null && SECRET_KEY.test(key) && !NOT_SECRET_KEY.test(key)) {
      const valueStart = KEY_COLON.lastIndex;
      out += `${text.slice(copied, valueStart)}"${SECRET_PLACEHOLDER}"`;
      copied = valueEnd(text, valueStart);
      i = text.indexOf('"', copied);
    } else {
      i = text.indexOf('"', end);
    }
  }
  return out + text.slice(copied);
}
