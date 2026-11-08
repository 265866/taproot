/**
 * Captures the README screenshots from a running demo server (see run.sh) with
 * playwright-core driving the installed Chrome. Every page is checked for
 * personal data before its screenshot is kept.
 *
 *   bun internal/taproot/demo/capture.ts --url http://127.0.0.1:8399 \
 *     --out docs/screenshots --playwright <dir of an installed playwright-core> \
 *     [--chrome <path to chrome>] [--deny <comma-separated strings>]
 *
 * playwright-core is not a project dependency; run.sh installs it into a
 * temporary directory and passes its path.
 */
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

interface Args {
  url: string;
  out: string;
  playwright: string;
  chrome?: string;
  deny: string[];
}

function parseArgs(argv: string[]): Args {
  const get = (name: string) => {
    const i = argv.indexOf(`--${name}`);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const url = get('url');
  const out = get('out');
  const playwright = get('playwright');
  if (!url || !out || !playwright) {
    console.error('usage: capture.ts --url <server> --out <dir> --playwright <playwright-core dir>');
    process.exit(2);
  }
  const deny = (get('deny') ?? '')
    .split(',')
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  return { url: url.replace(/\/$/, ''), out: resolve(out), playwright: resolve(playwright), chrome: get('chrome'), deny };
}

const args = parseArgs(process.argv.slice(2));
const { chromium } = (await import(pathToFileURL(join(args.playwright, 'index.mjs')).href)) as typeof import('playwright-core');

const api = async <T>(path: string): Promise<T> => {
  const res = await fetch(`${args.url}/taproot/api/v1${path}`, { headers: { 'X-Taproot-Request': '1' } });
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
  return (await res.json()) as T;
};

// The server fills in account emails on its first account refresh, about a
// minute after it starts. Until then the pages show bare auth indexes.
async function waitForAccounts(expected: number) {
  const deadline = Date.now() + 3 * 60_000;
  for (;;) {
    const { accounts } = await api<{ accounts: { email: string }[] }>('/accounts');
    if (accounts.length >= expected && accounts.every((a) => a.email)) return;
    if (Date.now() > deadline) throw new Error('accounts never got their emails from the server');
    await Bun.sleep(2000);
  }
}

await waitForAccounts(5);
const { logs } = await api<{ logs: { requestId: string; status: number; model: string }[] }>(
  '/request-logs?limit=20'
);
const detail = logs.find((l) => l.status === 200 && l.model.startsWith('claude'));
if (!detail) throw new Error('no successful Claude request log to show');

type Page = import('playwright-core').Page;

interface Shot {
  file: string;
  route: string;
  theme?: 'dark' | 'light';
  /** Viewport height; the default is 900. */
  height?: number;
  /** Runs after the page is ready, before the screenshot. */
  act?: (page: Page) => Promise<void>;
}

const shots: Shot[] = [
  { file: 'accounts.png', route: '/accounts' },
  {
    file: 'accounts-manage.png',
    route: '/accounts',
    act: async (page) => {
      await page.getByRole('button', { name: 'Manage' }).first().click();
      await page.getByRole('dialog').waitFor();
    },
  },
  { file: 'usage-overview.png', route: '/usage?tab=overview&range=30d' },
  {
    file: 'usage-devices.png',
    route: '/usage?tab=devices&range=30d',
    // A taller viewport fits the cache TTL check and the devices table below it.
    height: 1400,
  },
  { file: 'usage-sessions.png', route: '/usage?tab=sessions&range=7d' },
  { file: 'request-logs.png', route: '/request-logs' },
  { file: 'request-log-detail.png', route: `/request-logs?requestId=${encodeURIComponent(detail.requestId)}` },
  { file: 'routing.png', route: '/routing' },
  { file: 'backups.png', route: '/backups' },
  { file: 'access.png', route: '/access', height: 1480 },
  { file: 'settings.png', route: '/settings' },
  { file: 'accounts-light.png', route: '/accounts', theme: 'light' },
];

async function waitUntilSettled(page: Page) {
  await page.waitForLoadState('networkidle');
  await page.waitForFunction(
    () => !document.querySelector('.loading-spinner, [aria-busy="true"], [class*="skeleton"]'),
    null,
    { timeout: 30_000 }
  );
  // Charts and sheets animate in.
  await page.waitForTimeout(1500);
}

// The dashboard masks addresses as `a•••e@e•••.com`; masked ones are skipped.
const EMAIL = /[A-Za-z0-9._%+•-]+@[A-Za-z0-9•-]+(?:\.[A-Za-z0-9-]+)*/g;
const IPV4 = /\b\d{1,3}(?:\.\d{1,3}){3}\b/g;
const HOME_PATH = /[A-Za-z]:\\Users\\|\/Users\/|\/home\//;

/** Returns every piece of text on the page that is not one of the demo's synthetic values. */
async function privacyProblems(page: Page): Promise<string[]> {
  const text = await page.evaluate(() => {
    const parts = [document.body.innerText];
    for (const el of document.querySelectorAll('*')) {
      for (const attr of ['title', 'aria-label', 'value', 'placeholder', 'alt', 'href', 'id']) {
        const v = el.getAttribute(attr);
        if (v) parts.push(v);
      }
      if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) parts.push(el.value);
    }
    return parts.join('\n');
  });
  const problems: string[] = [];
  for (const m of text.match(EMAIL) ?? []) {
    if (m.includes('•')) continue;
    const domain = m.slice(m.indexOf('@') + 1).toLowerCase();
    if (!['example.com', 'example.org', 'github'].includes(domain)) problems.push(`address ${m}`);
  }
  for (const m of text.match(IPV4) ?? []) {
    // 192.0.2.0/24 is reserved for documentation (RFC 5737); the demo's address group uses it.
    if (m !== '127.0.0.1' && !m.startsWith('100.64.0.') && !m.startsWith('192.0.2.')) problems.push(`address ${m}`);
  }
  if (HOME_PATH.test(text)) problems.push('a home directory path');
  const lower = text.toLowerCase();
  for (const word of args.deny) if (lower.includes(word)) problems.push(`denied text "${word}"`);
  return problems;
}

mkdirSync(args.out, { recursive: true });
const browser = await chromium.launch(
  args.chrome ? { executablePath: args.chrome } : { channel: 'chrome' }
);
const failures: string[] = [];
try {
  for (const shot of shots) {
    const theme = shot.theme ?? 'dark';
    const context = await browser.newContext({
      viewport: { width: 1440, height: shot.height ?? 900 },
      colorScheme: theme,
      reducedMotion: 'reduce',
    });
    await context.addInitScript((t) => {
      localStorage.setItem('cli-proxy-theme', JSON.stringify({ state: { theme: t }, version: 0 }));
    }, theme);
    const page = await context.newPage();
    await page.goto(`${args.url}/#${shot.route}`);
    await waitUntilSettled(page);
    if (shot.act) {
      await shot.act(page);
      await waitUntilSettled(page);
    }
    const problems = await privacyProblems(page);
    if (problems.length > 0) {
      failures.push(`${shot.file}: ${[...new Set(problems)].join(', ')}`);
    } else {
      await page.screenshot({ path: join(args.out, shot.file) });
      console.log(`captured ${shot.file}`);
    }
    await context.close();
  }
} finally {
  await browser.close();
}
if (failures.length > 0) {
  console.error(`not captured, because the page showed non-demo data:\n${failures.join('\n')}`);
  process.exit(1);
}
