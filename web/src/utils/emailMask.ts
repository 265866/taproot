/**
 * Masks email addresses inside arbitrary text, so screenshots and shared screens do not leak
 * account identities: `anthropic1@sl.example.org` becomes `an•••1@s•••.run`, and the auth file
 * `claude-ab12cd34-anthropic1@sl.example.org.json` becomes `claude-ab12cd34-an•••1@s•••.run.json`.
 *
 * What counts as an email: a local part, `@`, and a domain of at least two labels whose last label
 * starts with a TLD (two or more letters). `alice@github` (a Tailscale login), `user@localhost`,
 * and package versions such as `react@19.3.1` have no such domain and stay as they are.
 *
 * The scanner jumps from `@` to `@` and looks a bounded distance either side, so masking a
 * multi-megabyte request body stays linear; a backtracking regex would not on long base64 runs.
 */

const DOTS = '•••';
const MAX_LOCAL = 64;
const MAX_DOMAIN = 253;

/** Auth file prefixes the server writes before the email (`claude-<hash>-`, `codex-<hash>-`). */
const FILE_PREFIX =
  /^(?:claude|codex|antigravity|gemini|xai|kimi|qwen|iflow|aistudio|devin|vertex)-(?:[0-9a-f]{8,64}-)?/;

/** Meta writes `meta-<email with @ as _>-<16 hex>.json`, so its file names carry no `@`. */
const META_FILE =
  /\bmeta-([A-Za-z0-9._-]+)_([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+)-([0-9a-f]{16})\.json/g;

const LOCAL_CHAR = /[A-Za-z0-9._%+-]/;
const DOMAIN_CHAR = /[A-Za-z0-9.-]/;
const TLD = /^[A-Za-z]{2,63}/;

function maskLocal(local: string): string {
  const head = local.slice(0, local.length >= 6 ? 2 : local.length >= 3 ? 1 : 0);
  const tail = local.length >= 4 ? local.slice(-1) : '';
  return `${head}${DOTS}${tail}`;
}

/** Masks one address already split at its `@`. The TLD stays readable; subdomains do not. */
export function maskAddress(local: string, domain: string, at = '@'): string {
  const dot = domain.lastIndexOf('.');
  const tld = dot > 0 ? domain.slice(dot) : '';
  return `${maskLocal(local)}${at}${domain.slice(0, 1)}${DOTS}${tld}`;
}

/**
 * The address's domain at the start of `run` (the domain characters after an `@`), or null when
 * there is none. File names glue a suffix to the domain (`example.com-team.json`,
 * `gmail.com-my-project.json`); a TLD is letters only, so the last label ends where its letters
 * do, and a trailing `.json` label is the file extension.
 */
export function addressDomain(run: string): string | null {
  const labels = run.replace(/[.-]+$/, '').split('.');
  if (labels.length > 2 && labels[labels.length - 1].toLowerCase() === 'json') labels.pop();
  while (labels.length >= 2) {
    const tld = TLD.exec(labels[labels.length - 1]);
    if (tld) {
      labels[labels.length - 1] = tld[0];
      return labels.every((label) => /^[A-Za-z0-9]/.test(label)) ? labels.join('.') : null;
    }
    labels.pop();
  }
  return null;
}

function maskMetaFiles(text: string): string {
  if (!text.includes('meta-')) return text;
  return text.replace(
    META_FILE,
    (_match, local: string, domain: string, hash: string) =>
      `meta-${maskAddress(local, domain, '_')}-${hash}.json`
  );
}

/** Masks every email-shaped substring in `text`. Already-masked text comes back unchanged. */
export function maskEmails(text: string): string {
  if (!text) return text;
  const source = maskMetaFiles(text);
  let at = source.indexOf('@');
  if (at === -1) return source;

  let out = '';
  let cursor = 0;
  while (at !== -1) {
    let start = at;
    while (start > cursor && at - start < MAX_LOCAL && LOCAL_CHAR.test(source[start - 1])) start--;
    while (start < at && source[start] === '.') start++;
    let end = at + 1;
    while (end < source.length && end - at <= MAX_DOMAIN && DOMAIN_CHAR.test(source[end])) end++;

    const local = source.slice(start, at);
    const domain = local && !local.endsWith('.') ? addressDomain(source.slice(at + 1, end)) : null;
    if (!domain) {
      at = source.indexOf('@', at + 1);
      continue;
    }

    // Keep a server-written file prefix readable only inside a file name.
    const isFileName = /\.json[.-]*$/i.test(source.slice(at + 1 + domain.length, end));
    const prefix = isFileName ? (FILE_PREFIX.exec(local)?.[0] ?? '') : '';
    const kept = prefix.length < local.length ? prefix : '';
    out += source.slice(cursor, start) + kept + maskAddress(local.slice(kept.length), domain);
    cursor = at + 1 + domain.length;
    at = source.indexOf('@', cursor);
  }
  return out + source.slice(cursor);
}
