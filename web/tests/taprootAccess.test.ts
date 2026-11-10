import { describe, expect, test } from 'bun:test';
import {
  accessUpdate,
  adminLock,
  allowDefaults,
  allowUpdate,
  canRename,
  changeCount,
  deviceName,
  draftFrom,
  isIdle,
  isNew,
  labelFor,
  layoutDevices,
  newAddress,
  proxyLock,
  rebaseDraft,
  runAllow,
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
      // Ordered as shown: dana's device ·0003, then · 100.64.0.31, then · fd7a:….
      'nDANA0003CNTRL',
      'nDANA0001CNTRL',
      'nDANA0002CNTRL',
    ]);
    expect(layout.removed && ids(layout.removed.devices)).toEqual(['nOLDCNTRL']);
  });

  test('IP labels sort by number, not as text', () => {
    const at = (id: string, ip: string) => ({
      ...data.devices.find((d) => d.id === id)!,
      addresses: [ip],
    });
    const folded = layoutDevices(
      {
        ...data,
        devices: [at('nDANA0001CNTRL', '100.64.0.31'), at('nDANA0002CNTRL', '100.64.0.4')],
      },
      onDesktop,
      '',
      NOW
    ).folded;
    expect(ids(folded)).toEqual(['nDANA0002CNTRL', 'nDANA0001CNTRL']);
  });

  test('from loopback, your logins are those of in-tailnet admins', () => {
    const layout = layoutDevices(data, loopback, '', NOW);
    expect(ids(layout.sections[0].devices)).toEqual(['nDESKCNTRL', 'nWORKCNTRL']);
    // An outside admin's login does not make its owner's devices yours.
    const outsideOnly = { ...data, devices: data.devices.filter((d) => d.id !== 'nDESKCNTRL') };
    const sections = layoutDevices(outsideOnly, loopback, '', NOW).sections;
    expect(sections.some((s) => s.kind === 'yours')).toBe(false);
  });

  test('search matches names, labels, logins, owners, IDs, tags, and IPs, not OS or MagicDNS', () => {
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
    expect(found('dana')).toEqual(['nDANA0003CNTRL', 'nDANA0001CNTRL', 'nDANA0002CNTRL']);
    expect(found('tag:ci')).toEqual(['nCICNTRL']);
    expect(found('nBOBP')).toEqual(['nBOBPCNTRL']);
    expect(found('bob@github')).toEqual(['nBOBCNTRL', 'nBOBPCNTRL']);
    expect(found('100.64.0.31')).toEqual(['nDANA0001CNTRL']);
    expect(found('fd7a:115c:a1e0::32')).toEqual(['nDANA0002CNTRL']);
    expect(found('100.64.0.2')).toEqual(['nDESKCNTRL']);
    expect(found('macos')).toEqual([]);
    expect(found('tailnet-0000')).toEqual([]);
  });
});

