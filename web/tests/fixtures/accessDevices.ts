/** The Access page's fixture: a messy tailnet with sharee, outside, idle, and removed devices. */
import type { AccessDevice, AccessDevices, AccessRequest, TaprootMe } from '@/services/api/taproot';

export const NOW = Date.parse('2026-11-08T12:00:00Z');
export const daysAgo = (days: number) => new Date(NOW - days * 86_400_000).toISOString();

export const device = (
  over: Partial<AccessDevice> & { id: string; name: string }
): AccessDevice => ({
  label: '',
  dnsName: over.name ? `${over.name}.tailnet-0000.ts.net` : '',
  login: 'alice@github',
  ownerName: 'Alice',
  tags: [],
  os: 'linux',
  created: daysAgo(100),
  outside: false,
  online: true,
  lastSeen: null,
  addresses: [],
  server: false,
  onTailnet: true,
  missingSince: null,
  proxy: false,
  admin: false,
  ...over,
});

const sharee = (n: number, over: Partial<AccessDevice> = {}) =>
  device({
    id: `nDANA000${n}CNTRL`,
    name: '',
    login: 'dana@example.com',
    ownerName: 'dana',
    os: '',
    created: null,
    outside: true,
    ...over,
  });

export const danaRequest: AccessRequest = {
  nodeId: 'nDANA0001CNTRL',
  login: 'dana@example.com',
  ownerName: 'dana',
  name: "Dana's PC",
  client: 'Claude Code',
  firstSeen: daysAgo(0.02),
  lastSeen: daysAgo(0.01),
  count: 7,
  outside: true,
  asked: 'admin',
  addresses: ['100.64.0.31', 'fd7a:115c:a1e0::31'],
};

export const data: AccessDevices = {
  enabled: true,
  forgetAfterHours: 24,
  addresses: [{ cidr: '192.0.2.20/32', note: 'Agents on the server host' }],
  requests: [danaRequest],
  devices: [
    device({ id: 'nHOSTCNTRL', name: 'taproot-host', server: true, proxy: true }),
    device({
      id: 'nDESKCNTRL',
      name: 'alice-desktop',
      proxy: true,
      admin: true,
      addresses: ['100.64.0.2', 'fd7a:115c:a1e0::2'],
    }),
    device({ id: 'nWORKCNTRL', name: 'alice-work-laptop', os: 'macOS', created: daysAgo(0.01) }),
    device({
      id: 'nTABCNTRL',
      name: 'alice-old-tablet',
      online: false,
      lastSeen: daysAgo(45),
    }),
    device({
      id: 'nBOBCNTRL',
      name: 'bob-laptop',
      login: 'bob@github',
      ownerName: '',
      proxy: true,
    }),
    device({ id: 'nBOBPCNTRL', name: 'bob-phone', login: 'bob@github', ownerName: '' }),
    device({ id: 'nCAROLCNTRL', name: 'carol-ipad', login: 'carol@github', created: daysAgo(3) }),
    device({
      id: 'nCICNTRL',
      name: 'ci-runner',
      login: '',
      ownerName: '',
      tags: ['tag:ci'],
      proxy: true,
    }),
    // A sharee's device is named by its IPv4, else its IPv6, else 4 characters of its ID.
    sharee(1, { addresses: ['100.64.0.31', 'fd7a:115c:a1e0::31'] }),
    sharee(2, { online: false, lastSeen: daysAgo(2), addresses: ['fd7a:115c:a1e0::32'] }),
    sharee(3, { online: false, lastSeen: daysAgo(40) }),
    device({
      id: 'nALIMBPCNTRL',
      name: 'alice-mbp.tailnet-1111.ts.net',
      dnsName: 'alice-mbp.tailnet-1111.ts.net',
      label: 'Work laptop',
      login: 'alice@example.org',
      ownerName: 'Alice',
      outside: true,
      proxy: true,
      admin: true,
    }),
    device({
      id: 'nOLDCNTRL',
      name: 'old-laptop',
      login: '',
      dnsName: '',
      onTailnet: false,
      online: false,
      created: null,
      missingSince: daysAgo(0.5),
      proxy: true,
    }),
  ],
};

export const onDesktop: TaprootMe = {
  login: 'Alice@GitHub',
  displayName: 'Alice',
  node: 'alice-desktop.tailnet-0000.ts.net',
  nodeId: 'nDESKCNTRL',
  via: 'tailnet',
  proxy: true,
  admin: true,
};
