/**
 * Masks email addresses inside arbitrary text, so screenshots and shared screens do not leak
 * account identities: `anthropic1@sl.example.org` becomes `an•••1@s•••.run`, and the auth file
 * `claude-ab12cd34-anthropic1@sl.example.org.json` becomes `claude-ab12cd34-an•••1@s•••.run.json`.
 *
 * What counts as an email: a local part, an at sign, and a domain of at least two labels whose last
 * label starts with a TLD (two or more letters). The at sign may be written `@`, `%40` (URLs and
 * access logs), `@` (JSON), or `&#64;` (HTML), and is kept as written. `alice@github` (a
 * Tailscale login), `user@localhost`, and package versions such as `react@19.3.1` have no such
 * domain and stay as they are.
 *
 * Every scan is linear: the address scanner looks a bounded distance either side of each at sign,
 * and the Meta file scanner reads each run of file-name characters once. A backtracking regex
 * would be quadratic on long base64 or `meta-meta-…` runs, and request bodies are masked whole.
 */

const DOTS = '•••';
const MAX_LOCAL = 64;
const MAX_DOMAIN = 253;

/**
 * Auth file prefixes the server writes before the email (`claude-<hash>-`, `codex-<hash>-`). The
 * dashboard often shows names without `.json`, so a prefix with a hash counts on its own.
 */
const FILE_PREFIX =
  /^(?:claude|codex|antigravity|gemini|xai|kimi|qwen|iflow|aistudio|devin|vertex)-([0-9a-f]{8,64}-)?/;

