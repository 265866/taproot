// The privacy check of capture.ts: what text on a page is not one of the
// demo's synthetic values. It fails closed, so a value the demo adds later
// needs an entry here.

// The dashboard masks addresses as `a•••e@e•••.com`; masked ones are skipped.
const EMAIL = /[A-Za-z0-9._%+•-]+@[A-Za-z0-9•-]+(?:\.[A-Za-z0-9-]+)*/g;
const IPV4 = /\b\d{1,3}(?:\.\d{1,3}){3}\b/g;
// Three to eight colon-separated hex groups. A clock time such as 12:34:56
// matches too; only an address with "::" or eight groups counts.
const IPV6 = /\b[0-9a-f]{1,4}(?::[0-9a-f]{0,4}){2,7}\b/gi;
const MAGIC_DNS = /\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.ts\.net\b/gi;
const STABLE_ID = /\bn[A-Za-z0-9]{4,}CNTRL\b/g;
const HOME_PATH = /[A-Za-z]:\\Users\\|\/Users\/|\/home\/|\/var\/home\/|\/root\//;

// Tailscale's GitHub logins have the form <user>@github, so only the demo's
// users pass (tailnet.go and traffic.go).
const DEMO_GITHUB_LOGINS = new Set(['alice@github', 'bob@github', 'carol@github']);
const DEMO_TAILNETS = ['tailnet-0000.ts.net', 'tailnet-1111.ts.net'];
// The demo's stable IDs, such as nLAPTOP01CNTRL, are upper case and digits.
// Real ones mix upper and lower case.
const DEMO_STABLE_ID = /^n[A-Z0-9]+CNTRL$/;

/** Returns every piece of text that is not one of the demo's synthetic values. */
export function privacyProblems(text: string, deny: string[] = []): string[] {
  const problems: string[] = [];
  for (const m of text.match(EMAIL) ?? []) {
    if (m.includes('•')) continue;
    const domain = m.slice(m.indexOf('@') + 1).toLowerCase();
    const allowed =
      domain === 'example.com' || domain === 'example.org' || DEMO_GITHUB_LOGINS.has(m.toLowerCase());
    if (!allowed) problems.push(`address ${m}`);
  }
  for (const m of text.match(IPV4) ?? []) {
    // 192.0.2.0/24 is reserved for documentation (RFC 5737); the demo's address group uses it.
    if (m !== '127.0.0.1' && !m.startsWith('100.64.0.') && !m.startsWith('192.0.2.')) problems.push(`address ${m}`);
  }
  for (const m of text.match(IPV6) ?? []) {
    if (!m.includes('::') && m.split(':').length < 8) continue;
    // 2001:db8::/32 is reserved for documentation (RFC 3849).
    if (!m.toLowerCase().startsWith('2001:db8:')) problems.push(`address ${m}`);
  }
  for (const m of text.match(MAGIC_DNS) ?? []) {
    const name = m.toLowerCase();
    if (!DEMO_TAILNETS.some((t) => name === t || name.endsWith(`.${t}`))) problems.push(`name ${m}`);
  }
  for (const m of text.match(STABLE_ID) ?? []) {
    if (!DEMO_STABLE_ID.test(m)) problems.push(`node ${m}`);
  }
  if (HOME_PATH.test(text)) problems.push('a home directory path');
  const lower = text.toLowerCase();
  for (const word of deny) if (lower.includes(word)) problems.push(`denied text "${word}"`);
  return problems;
}
