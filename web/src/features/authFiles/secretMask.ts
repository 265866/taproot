const SECRET_KEY = /token|secret|password|passwd|cookie|api[_-]?key|private[_-]?key|authorization/i;
// Keys that name a token's kind, lifetime, or endpoint rather than hold one.
const NOT_SECRET_KEY = /type|expir|endpoint|ur[il]$|_at$|_in$/i;
const JSON_STRING_FIELD = /("((?:[^"\\]|\\.)*)"\s*:\s*)"(?:[^"\\]|\\.)*"/g;

export const SECRET_PLACEHOLDER = '••••••••';

/**
 * Hides the string values of token, secret, password, cookie, and key fields in credential JSON,
 * nested or not. It works on text, so it also covers content that is not valid JSON.
 */
export function maskCredentialSecrets(text: string): string {
  return text.replace(JSON_STRING_FIELD, (field, prefix: string, key: string) =>
    SECRET_KEY.test(key) && !NOT_SECRET_KEY.test(key) ? `${prefix}"${SECRET_PLACEHOLDER}"` : field
  );
}
