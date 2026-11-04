/**
 * Values that can hold an email, such as session ids, go into the address bar encoded, so a
 * screenshot or a shared screen does not show them. This hides, it does not protect: anyone with
 * the link can decode it, and the link still works when shared.
 */

const PREFIX = 'b64.';

function toBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function encodeUrlValue(value: string): string {
  return value ? PREFIX + toBase64Url(new TextEncoder().encode(value)) : '';
}

/** Decodes an encoded value; anything else, such as a link from before encoding, passes through. */
export function decodeUrlValue(value: string): string {
  if (!value.startsWith(PREFIX)) return value;
  try {
    const base64 = value.slice(PREFIX.length).replace(/-/g, '+').replace(/_/g, '/');
    const bytes = Uint8Array.from(atob(base64), (char) => char.charCodeAt(0));
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  } catch {
    return value;
  }
}
