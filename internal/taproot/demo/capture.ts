/**
 * Captures the documentation screenshots from a running demo server (see run.sh)
 * with playwright-core driving the installed Chrome. Each page is captured at a
 * desktop size as <name>.png and at a phone size as <name>-mobile.png. Every
 * capture is checked for personal data before its screenshot is kept.
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
import { privacyProblems } from './privacy';

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

// run.sh sets TZ=Etc/UTC, but Go on Windows reads the zone from the system, not TZ, so the
// server could label host-zone times as UTC. Refuse rather than capture the host's zone.
const { info } = await api<{ info: { utcOffsetSeconds: number } }>('/settings');
if (info.utcOffsetSeconds !== 0) {
  throw new Error(`the server's clock is ${info.utcOffsetSeconds}s from UTC; set the host zone to UTC to capture`);
}
const { logs } = await api<{ logs: { requestId: string; status: number; model: string }[] }>(
  '/request-logs?limit=20'
);
const detail = logs.find((l) => l.status === 200 && l.model.startsWith('claude'));
if (!detail) throw new Error('no successful Claude request log to show');

type Page = import('playwright-core').Page;

interface Shot {
  /** Written as <name>.png and <name>-mobile.png. */
  name: string;
  route: string;
  theme?: 'dark' | 'light';
  /** Desktop viewport height; the default is 900. */
  height?: number;
  /** Runs after the page is ready, before the screenshot. */
  act?: (page: Page) => Promise<void>;
}

const shots: Shot[] = [
  { name: 'accounts', route: '/accounts' },
  {
    name: 'accounts-manage',
    route: '/accounts',
    act: async (page) => {
      await page.getByRole('button', { name: 'Manage' }).first().click();
      await page.getByRole('dialog').waitFor();
    },
  },
  { name: 'usage-overview', route: '/usage?tab=overview&range=30d' },
  {
    name: 'usage-devices',
    route: '/usage?tab=devices&range=30d',
    // A taller desktop viewport fits the cache TTL check and the devices table below it.
    height: 1400,
  },
  { name: 'usage-sessions', route: '/usage?tab=sessions&range=7d' },
  { name: 'request-logs', route: '/request-logs' },
  { name: 'request-log-detail', route: `/request-logs?requestId=${encodeURIComponent(detail.requestId)}` },
  { name: 'routing', route: '/routing' },
  { name: 'backups', route: '/backups' },
  { name: 'access', route: '/access', height: 1480 },
  { name: 'settings', route: '/settings' },
  { name: 'accounts-light', route: '/accounts', theme: 'light' },
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

/** Returns every piece of text on the page that is not one of the demo's synthetic values. */
async function pageProblems(page: Page): Promise<string[]> {
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
  return privacyProblems(text, args.deny);
}

mkdirSync(args.out, { recursive: true });
const browser = await chromium.launch(
  args.chrome ? { executablePath: args.chrome } : { channel: 'chrome' }
);
const failures: string[] = [];

interface Size {
  suffix: string;
  options: (shot: Shot) => import('playwright-core').BrowserContextOptions;
}

const sizes: Size[] = [
  { suffix: '', options: (shot) => ({ viewport: { width: 1440, height: shot.height ?? 900 } }) },
  {
    suffix: '-mobile',
    options: () => ({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true }),
  },
];

try {
  for (const shot of shots) {
    for (const size of sizes) {
      const file = `${shot.name}${size.suffix}.png`;
      const theme = shot.theme ?? 'dark';
      const context = await browser.newContext({
        ...size.options(shot),
        colorScheme: theme,
        reducedMotion: 'reduce',
        timezoneId: 'Etc/UTC',
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
      const problems = await pageProblems(page);
      if (problems.length > 0) {
        failures.push(`${file}: ${[...new Set(problems)].join(', ')}`);
      } else {
        await page.screenshot({ path: join(args.out, file) });
        console.log(`captured ${file}`);
      }
      await context.close();
    }
  }
} finally {
  await browser.close();
}
if (failures.length > 0) {
  console.error(`not captured, because the page showed non-demo data:\n${failures.join('\n')}`);
  process.exit(1);
}
