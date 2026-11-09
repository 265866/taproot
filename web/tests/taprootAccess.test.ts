import { describe, expect, test } from 'bun:test';
import {
  accessUpdate,
  adminLock,
  changeCount,
  draftFrom,
  groupDevices,
  isNew,
  newAddress,
  proxyLock,
} from '@/features/taproot-access/model/accessModel';
import { en } from '@/i18n/resources';
import type { AccessDevice, AccessDevices, TaprootMe } from '@/services/api/taproot';

const NOW = Date.parse('2026-11-08T12:00:00Z');
const daysAgo = (days: number) => new Date(NOW - days * 86_400_000).toISOString();

const device = (over: Partial<AccessDevice> & { id: string; name: string }): AccessDevice => ({
  dnsName: `${over.name}.tailnet-0000.ts.net`,
  login: 'alice@github',
  tags: [],
  os: 'linux',
  created: daysAgo(100),
  server: false,
  onTailnet: true,
  missingSince: null,
  proxy: false,
  admin: false,
  ...over,
});

const data: AccessDevices = {
  enabled: true,
  forgetAfterHours: 24,
  addresses: [{ cidr: '192.0.2.20/32', note: 'Agents on the server host' }],
  devices: [
    device({ id: 'nHOSTCNTRL', name: 'taproot-host', server: true, proxy: true }),
    device({ id: 'nDESKCNTRL', name: 'alice-desktop', proxy: true, admin: true }),
    device({ id: 'nWORKCNTRL', name: 'alice-work-laptop', os: 'macOS', created: daysAgo(0.01) }),
    device({ id: 'nBOBCNTRL', name: 'bob-laptop', login: 'bob@github', proxy: true }),
    device({ id: 'nCAROLCNTRL', name: 'carol-ipad', login: 'carol@github', created: daysAgo(3) }),
    device({ id: 'nCICNTRL', name: 'ci-runner', login: '', tags: ['tag:ci'], proxy: true }),
    device({
      id: 'nOLDCNTRL',
      name: 'old-laptop',
      login: '',
      dnsName: '',
      onTailnet: false,
      created: null,
      missingSince: daysAgo(0.5),
      proxy: true,
    }),
  ],
};

const onDesktop: TaprootMe = {
  login: 'Alice@GitHub',
  displayName: 'Alice',
  node: 'alice-desktop.tailnet-0000.ts.net',
  nodeId: 'nDESKCNTRL',
  via: 'tailnet',
  proxy: true,
  admin: true,
};
const loopback: TaprootMe = {
  login: '',
  displayName: '',
  node: '127.0.0.1',
  via: 'loopback',
  admin: true,
};

const names = (devices: AccessDevice[]) => devices.map((d) => d.name);

describe('grouping', () => {
  test('puts your own login first, then people, tagged devices, and gone ones', () => {
    const groups = groupDevices(data, onDesktop);
    expect(groups.map((g) => [g.kind, g.login, g.own])).toEqual([
      ['owner', 'alice@github', true],
      ['owner', 'bob@github', false],
      ['owner', 'carol@github', false],
      ['tagged', '', false],
      ['gone', '', false],
    ]);
    expect(names(groups[0].devices)).toEqual([
      'alice-desktop',
      'alice-work-laptop',
      'taproot-host',
    ]);
    expect(names(groups[3].devices)).toEqual(['ci-runner']);
    expect(names(groups[4].devices)).toEqual(['old-laptop']);
  });

  test('from loopback no group is yours, and logins sort by name', () => {
    const groups = groupDevices(data, loopback);
    expect(groups.every((g) => !g.own)).toBe(true);
    expect(groups.filter((g) => g.kind === 'owner').map((g) => g.login)).toEqual([
      'alice@github',
      'bob@github',
      'carol@github',
    ]);
  });

  test('search matches names, IDs, owners, and OS, and drops empty groups', () => {
    expect(groupDevices(data, onDesktop, 'BOB').map((g) => names(g.devices))).toEqual([
      ['bob-laptop'],
    ]);
    expect(groupDevices(data, onDesktop, 'nci').map((g) => g.kind)).toEqual(['tagged']);
    expect(groupDevices(data, onDesktop, 'macos alice').map((g) => names(g.devices))).toEqual([
      ['alice-work-laptop'],
    ]);
    expect(groupDevices(data, onDesktop, 'nothing-like-this')).toEqual([]);
  });
});

