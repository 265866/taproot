import { describe, expect, test } from 'bun:test';
import {
  accessUpdate,
  adminLock,
  allowDefaults,
  allowUpdate,
  changeCount,
  deviceName,
  draftFrom,
  isIdle,
  isNew,
  layoutDevices,
  newAddress,
  proxyLock,
  rebaseDraft,
} from '@/features/taproot-access/model/accessModel';
import { en } from '@/i18n/resources';
import {
  normalizeAccessDevices,
  type AccessDevice,
  type AccessDevices,
  type AccessRequest,
  type TaprootMe,
} from '@/services/api/taproot';
import { danaRequest, data, daysAgo, NOW, onDesktop } from './fixtures/accessDevices';

const loopback: TaprootMe = {
  login: '',
  displayName: '',
  node: '127.0.0.1',
  via: 'loopback',
  admin: true,
};

const ids = (devices: AccessDevice[]) => devices.map((d) => d.id);

describe('layout', () => {
  test('yours, servers, people with a granted device, the fold, and removed devices', () => {
    const layout = layoutDevices(data, onDesktop, '', NOW);
    expect(layout.sections.map((s) => [s.kind, s.login, s.ownerName, s.outside])).toEqual([
      ['yours', '', '', false],
      ['servers', '', '', false],
      ['person', 'alice@example.org', 'Alice', true],
      ['person', 'bob@github', '', false],
    ]);
    expect(layout.sections.map((s) => ids(s.devices))).toEqual([
      ['nDESKCNTRL', 'nWORKCNTRL'],
      ['nCICNTRL', 'nHOSTCNTRL'],
      ['nALIMBPCNTRL'],
      ['nBOBCNTRL'],
    ]);
    // Sharee devices, other people's devices without access, and idle ones fold away.
    expect(ids(layout.folded)).toEqual([
      'nTABCNTRL',
      'nBOBPCNTRL',
      'nCAROLCNTRL',
      'nDANA0001CNTRL',
      'nDANA0002CNTRL',
      'nDANA0003CNTRL',
    ]);
    expect(layout.removed && ids(layout.removed.devices)).toEqual(['nOLDCNTRL']);
  });

  test('from loopback, your logins are those of in-tailnet admins', () => {
    const layout = layoutDevices(data, loopback, '', NOW);
    expect(ids(layout.sections[0].devices)).toEqual(['nDESKCNTRL', 'nWORKCNTRL']);
    // An outside admin's login does not make its owner's devices yours.
    const outsideOnly = { ...data, devices: data.devices.filter((d) => d.id !== 'nDESKCNTRL') };
    const sections = layoutDevices(outsideOnly, loopback, '', NOW).sections;
    expect(sections.some((s) => s.kind === 'yours')).toBe(false);
  });

  test('search matches names, labels, logins, owners, IDs, and tags, not OS or MagicDNS', () => {
    const found = (q: string) => {
      const layout = layoutDevices(data, onDesktop, q, NOW);
      return [
        ...layout.sections.flatMap((s) => ids(s.devices)),
        ...ids(layout.folded),
        ...(layout.removed ? ids(layout.removed.devices) : []),
      ];
    };
    expect(found('work laptop')).toEqual(['nWORKCNTRL', 'nALIMBPCNTRL']);
    expect(found('Work laptop alice@example')).toEqual(['nALIMBPCNTRL']);
    expect(found('dana')).toEqual(['nDANA0001CNTRL', 'nDANA0002CNTRL', 'nDANA0003CNTRL']);
    expect(found('tag:ci')).toEqual(['nCICNTRL']);
    expect(found('nBOBP')).toEqual(['nBOBPCNTRL']);
    expect(found('bob@github')).toEqual(['nBOBCNTRL', 'nBOBPCNTRL']);
    expect(found('macos')).toEqual([]);
    expect(found('tailnet-0000')).toEqual([]);
  });
});

describe('names, badges, and locks', () => {
  test('label, then Tailscale name, then the owner and the ID before CNTRL', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id)!;
    expect(deviceName(byId('nALIMBPCNTRL'))).toEqual({ kind: 'named', name: 'Work laptop' });
    expect(deviceName(byId('nBOBCNTRL'))).toEqual({ kind: 'named', name: 'bob-laptop' });
    expect(deviceName(byId('nDANA0002CNTRL'))).toEqual({
      kind: 'unnamed',
      owner: 'dana',
      suffix: '0002',
    });
    expect(deviceName({ ...byId('nDANA0002CNTRL'), ownerName: '' })).toMatchObject({
      owner: 'dana@example.com',
    });
    expect(en.taproot_access.unnamed).toBe("{{owner}}'s device ·{{suffix}}");
  });

  test('New marks a recent device without access; idle means offline for 30 days', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id)!;
    expect(isNew(byId('nWORKCNTRL'), NOW)).toBe(true);
    expect(isNew({ ...byId('nCAROLCNTRL'), created: daysAgo(8) }, NOW)).toBe(false);
    expect(isNew({ ...byId('nCAROLCNTRL'), proxy: true }, NOW)).toBe(false);
    expect(isNew(byId('nOLDCNTRL'), NOW)).toBe(false);
    expect(isIdle(byId('nTABCNTRL'), NOW)).toBe(true);
    expect(isIdle(byId('nDANA0002CNTRL'), NOW)).toBe(false);
    expect(isIdle(byId('nDANA0001CNTRL'), NOW)).toBe(false);
  });

  test('Admin is locked for the server, your device, and outside devices without it', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id)!;
    expect(proxyLock(byId('nHOSTCNTRL'))).toBe('server_always');
    expect(adminLock(byId('nHOSTCNTRL'), onDesktop)).toBe('server_never');
    expect(adminLock(byId('nDESKCNTRL'), onDesktop)).toBe('needed_by_you');
    expect(adminLock(byId('nDESKCNTRL'), loopback)).toBeNull();
    expect(adminLock(byId('nDANA0001CNTRL'), onDesktop)).toBe('from_request');
    expect(proxyLock(byId('nDANA0001CNTRL'))).toBeNull();
    // An outside device keeps Admin it already has, so it can be switched off.
    expect(adminLock(byId('nALIMBPCNTRL'), onDesktop)).toBeNull();
    expect(adminLock(byId('nWORKCNTRL'), onDesktop)).toBeNull();
  });
});