const AT_SIGN = /@|%40|\\u0040|&#0*64;|&#x0*40;/gi;
const LOCAL_CHAR = /[\p{L}\p{N}._%+'-]/u;
const DOMAIN_CHAR = /[\p{L}\p{N}.-]/u;
const LABEL_START = /^[\p{L}\p{N}]/u;
const TLD = /^\p{L}{2,63}/u;
const META_CHAR = /[A-Za-z0-9._-]/;
const WORD_CHAR = /\w/;
const META_TAIL = /-([0-9a-f]{16})(?:\.json)?$/i;

function maskLocal(local: string): string {
  const chars = [...local];
  const head = chars.slice(0, chars.length >= 6 ? 2 : chars.length >= 3 ? 1 : 0).join('');
  const tail = chars.length >= 4 ? chars[chars.length - 1] : '';
  return `${head}${DOTS}${tail}`;
}

function maskDomain(domain: string): string {
  const dot = domain.lastIndexOf('.');
  return `${domain.slice(0, 1)}${DOTS}${dot > 0 ? domain.slice(dot) : ''}`;
}

/** Masks one address already split at its at sign. The TLD stays readable; subdomains do not. */
export function maskAddress(local: string, domain: string, at = '@'): string {
  return `${maskLocal(local)}${at}${maskDomain(domain)}`;
}

/**
 * The address's domain at the start of `run` (the domain characters after an at sign), or null when
 * there is none. File names glue a suffix to the domain (`example.com-team.json`,
 * `gmail.com-my-project.json`); a TLD is letters only, so the last label ends where its letters
 * do, and a trailing `.json` label is the file extension. An empty label (`gmail..com`) is a typo,
 * not the end of the address.
 */
export function addressDomain(run: string): string | null {
  const labels = run.replace(/[.-]+$/, '').split('.');
  if (labels.length > 2 && labels[labels.length - 1].toLowerCase() === 'json') labels.pop();
  while (labels.length >= 2) {
    const tld = TLD.exec(labels[labels.length - 1]);
    if (tld) {
      labels[labels.length - 1] = tld[0];
      const valid =
        LABEL_START.test(labels[0]) && labels.every((label) => !label || LABEL_START.test(label));
      return valid ? labels.join('.') : null;
    }
    labels.pop();
  }
  return null;
}

/** One email in a text: the span `[from, to)` and what to show instead. */
export interface EmailMatch {
  from: number;
  to: number;
  masked: string;
}

/** Meta writes `meta-<email with @ as _>-<16 hex>.json`, so its file names carry no at sign. */
function findMetaFiles(text: string): EmailMatch[] {
  const out: EmailMatch[] = [];
  let runEnd = -1;
  let seen = -1;
  for (let p = text.indexOf('meta-'); p !== -1; p = text.indexOf('meta-', p + 1)) {
    if (p >= runEnd) {
      runEnd = p + 5;
      while (runEnd < text.length && META_CHAR.test(text[runEnd])) runEnd++;
    }
    // Only the first word-boundary `meta-` of a run can match; the rest share its tail.
    if (p < seen || (p > 0 && WORD_CHAR.test(text[p - 1]))) continue;
    seen = runEnd;
    const run = text.slice(p + 5, runEnd).replace(/[.-]+$/, '');
    const tail = META_TAIL.exec(run);
    if (!tail) continue;
    const middle = run.slice(0, tail.index);
    const split = middle.lastIndexOf('_');
    const domain = split > 0 ? addressDomain(middle.slice(split + 1)) : null;
    if (!domain || domain.length !== middle.length - split - 1) continue;
    out.push({
      from: p,
      to: p + 5 + tail.index + 17,
      masked: `meta-${maskAddress(middle.slice(0, split), domain, '_')}-${tail[1]}`,
    });
  }
  return out;
}

/** Every email-shaped span in `text`, in order and without overlaps. */
export function findEmails(text: string): EmailMatch[] {
  if (!text) return [];
  const matches = text.includes('meta-') ? findMetaFiles(text) : [];
  let cursor = 0;
  AT_SIGN.lastIndex = 0;
  for (let sign = AT_SIGN.exec(text); sign; sign = AT_SIGN.exec(text)) {
    const at = sign.index;
    const after = at + sign[0].length;
    let start = at;
    while (start > cursor && at - start < MAX_LOCAL && LOCAL_CHAR.test(text[start - 1])) start--;
    // A JSON escape glued to the address (`<user`, `\nuser`) is not part of it.
    if (start > 0 && text[start - 1] === '\\') {
      start += /^u[0-9a-f]{4}/i.test(text.slice(start, start + 5)) ? 5 : 1;
    }
    while (start < at && (text[start] === '.' || text[start] === "'")) start++;
    let end = after;
    while (end < text.length && end - after < MAX_DOMAIN && DOMAIN_CHAR.test(text[end])) end++;

    const local = text.slice(Math.min(start, at), at);
    const domain = addressDomain(text.slice(after, end));
    if (!domain || local.endsWith('.')) continue;
    if (!local) {
      // The second address of `x@y.com@z.com` lost its local part to the first; hide its domain.
      if (at === cursor && cursor > 0) {
        matches.push({ from: after, to: after + domain.length, masked: maskDomain(domain) });
        cursor = after + domain.length;
        AT_SIGN.lastIndex = cursor;
      }
      continue;
    }

    // Keep a server-written file prefix readable only where it is surely one.
    const isFileName = /\.json[.-]*$/i.test(text.slice(after + domain.length, end));
    const file = FILE_PREFIX.exec(local);
    const prefix = file && (isFileName || file[1]) ? file[0] : '';
    const kept = prefix.length < local.length ? prefix.length : 0;
    cursor = after + domain.length;
    AT_SIGN.lastIndex = cursor;
    matches.push({
      from: start + kept,
      to: cursor,
      masked: maskAddress(local.slice(kept), domain, sign[0]),
    });
  }
  return matches.sort((a, b) => a.from - b.from);
}

/** Masks every email-shaped substring in `text`. Already-masked text comes back unchanged. */
export function maskEmails(text: string): string {
  const matches = findEmails(text);
  if (matches.length === 0) return text;
  let out = '';
  let cursor = 0;
  for (const match of matches) {
    if (match.from < cursor) continue;
    out += text.slice(cursor, match.from) + match.masked;
    cursor = match.to;
  }
  return out + text.slice(cursor);
}