describe('badges and locks', () => {
  test('New marks a device from the last 7 days with no permission', () => {
    const byName = (name: string) => data.devices.find((d) => d.name === name)!;
    expect(isNew(byName('alice-work-laptop'), NOW)).toBe(true);
    expect(isNew(byName('carol-ipad'), NOW)).toBe(true);
    expect(isNew({ ...byName('carol-ipad'), created: daysAgo(8) }, NOW)).toBe(false);
    expect(isNew({ ...byName('carol-ipad'), proxy: true }, NOW)).toBe(false);
    expect(isNew({ ...byName('carol-ipad'), created: null }, NOW)).toBe(false);
    expect(isNew({ ...byName('taproot-host'), proxy: false, created: daysAgo(1) }, NOW)).toBe(
      false
    );
    expect(isNew(byName('old-laptop'), NOW)).toBe(false);
  });

  test('the server has Proxy always and Admin never; your own device keeps Admin', () => {
    const [host, desktop, work] = data.devices;
    expect(proxyLock(host)).toBe('server_always');
    expect(adminLock(host, onDesktop)).toBe('server_never');
    expect(adminLock(desktop, onDesktop)).toBe('needed_by_you');
    expect(proxyLock(desktop)).toBeNull();
    expect(adminLock(desktop, loopback)).toBeNull();
    expect(adminLock(work, onDesktop)).toBeNull();
  });
});

describe('saving', () => {
  test('an untouched draft has no changes and saves the same lists', () => {
    const draft = draftFrom(data);
    expect(changeCount(data, draft)).toBe(0);
    expect(accessUpdate(data, draft, onDesktop)).toEqual({
      ok: true,
      update: {
        devices: [
          { id: 'nDESKCNTRL', name: 'alice-desktop', proxy: true, admin: true },
          { id: 'nBOBCNTRL', name: 'bob-laptop', proxy: true, admin: false },
          { id: 'nCICNTRL', name: 'ci-runner', proxy: true, admin: false },
          { id: 'nOLDCNTRL', name: 'old-laptop', proxy: true, admin: false },
        ],
        addresses: [{ cidr: '192.0.2.20/32', note: 'Agents on the server host' }],
      },
    });
  });

  test('switches, forgetting, and address edits each count once', () => {
    const draft = draftFrom(data);
    draft.devices.nWORKCNTRL = { proxy: true, admin: false };
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    draft.forget.push('nOLDCNTRL');
    draft.addresses = [{ cidr: '192.0.2.21', note: '' }];
    expect(changeCount(data, draft)).toBe(5);
    const result = accessUpdate(data, draft, onDesktop);
    expect(result.ok && result.update.devices.map((d) => d.id)).toEqual([
      'nDESKCNTRL',
      'nWORKCNTRL',
      'nCICNTRL',
    ]);
    expect(result.ok && result.update.addresses).toEqual([{ cidr: '192.0.2.21', note: '' }]);

    // Switching a change back undoes it.
    draft.devices.nBOBCNTRL = { proxy: true, admin: false };
    expect(changeCount(data, draft)).toBe(4);
  });

  test('never sends the server, and refuses to drop your own Admin', () => {
    const draft = draftFrom(data);
    draft.devices.nHOSTCNTRL = { proxy: true, admin: true };
    const result = accessUpdate(data, draft, onDesktop);
    expect(result.ok && result.update.devices.some((d) => d.id === 'nHOSTCNTRL')).toBe(false);

    draft.devices.nDESKCNTRL = { proxy: true, admin: false };
    expect(accessUpdate(data, draft, onDesktop)).toEqual({ ok: false, error: 'lockout' });
    expect(accessUpdate(data, draft, loopback).ok).toBe(true);
  });

  test('a new address must look like an address and not repeat one', () => {
    const draft = draftFrom(data);
    expect(newAddress(' 192.0.2.30 ', ' agents ', draft)).toEqual({
      ok: true,
      update: { cidr: '192.0.2.30', note: 'agents' },
    });
    expect(newAddress('fd00::5/128', '', draft).ok).toBe(true);
    expect(newAddress('192.0.2.20', '', draft)).toMatchObject({
      ok: false,
      error: 'address_duplicate',
    });
    for (const bad of ['laptop', '192.0.2', 'http://192.0.2.1', '']) {
      expect(newAddress(bad, '', draft)).toMatchObject({ ok: false, error: 'address_format' });
    }
  });
});

test('every access error has copy', () => {
  const errors = en.taproot_access.errors as Record<string, string>;
  for (const key of ['lockout', 'address_format', 'address_duplicate']) {
    expect(errors[key]?.length).toBeGreaterThan(0);
  }
});