describe('saving', () => {
  test('an untouched draft has no changes and saves the same lists, labels included', () => {
    const draft = draftFrom(data);
    expect(changeCount(data, draft)).toBe(0);
    expect(accessUpdate(data, draft, onDesktop)).toEqual({
      ok: true,
      update: {
        devices: [
          { id: 'nDESKCNTRL', name: 'alice-desktop', label: '', proxy: true, admin: true },
          { id: 'nBOBCNTRL', name: 'bob-laptop', label: '', proxy: true, admin: false },
          { id: 'nCICNTRL', name: 'ci-runner', label: '', proxy: true, admin: false },
          {
            id: 'nALIMBPCNTRL',
            name: 'alice-mbp.tailnet-1111.ts.net',
            label: 'Work laptop',
            proxy: true,
            admin: true,
          },
          { id: 'nOLDCNTRL', name: 'old-laptop', label: '', proxy: true, admin: false },
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
      'nALIMBPCNTRL',
    ]);
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

  test('Allow saves the saved grants plus the request, with the name as its label', () => {
    expect(allowDefaults(danaRequest)).toEqual({ name: "Dana's PC", proxy: true, admin: false });
    const draft = draftFrom(data);
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    const result = allowUpdate(data, danaRequest, {
      name: " Dana's PC ",
      proxy: true,
      admin: true,
    });
    expect(result).toEqual({
      ok: true,
      update: {
        devices: [
          { id: 'nDESKCNTRL', name: 'alice-desktop', label: '', proxy: true, admin: true },
          { id: 'nBOBCNTRL', name: 'bob-laptop', label: '', proxy: true, admin: false },
          { id: 'nCICNTRL', name: 'ci-runner', label: '', proxy: true, admin: false },
          {
            id: 'nALIMBPCNTRL',
            name: 'alice-mbp.tailnet-1111.ts.net',
            label: 'Work laptop',
            proxy: true,
            admin: true,
          },
          { id: 'nOLDCNTRL', name: 'old-laptop', label: '', proxy: true, admin: false },
          { id: 'nDANA0001CNTRL', name: '', label: "Dana's PC", proxy: true, admin: true },
        ],
      },
    });
    // A request from a named device that keeps its Tailscale name saves no label.
    const carol: AccessRequest = { ...danaRequest, nodeId: 'nCAROLCNTRL', name: 'carol-ipad' };
    const named = allowUpdate(data, carol, allowDefaults(carol));
    expect(named.ok && named.update.devices.at(-1)).toEqual({
      id: 'nCAROLCNTRL',
      name: 'carol-ipad',
      label: '',
      proxy: true,
      admin: false,
    });
    expect(allowUpdate(data, danaRequest, { name: 'x', proxy: false, admin: false })).toEqual({
      ok: false,
      error: 'allow_nothing',
    });
    expect(
      allowUpdate(data, danaRequest, { name: 'é'.repeat(65), proxy: true, admin: false })
    ).toMatchObject({ ok: false, error: 'label_too_long' });
  });

  test('a reload after Allow keeps the switches still being edited', () => {
    const draft = draftFrom(data);
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    draft.addresses = [];
    const after: AccessDevices = {
      ...data,
      requests: [],
      devices: data.devices.map((d) =>
        d.id === 'nDANA0001CNTRL' ? { ...d, proxy: true, label: "Dana's PC" } : d
      ),
    };
    const next = rebaseDraft(data, draft, after);
    expect(next.devices.nBOBCNTRL).toEqual({ proxy: false, admin: false });
    expect(next.devices.nDANA0001CNTRL).toEqual({ proxy: true, admin: false });
    expect(next.addresses).toEqual([]);
    expect(changeCount(after, next)).toBe(2);
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

test('an older server without the new fields still reads as one shape', () => {
  const read = normalizeAccessDevices({
    enabled: true,
    devices: [{ id: 'n1CNTRL', name: 'laptop', proxy: true, admin: false, onTailnet: true }],
    addresses: [{ cidr: '192.0.2.20/32', note: '' }],
    forgetAfterHours: 24,
  });
  expect(read.requests).toEqual([]);
  expect(read.devices[0]).toMatchObject({
    label: '',
    ownerName: '',
    outside: false,
    online: false,
    lastSeen: null,
    tags: [],
  });
  expect(read.tailnetError).toBeUndefined();
});

test('every access error has copy, and the removed explanations are gone', () => {
  const copy = en.taproot_access as Record<string, unknown>;
  const errors = copy.errors as Record<string, string>;
  for (const key of [
    'lockout',
    'address_format',
    'address_duplicate',
    'allow_nothing',
    'label_too_long',
  ]) {
    expect(errors[key]?.length).toBeGreaterThan(0);
  }
  for (const key of [
    'description',
    'proxy_hint',
    'admin_hint',
    'tagged_hint',
    'gone_hint',
    'had',
    'addresses_hint',
    'unsaved_none',
  ]) {
    expect(copy[key]).toBeUndefined();
  }
  expect(copy.unsaved_one).toBe('1 unsaved change');
  expect(copy.gone_unknown).toBe('Missing');
});
