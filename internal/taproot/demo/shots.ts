// The screenshots capture.ts takes, for each demo scenario that run.sh seeds.

import type { Page } from 'playwright-core';

/** The run.sh scenarios that have screenshots. All clear has none. */
export const SCENARIOS = ['normal', 'degraded', 'first-run'] as const;
export type Scenario = (typeof SCENARIOS)[number];

export interface Shot {
  /** Written as <name>.png and <name>-mobile.png. */
  name: string;
  route: string;
  theme?: 'dark' | 'light';
  /** Desktop viewport height; the default is 900. */
  height?: number;
  /** On a desktop, grow the viewport to the page's whole height. */
  fullHeight?: boolean;
  /** Runs after the page is ready, before the screenshot. */
  act?: (page: Page) => Promise<void>;
}

export function isScenario(value: string): value is Scenario {
  return (SCENARIOS as readonly string[]).includes(value);
}

/** How many signed-in accounts the scenario's pages show once the server has read them. */
export function expectedAccounts(scenario: Scenario): number {
  return scenario === 'first-run' ? 0 : 5;
}

/**
 * The shots of one scenario. The normal demo has every page; the others add
 * only the Dashboard state they exist for. detailRequestId is the request log
 * the request-detail shot opens, needed for the normal scenario only.
 */
export function shotsFor(scenario: Scenario, detailRequestId?: string): Shot[] {
  switch (scenario) {
    case 'degraded':
      return [{ name: 'dashboard-degraded', route: '/', fullHeight: true }];
    case 'first-run':
      return [{ name: 'dashboard-first-run', route: '/', fullHeight: true }];
    case 'normal':
      if (!detailRequestId) throw new Error('the normal scenario needs a request log to show');
      return [
        { name: 'dashboard', route: '/', fullHeight: true },
        { name: 'accounts', route: '/accounts' },
        {
          name: 'accounts-manage',
          route: '/accounts',
          act: async (page) => {
            await page.getByRole('button', { name: 'Manage' }).first().click();
            // On a phone the closed navigation drawer is a dialog too.
            await page.locator('[role="dialog"]:not(#main-navigation)').waitFor();
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
        { name: 'request-log-detail', route: `/request-logs?requestId=${encodeURIComponent(detailRequestId)}` },
        { name: 'routing', route: '/routing' },
        { name: 'backups', route: '/backups' },
        { name: 'access', route: '/access', height: 1480 },
        { name: 'settings', route: '/settings' },
        { name: 'accounts-light', route: '/accounts', theme: 'light' },
      ];
  }
}
