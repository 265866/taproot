/**
 * The Access page's model: grouping tailnet devices by owner, the draft of switch changes, and
 * its conversion to a `PUT /settings` access section. The server checks every rule again.
 */
import type {
  AccessDevice,
  AccessDevices,
  AddressGrant,
  DeviceGrant,
  TaprootMe,
  TaprootSettingsUpdate,
} from '@/services/api/taproot';

export interface Permissions {
  proxy: boolean;
  admin: boolean;
}

/** The page's unsaved state: every device's switches, devices to forget, and the addresses. */
export interface AccessDraft {
  devices: Record<string, Permissions>;
  forget: string[];
  addresses: AddressGrant[];
}

export function draftFrom(data: AccessDevices): AccessDraft {
  const devices: Record<string, Permissions> = {};
  for (const d of data.devices) devices[d.id] = { proxy: d.proxy, admin: d.admin };
  return { devices, forget: [], addresses: data.addresses.map((a) => ({ ...a })) };
}

/** The device the dashboard is open on, when it came through the tailnet. */
export function ownDeviceId(me: TaprootMe | null): string | null {
  return me?.via === 'tailnet' && me.nodeId ? me.nodeId : null;
}

/** Why a switch cannot be changed, or null when it can. */
export type SwitchLock = 'server_always' | 'server_never' | 'needed_by_you' | null;

export function proxyLock(device: AccessDevice): SwitchLock {
  return device.server ? 'server_always' : null;
}

export function adminLock(device: AccessDevice, me: TaprootMe | null): SwitchLock {
  if (device.server) return 'server_never';
  if (device.id === ownDeviceId(me)) return 'needed_by_you';
  return null;
}

const NEW_FOR_MS = 7 * 24 * 60 * 60 * 1000;

/** A device that joined in the last 7 days and has no permission yet. */
export function isNew(device: AccessDevice, now: number): boolean {
  if (device.server || !device.onTailnet || device.proxy || device.admin || !device.created) {
    return false;
  }
  const created = Date.parse(device.created);
  return Number.isFinite(created) && now - created < NEW_FOR_MS && created <= now + 60_000;
}

export type GroupKind = 'owner' | 'tagged' | 'gone';

export interface DeviceGroup {
  kind: GroupKind;
  /** The owner's login for an owner group. */
  login: string;
  /** True for the group of the login the dashboard is open as. */
  own: boolean;
  devices: AccessDevice[];
}

const byName = (a: AccessDevice, b: AccessDevice) =>
  a.name.localeCompare(b.name) || a.id.localeCompare(b.id);

/** The words a search matches for a device. */
function deviceText(d: AccessDevice): string {
  return [d.name, d.dnsName, d.id, d.login, d.os, ...d.tags].join(' ').toLowerCase();
}

export function matchesQuery(text: string, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q || q.split(/\s+/).every((word) => text.toLowerCase().includes(word));
}

/**
 * Groups devices by owner under plain headers: the viewer's own login first, then the others by
 * login, then tagged devices, then devices no longer on the tailnet. Empty groups are left out.
 */
export function groupDevices(data: AccessDevices, me: TaprootMe | null, query = ''): DeviceGroup[] {
  const ownLogin = me?.via === 'tailnet' ? me.login.toLowerCase() : '';
  const owners = new Map<string, AccessDevice[]>();
  const tagged: AccessDevice[] = [];
  const gone: AccessDevice[] = [];
  for (const d of data.devices) {
    if (!matchesQuery(deviceText(d), query)) continue;
    if (!d.onTailnet) gone.push(d);
    else if (d.tags.length > 0 || !d.login) tagged.push(d);
    else owners.set(d.login, [...(owners.get(d.login) ?? []), d]);
  }
  const logins = [...owners.keys()].sort((a, b) => {
    const own = Number(b.toLowerCase() === ownLogin) - Number(a.toLowerCase() === ownLogin);
    return own || a.localeCompare(b);
  });
  const groups: DeviceGroup[] = logins.map((login) => ({
    kind: 'owner',
    login,
    own: login.toLowerCase() === ownLogin,
    devices: (owners.get(login) ?? []).sort(byName),
  }));
  if (tagged.length)
    groups.push({ kind: 'tagged', login: '', own: false, devices: tagged.sort(byName) });
  if (gone.length) groups.push({ kind: 'gone', login: '', own: false, devices: gone.sort(byName) });
  return groups;
}

const addressKey = (a: AddressGrant) => `${a.cidr}\n${a.note}`;

/** How many rows the draft changes: switches, forgotten devices, and added or removed addresses. */
export function changeCount(data: AccessDevices, draft: AccessDraft): number {
  let n = draft.forget.length;
  for (const d of data.devices) {
    const p = draft.devices[d.id];
    if (p && (p.proxy !== d.proxy || p.admin !== d.admin)) n++;
  }
  const saved = new Set(data.addresses.map(addressKey));
  const next = new Set(draft.addresses.map(addressKey));
  for (const key of next) if (!saved.has(key)) n++;
  for (const key of saved) if (!next.has(key)) n++;
  return n;
}

export type Validation<T> = { ok: true; update: T } | { ok: false; error: string; detail?: string };

/**
 * The access section to save: every device with a permission, in the server's order, and the
 * addresses. The server's own node is left out, since it always has Proxy and never Admin, and
 * a forgotten device is dropped.
 */
export function accessUpdate(
  data: AccessDevices,
  draft: AccessDraft,
  me: TaprootMe | null
): Validation<NonNullable<TaprootSettingsUpdate['access']>> {
  const devices: DeviceGrant[] = [];
  for (const d of data.devices) {
    if (d.server || draft.forget.includes(d.id)) continue;
    const p = draft.devices[d.id] ?? { proxy: d.proxy, admin: d.admin };
    if (p.proxy || p.admin)
      devices.push({ id: d.id, name: d.name, proxy: p.proxy, admin: p.admin });
  }
  const own = ownDeviceId(me);
  if (own && !devices.some((d) => d.id === own && d.admin)) return { ok: false, error: 'lockout' };
  return { ok: true, update: { devices, addresses: draft.addresses } };
}

/** An IPv4 or IPv6 address, optionally with a prefix length. The server parses it exactly. */
const CIDR =
  /^(?:\d{1,3}(?:\.\d{1,3}){3}(?:\/\d{1,2})?|[0-9a-fA-F:]*:[0-9a-fA-F:.]*(?:\/\d{1,3})?)$/;

export function newAddress(
  cidr: string,
  note: string,
  draft: AccessDraft
): Validation<AddressGrant> {
  const value = cidr.trim();
  if (!CIDR.test(value)) return { ok: false, error: 'address_format', detail: value };
  const bare = (a: string) => a.replace(/\/(32|128)$/, '');
  if (draft.addresses.some((a) => bare(a.cidr) === bare(value))) {
    return { ok: false, error: 'address_duplicate', detail: value };
  }
  return { ok: true, update: { cidr: value, note: note.trim() } };
}