describe('names, badges, and locks', () => {
  test('label, then Tailscale name, then the owner and the ID before CNTRL', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id)!;
    expect(deviceName(byId('nALIMBPCNTRL'))).toEqual({ kind: 'named', name: 'Work laptop' });
    expect(deviceName(byId('nBOBCNTRL'))).toEqual({ kind: 'named', name: 'bob-laptop' });
    expect(deviceName(byId('nDANA0001CNTRL'))).toEqual({
      kind: 'unnamed',
      owner: 'dana',
      address: '100.64.0.31',
      suffix: '0001',
    });
    // The IPv4 wins wherever tailscaled lists it; without one, the IPv6.
    expect(
      deviceName({ ...byId('nDANA0001CNTRL'), addresses: ['fd7a:115c:a1e0::31', '100.64.0.31'] })
    ).toMatchObject({ address: '100.64.0.31' });
    expect(deviceName(byId('nDANA0002CNTRL'))).toMatchObject({ address: 'fd7a:115c:a1e0::32' });
    // Without an IP, 4 characters of the stable ID before CNTRL.
    expect(deviceName(byId('nDANA0003CNTRL'))).toEqual({
      kind: 'unnamed',
      owner: 'dana',
      address: '',
      suffix: '0003',
    });
    expect(deviceName({ ...byId('nDANA0002CNTRL'), ownerName: '' })).toMatchObject({
      owner: 'dana@example.com',
    });
    expect(en.taproot_access.unnamed).toBe("{{owner}}'s device · {{address}}");
    expect(en.taproot_access.unnamed_id).toBe("{{owner}}'s device ·{{suffix}}");
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

  test('Admin is locked for the server and your device only', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id)!;
    expect(proxyLock(byId('nHOSTCNTRL'))).toBe('server_always');
    expect(adminLock(byId('nHOSTCNTRL'), onDesktop)).toBe('server_never');
    expect(adminLock(byId('nDESKCNTRL'), onDesktop)).toBe('needed_by_you');
    expect(adminLock(byId('nDESKCNTRL'), loopback)).toBeNull();
    // A device outside the tailnet switches like any other, with or without Admin already.
    expect(adminLock(byId('nDANA0001CNTRL'), onDesktop)).toBeNull();
    expect(proxyLock(byId('nDANA0001CNTRL'))).toBeNull();
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
        // The server refuses the save if the lists changed since this version.
        version: '0123456789abcdef',
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

  test('Allow saves only the request’s device, with the name as its label', () => {
    expect(allowDefaults(danaRequest)).toEqual({ name: "Dana's PC", proxy: true, admin: false });
    const draft = draftFrom(data);
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    const result = allowUpdate(data, danaRequest, {
      name: " Dana's PC ",
      proxy: true,
      admin: true,
    });
    // Other devices are left as the server has them, so a page loaded before another save
    // cannot undo it.
    expect(result).toEqual({
      ok: true,
      update: {
        device: { id: 'nDANA0001CNTRL', name: '', label: "Dana's PC", proxy: true, admin: true },
      },
    });
    // A request from a named device that keeps its Tailscale name saves no label.
    const carol: AccessRequest = { ...danaRequest, nodeId: 'nCAROLCNTRL', name: 'carol-ipad' };
    const named = allowUpdate(data, carol, allowDefaults(carol));
    expect(named.ok && named.update.device).toEqual({
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

  test('Allow starts from the device: Proxy on, and Admin only when it already has it', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id);
    const request = (nodeId: string): AccessRequest => ({ ...danaRequest, nodeId, name: '' });
    // A Proxy-only device that opened the dashboard: Allow as shown keeps Proxy, adds nothing.
    expect(allowDefaults(request('nBOBCNTRL'), byId('nBOBCNTRL'))).toEqual({
      name: '',
      proxy: true,
      admin: false,
    });
    // A device with Admin keeps it.
    expect(allowDefaults(request('nALIMBPCNTRL'), byId('nALIMBPCNTRL'))).toMatchObject({
      proxy: true,
      admin: true,
    });
    // A device without access, or one the page does not list, starts with Proxy only.
    expect(allowDefaults(request('nWORKCNTRL'), byId('nWORKCNTRL'))).toMatchObject({
      proxy: true,
      admin: false,
    });
    const bobAllow = allowUpdate(
      data,
      request('nBOBCNTRL'),
      allowDefaults(request('nBOBCNTRL'), byId('nBOBCNTRL'))
    );
    expect(bobAllow.ok && bobAllow.update.device).toEqual({
      id: 'nBOBCNTRL',
      name: 'bob-laptop',
      label: '',
      proxy: true,
      admin: false,
    });
    expect(
      allowUpdate(data, danaRequest, { name: 'Dana\u202ePC', proxy: true, admin: false })
    ).toMatchObject({ ok: false, error: 'label_invalid' });
  });

  test('a rename is one change and saves the label with the switches', () => {
    const draft = draftFrom(data);
    draft.labels.nBOBCNTRL = 'Bob laptop';
    expect(changeCount(data, draft)).toBe(1);
    // A switch on the same row is still one changed row.
    draft.devices.nBOBCNTRL = { proxy: true, admin: true };
    expect(changeCount(data, draft)).toBe(1);
    // Clearing a label falls back to the Tailscale name.
    draft.labels.nALIMBPCNTRL = '';
    expect(changeCount(data, draft)).toBe(2);
    const result = accessUpdate(data, draft, onDesktop);
    expect(result.ok && result.update.devices.find((d) => d.id === 'nBOBCNTRL')).toEqual({
      id: 'nBOBCNTRL',
      name: 'bob-laptop',
      label: 'Bob laptop',
      proxy: true,
      admin: true,
    });
    expect(result.ok && result.update.devices.find((d) => d.id === 'nALIMBPCNTRL')?.label).toBe('');
    // A named device without a permission is not saved, so its name is no change.
    draft.labels.nWORKCNTRL = 'Work Mac';
    expect(changeCount(data, draft)).toBe(2);
    const dropped = accessUpdate(data, draft, onDesktop);
    expect(dropped.ok && dropped.update.devices.some((d) => d.id === 'nWORKCNTRL')).toBe(false);
    // Putting the old name back is no change.
    draft.labels.nWORKCNTRL = '';
    draft.labels.nALIMBPCNTRL = 'Work laptop';
    draft.labels.nBOBCNTRL = '';
    draft.devices.nBOBCNTRL = { proxy: true, admin: false };
    expect(changeCount(data, draft)).toBe(0);
  });

  test('only a device the draft gives a permission can be renamed', () => {
    const byId = (id: string) => data.devices.find((d) => d.id === id)!;
    const draft = draftFrom(data);
    expect(canRename(byId('nBOBCNTRL'), draft)).toBe(true);
    expect(canRename(byId('nWORKCNTRL'), draft)).toBe(false);
    expect(canRename(byId('nHOSTCNTRL'), draft)).toBe(false);
    // Turning a switch on in the draft makes the row renamable before the save.
    draft.devices.nWORKCNTRL = { proxy: false, admin: true };
    expect(canRename(byId('nWORKCNTRL'), draft)).toBe(true);
    // A staged name on a device switched off is not a change and is not saved; switching it
    // back on brings the name back.
    draft.devices.nWORKCNTRL = { proxy: false, admin: false };
    draft.labels.nBOBCNTRL = 'Bob laptop';
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    expect(canRename(byId('nBOBCNTRL'), draft)).toBe(false);
    expect(changeCount(data, draft)).toBe(1);
    const off = accessUpdate(data, draft, onDesktop);
    expect(off.ok && off.update.devices.some((d) => d.id === 'nBOBCNTRL')).toBe(false);
    draft.devices.nBOBCNTRL = { proxy: true, admin: false };
    expect(changeCount(data, draft)).toBe(1);
    const on = accessUpdate(data, draft, onDesktop);
    expect(on.ok && on.update.devices.find((d) => d.id === 'nBOBCNTRL')?.label).toBe('Bob laptop');
  });

  test('Allow keeps the owner’s label unless the field is edited', () => {
    // The device asked again from its browser, which guessed a different name.
    const labeled: AccessDevices = {
      ...data,
      devices: data.devices.map((d) =>
        d.id === 'nBOBCNTRL' ? { ...d, label: "Dana's work laptop" } : d
      ),
    };
    const bob = labeled.devices.find((d) => d.id === 'nBOBCNTRL');
    const request: AccessRequest = { ...danaRequest, nodeId: 'nBOBCNTRL', name: 'Windows PC' };
    const form = allowDefaults(request, bob);
    expect(form.name).toBe("Dana's work laptop");
    const kept = allowUpdate(labeled, request, form);
    expect(kept.ok && kept.update.device?.label).toBe(
      "Dana's work laptop"
    );
    const edited = allowUpdate(labeled, request, { ...form, name: 'Windows PC' });
    expect(edited.ok && edited.update.device?.label).toBe(
      'Windows PC'
    );
    // Without a label, the device's own name, then its Tailscale name.
    expect(
      allowDefaults(
        request,
        data.devices.find((d) => d.id === 'nBOBCNTRL')
      ).name
    ).toBe('Windows PC');
    // A label equal to the Tailscale name is kept as it is.
    const same: AccessDevices = {
      ...data,
      devices: data.devices.map((d) => (d.id === 'nBOBCNTRL' ? { ...d, label: 'bob-laptop' } : d)),
    };
    const sameForm = allowDefaults(
      request,
      same.devices.find((d) => d.id === 'nBOBCNTRL')
    );
    const sameAllow = allowUpdate(same, request, sameForm);
    expect(sameAllow.ok && sameAllow.update.device?.label).toBe(
      'bob-laptop'
    );
  });

  test('Allow saves, closes the request, then reloads, even when closing fails', async () => {
    const steps = (fail: 'save' | 'close' | null) => {
      const calls: string[] = [];
      const step = (name: 'save' | 'close' | 'reload') => async () => {
        calls.push(name);
        if (fail === name) throw new Error(`${name} failed`);
      };
      return { calls, steps: { save: step('save'), close: step('close'), reload: step('reload') } };
    };
    const ok = steps(null);
    expect(await runAllow(ok.steps)).toEqual({ kind: 'allowed' });
    expect(ok.calls).toEqual(['save', 'close', 'reload']);
    const notClosed = steps('close');
    expect(await runAllow(notClosed.steps)).toMatchObject({ kind: 'saved_not_closed' });
    expect(notClosed.calls).toEqual(['save', 'close', 'reload']);
    const notSaved = steps('save');
    expect(await runAllow(notSaved.steps)).toMatchObject({ kind: 'save_failed' });
    expect(notSaved.calls).toEqual(['save']);
  });

  test('a typed name is checked the way the server checks it', () => {
    expect(labelFor('  Bob laptop ', 'bob-laptop')).toEqual({ ok: true, update: 'Bob laptop' });
    expect(labelFor(' bob-laptop ', 'bob-laptop')).toEqual({ ok: true, update: '' });
    expect(labelFor('', 'bob-laptop')).toEqual({ ok: true, update: '' });
    expect(labelFor('\t\u3000Bob\u00a0', '')).toEqual({ ok: true, update: 'Bob' });
    expect(labelFor('é'.repeat(64), '')).toMatchObject({ ok: true });
    expect(labelFor('é'.repeat(65), '')).toMatchObject({ ok: false, error: 'label_too_long' });
    for (const bad of ['tab\there', 'a\u0007', 'lap\u202etop', 'lap\u200btop', '\ufeffphone']) {
      expect(labelFor(bad, '')).toMatchObject({ ok: false, error: 'label_invalid' });
    }
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
    draft.labels.nCICNTRL = 'Runner';
    const next = rebaseDraft(data, draft, after);
    expect(next.labels.nCICNTRL).toBe('Runner');
    expect(next.labels.nDANA0001CNTRL).toBe("Dana's PC");
    expect(next.devices.nBOBCNTRL).toEqual({ proxy: false, admin: false });
    expect(next.devices.nDANA0001CNTRL).toEqual({ proxy: true, admin: false });
    expect(next.addresses).toEqual([]);
    expect(changeCount(after, next)).toBe(3);
  });

  test('a reload drops edits to anything the server changed in between', () => {
    // The admin turned on Proxy for Dana's device, then allowed its request with Admin too.
    const draft = draftFrom(data);
    draft.devices.nDANA0001CNTRL = { proxy: true, admin: false };
    draft.devices.nBOBCNTRL = { proxy: false, admin: false };
    draft.labels.nCICNTRL = 'Runner';
    draft.labels.nBOBCNTRL = 'Bob laptop';
    draft.addresses = [];
    const after: AccessDevices = {
      ...data,
      version: 'fedcba9876543210',
      devices: data.devices.map((d) => {
        if (d.id === 'nDANA0001CNTRL') return { ...d, proxy: true, admin: true };
        if (d.id === 'nBOBCNTRL') return { ...d, label: 'Bob' };
        return d;
      }),
      addresses: [{ cidr: '192.0.2.21/32', note: '' }],
    };
    const next = rebaseDraft(data, draft, after);
    // Saving the old edit would take back the Admin just granted.
    expect(next.devices.nDANA0001CNTRL).toEqual({ proxy: true, admin: true });
    expect(next.labels.nBOBCNTRL).toBe('Bob');
    expect(next.addresses).toEqual([{ cidr: '192.0.2.21/32', note: '' }]);
    // Edits to what nobody else changed stay.
    expect(next.devices.nBOBCNTRL).toEqual({ proxy: false, admin: false });
    expect(next.labels.nCICNTRL).toBe('Runner');
    expect(changeCount(after, next)).toBe(2);
    const saved = accessUpdate(after, next, onDesktop);
    expect(saved.ok && saved.update.version).toBe('fedcba9876543210');
    expect(saved.ok && saved.update.devices?.find((d) => d.id === 'nDANA0001CNTRL')).toMatchObject({
      proxy: true,
      admin: true,
    });
  });

  test('a new address may not cover Tailscale addresses', () => {
    const draft = draftFrom(data);
    for (const tailnet of [
      '100.64.0.0/10',
      '100.64.0.2',
      '100.127.255.255/32',
      '100.0.0.0/8',
      '64.0.0.0/2',
      '0.0.0.0/0',
      'fd7a:115c:a1e0::1',
      'fd7a:115c:a1e0:ab12::/64',
      'fd7a::/16',
      'fd00::/8',
      '::/0',
      '::ffff:100.64.0.5',
    ]) {
      expect(newAddress(tailnet, '', draft)).toMatchObject({
        ok: false,
        error: 'address_tailnet',
        detail: tailnet,
      });
    }
    for (const outside of ['100.128.0.0/9', '100.63.255.255', '10.0.0.0/8', 'fd7a:115c:a1e1::/48', 'fd00::/16']) {
      expect(newAddress(outside, '', draft).ok).toBe(true);
    }
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
    for (const bad of [
      'laptop',
      '192.0.2',
      'http://192.0.2.1',
      '',
      '999.1.1.1/24',
      '192.0.2.1/33',
      '192.0.2.01',
      'fd00::5/129',
      ':',
      '1::2::3',
      '1:2:3:4:5:6:7:8:9',
    ]) {
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
    addresses: [],
    tags: [],
  });
  expect(read.tailnetError).toBeUndefined();
  // Without a version, a save sends none, which an older server ignores anyway.
  expect(read.version).toBe('');
  const unversioned = accessUpdate(read, draftFrom(read), null);
  expect(unversioned.ok && 'version' in unversioned.update).toBe(false);
  expect(normalizeAccessDevices({ version: '0123456789abcdef' }).version).toBe('0123456789abcdef');
  const asked = normalizeAccessDevices({
    requests: [{ nodeId: 'n1CNTRL', asked: 'admin' }, { nodeId: 'n2CNTRL' }, { asked: 'root' }],
  });
  expect(asked.requests.map((r) => r.asked)).toEqual(['admin', 'proxy', 'proxy']);
  const withAddresses = normalizeAccessDevices({
    devices: [{ id: 'n1CNTRL', addresses: ['100.64.0.31', 7, '', 'fd7a:115c:a1e0::31'] }],
  });
  expect(withAddresses.devices[0].addresses).toEqual(['100.64.0.31', 'fd7a:115c:a1e0::31']);
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
    'label_invalid',
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
    'online',
    'last_seen',
  ]) {
    expect(copy[key]).toBeUndefined();
  }
  expect(copy.unsaved_one).toBe('1 unsaved change');
  expect(copy.gone_unknown).toBe('Missing');
});
