import type { AccessDevices } from '@/services/api/taproot';
import type { Pools } from '@/services/api/taprootStatus';

export type SetupStepId = 'account' | 'client' | 'devices';
export type SetupStepState = 'done' | 'current' | 'pending';

export interface SetupStep {
  id: SetupStepId;
  state: SetupStepState;
}

export interface FirstRunFacts {
  /** Any account, of any provider, signed in or not. */
  hasAccount: boolean;
  /** Any client request ever reached the proxy routes. */
  hasClientRequest: boolean;
  /** Any device besides loopback has Admin, or the guard is off and lets every caller in. */
  devicesApproved: boolean;
}

export const hasAccounts = (pools: Pools): boolean =>
  pools.pools.some((pool) => pool.totalAccounts > 0);

/**
 * Loopback is not a device, and the server's own node never has Admin, so any device with Admin
 * is one the user approved. With the guard off there is nothing to approve.
 */
export const devicesApproved = (access: AccessDevices): boolean =>
  !access.enabled || access.devices.some((device) => device.admin && !device.server);

/**
 * The checklist replaces the main column until the server has an account and has served a client
 * request: before then the window has nothing to show. Approving devices is optional, so it does
 * not hold the checklist up.
 */
export const showFirstRun = (facts: Pick<FirstRunFacts, 'hasAccount' | 'hasClientRequest'>) =>
  !facts.hasAccount || !facts.hasClientRequest;

/** Each step ticks itself off; the first one not done is current. */
export function setupSteps(facts: FirstRunFacts): SetupStep[] {
  const done: Record<SetupStepId, boolean> = {
    account: facts.hasAccount,
    client: facts.hasClientRequest,
    devices: facts.devicesApproved,
  };
  const order: SetupStepId[] = ['account', 'client', 'devices'];
  const current = order.find((id) => !done[id]);
  return order.map((id) => ({
    id,
    state: done[id] ? 'done' : id === current ? 'current' : 'pending',
  }));
}

/** The server's address as this browser reaches it; the proxy shares the dashboard's port. */
export function proxyBaseUrl(location: Pick<Location, 'origin'>): string {
  return location.origin.replace(/\/+$/, '');
}

/**
 * A caveat for the address the snippets use. Loopback works only on the server itself. An https
 * address on the default port is most likely a proxy such as `tailscale serve`, which taproot
 * cannot tell apart from loopback (docs/troubleshooting.md).
 */
export function addressHint(
  location: Pick<Location, 'protocol' | 'hostname' | 'port'>
): 'loopback' | 'proxy' | null {
  const host = location.hostname.replace(/^\[|\]$/g, '');
  if (host === 'localhost' || host === '::1' || /^127\./.test(host)) return 'loopback';
  if (location.protocol === 'https:' && location.port === '') return 'proxy';
  return null;
}

export function claudeCodeSnippet(baseUrl: string): string {
  return `export ANTHROPIC_BASE_URL="${baseUrl}"\nexport ANTHROPIC_AUTH_TOKEN="taproot"  # any value`;
}

export function codexSnippet(baseUrl: string): string {
  return [
    'model_provider = "taproot"',
    '',
    '[model_providers.taproot]',
    'name = "taproot"',
    `base_url = "${baseUrl}/v1"`,
    'wire_api = "responses"',
  ].join('\n');
}
