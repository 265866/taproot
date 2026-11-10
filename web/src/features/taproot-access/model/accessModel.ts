/**
 * The Access page's model: sorting devices into sections, the draft of switch changes, and its
 * conversion to a `PUT /settings` access section. The server checks every rule again.
 */
import type {
  AccessDevice,
  AccessDevices,
  AccessRequest,
  AddressGrant,
  DeviceGrant,
  TaprootMe,
  TaprootSettingsUpdate,
} from '@/services/api/taproot';

export interface Permissions {
  proxy: boolean;
  admin: boolean;
}

/**
 * The page's unsaved state: every device's switches and label, devices to forget, and the
 * addresses.
 */
export interface AccessDraft {
  devices: Record<string, Permissions>;
  labels: Record<string, string>;
  forget: string[];
  addresses: AddressGrant[];
}

export function draftFrom(data: AccessDevices): AccessDraft {
  const devices: Record<string, Permissions> = {};
  const labels: Record<string, string> = {};
  for (const d of data.devices) {
    devices[d.id] = { proxy: d.proxy, admin: d.admin };
    labels[d.id] = d.label;
  }
  return { devices, labels, forget: [], addresses: data.addresses.map((a) => ({ ...a })) };
}

const addressKey = (a: AddressGrant) => `${a.cidr}\n${a.note}`;
const sameAddresses = (a: AddressGrant[], b: AddressGrant[]) =>
  a.length === b.length && a.every((x, i) => addressKey(x) === addressKey(b[i]));

/**
 * Carries the unsaved changes in `draft`, made against `before`, over to freshly loaded `after`,
 * so saving one request does not throw away switches the person was still editing. An edit to
 * something the server changed in between is dropped: saving it would undo that change.
 */
export function rebaseDraft(
  before: AccessDevices,
  draft: AccessDraft,
  after: AccessDevices
): AccessDraft {
  const next = draftFrom(after);
  const saved = new Map(before.devices.map((d) => [d.id, d]));
  const fresh = new Map(after.devices.map((d) => [d.id, d]));
  for (const [id, p] of Object.entries(draft.devices)) {
    const old = saved.get(id);
    const now = fresh.get(id);
    if (
      old &&
      now &&
      (old.proxy !== p.proxy || old.admin !== p.admin) &&
      old.proxy === now.proxy &&
      old.admin === now.admin
    ) {
      next.devices[id] = { ...p };
    }
  }
  for (const [id, label] of Object.entries(draft.labels)) {
    const old = saved.get(id);
    const now = fresh.get(id);
    if (old && now && old.label !== label && old.label === now.label) next.labels[id] = label;
  }
  next.forget = draft.forget.filter((id) => fresh.has(id));
  if (
    !sameAddresses(draft.addresses, before.addresses) &&
    sameAddresses(before.addresses, after.addresses)
  ) {
    next.addresses = draft.addresses;
  }
  return next;
}

/** The switches the draft shows for a device. The server always has Proxy and never Admin. */
export function draftPermissions(device: AccessDevice, draft: AccessDraft): Permissions {
  if (device.server) return { proxy: true, admin: false };
  return draft.devices[device.id] ?? { proxy: device.proxy, admin: device.admin };
}

/**
 * Whether the row can be renamed: a label is saved only with a permission, so a device the
 * draft gives none has no name to stage.
 */
export function canRename(device: AccessDevice, draft: AccessDraft): boolean {
  if (device.server || draft.forget.includes(device.id)) return false;
  const p = draftPermissions(device, draft);
  return p.proxy || p.admin;
}

/** The label a row shows: the draft's when the device can keep one, else the saved one. */
export function draftLabel(device: AccessDevice, draft: AccessDraft): string {
  return canRename(device, draft) ? (draft.labels[device.id] ?? device.label) : device.label;
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

/** The server never has Admin, and your own device keeps it. */
export function adminLock(device: AccessDevice, me: TaprootMe | null): SwitchLock {
  if (device.server) return 'server_never';
  if (device.id === ownDeviceId(me)) return 'needed_by_you';
  return null;
}

const DAY_MS = 24 * 60 * 60 * 1000;
const NEW_FOR_MS = 7 * DAY_MS;
const IDLE_AFTER_MS = 30 * DAY_MS;

/** A device that joined in the last 7 days and has no permission yet. */
export function isNew(device: AccessDevice, now: number): boolean {
  if (device.server || !device.onTailnet || device.proxy || device.admin || !device.created) {
    return false;
  }
  const created = Date.parse(device.created);
  return Number.isFinite(created) && now - created < NEW_FOR_MS && created <= now + 60_000;
}

/** Offline and last seen more than 30 days ago. */
export function isIdle(device: AccessDevice, now: number): boolean {
  if (device.online || !device.lastSeen) return false;
  const seen = Date.parse(device.lastSeen);
  return Number.isFinite(seen) && now - seen > IDLE_AFTER_MS;
}

/**
 * How a row names a device: the owner's label, else the Tailscale name, else (when Tailscale
 * hides it) the owner and the device's Tailscale IP, which the owner also sees in their own admin
 * console, for the caller to phrase. A device without an IP, such as one no longer on the tailnet,
 * gets 4 characters of its stable ID instead.
 */
export type DeviceName =
  | { kind: 'named'; name: string }
  | { kind: 'unnamed'; owner: string; address: string; suffix: string };

/** The IP a row names a device by: its IPv4, else its first IPv6, else empty. */
function displayAddress(addresses: readonly string[]): string {
  return addresses.find((a) => !a.includes(':')) ?? addresses[0] ?? '';
}

export function deviceName(
  device: Pick<AccessDevice, 'label' | 'name' | 'ownerName' | 'login' | 'id' | 'addresses'>
): DeviceName {
  const name = device.label.trim() || device.name.trim();
  if (name) return { kind: 'named', name };
  return {
    kind: 'unnamed',
    owner: device.ownerName.trim() || device.login.trim(),
    address: displayAddress(device.addresses),
    // Every stable ID ends in CNTRL, so the characters before it tell devices apart.
    suffix: device.id.replace(/CNTRL$/, '').slice(-4),
  };
}

/**
 * The words a search matches for a device: names, owner, ID, tags, and Tailscale IPs, not OS or
 * MagicDNS.
 */
function deviceText(d: AccessDevice): string {
  return [d.label, d.name, d.login, d.ownerName, d.id, ...d.tags, ...d.addresses]
    .join(' ')
    .toLowerCase();
}

export function matchesQuery(text: string, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q || q.split(/\s+/).every((word) => text.toLowerCase().includes(word));
}

export type SectionKind = 'yours' | 'servers' | 'person' | 'removed';

export interface DeviceSection {
  kind: SectionKind;
  /** The owner's login for a person section. */
  login: string;
  /** The owner's display name for a person section, or empty. */
  ownerName: string;
  /** A person section whose devices are outside the server's tailnet. */
  outside: boolean;
  devices: AccessDevice[];
}

export interface AccessLayout {
  /** Yours, Servers, then one section per other person with a granted device. */
  sections: DeviceSection[];
  /** Devices without access that the page folds into one line. */
  folded: AccessDevice[];
  /** Granted devices no longer on the tailnet. */
  removed: DeviceSection | null;
}

const sortKey = (d: AccessDevice) => {
  const name = deviceName(d);
  return (
    name.kind === 'named' ? name.name : `${name.owner} ${name.address || name.suffix}`
  ).toLowerCase();
};
const byName = (a: AccessDevice, b: AccessDevice) =>
  sortKey(a).localeCompare(sortKey(b), undefined, { numeric: true }) || a.id.localeCompare(b.id);

/**
 * Sorts devices into the page's sections. Your logins are the viewer's and those of devices in
 * the tailnet that have Admin. Yours lists their devices, Servers the tagged ones and this
 * server, and each other login with a granted device gets a section of its granted devices.
 * Every remaining device without access (a sharee's, another person's, or one idle for over 30
 * days) is folded. Grouping follows the saved grants, so rows do not jump while switches change.
 */
export function layoutDevices(
  data: AccessDevices,
  me: TaprootMe | null,
  query = '',
  now = Date.now()
): AccessLayout {
  const mine = new Set<string>();
  if (me?.via === 'tailnet' && me.login) mine.add(me.login.toLowerCase());
  for (const d of data.devices) {
    if (d.onTailnet && d.admin && !d.outside && d.login) mine.add(d.login.toLowerCase());
  }
  const yours: AccessDevice[] = [];
  const servers: AccessDevice[] = [];
  const people = new Map<string, AccessDevice[]>();
  const folded: AccessDevice[] = [];
  const removed: AccessDevice[] = [];
  for (const d of data.devices) {
    if (!matchesQuery(deviceText(d), query)) continue;
    if (!d.onTailnet) {
      removed.push(d);
      continue;
    }
    const granted = d.server || d.proxy || d.admin;
    const own = mine.has(d.login.toLowerCase());
    if (!granted && (d.outside || isIdle(d, now) || (d.login && !own))) folded.push(d);
    else if (d.server || d.tags.length > 0 || !d.login) servers.push(d);
    else if (own) yours.push(d);
    else people.set(d.login, [...(people.get(d.login) ?? []), d]);
  }
  const section = (kind: SectionKind, devices: AccessDevice[]): DeviceSection => ({
    kind,
    login: '',
    ownerName: '',
    outside: false,
    devices: devices.sort(byName),
  });
  const sections: DeviceSection[] = [];
  if (yours.length) sections.push(section('yours', yours));
  if (servers.length) sections.push(section('servers', servers));
  for (const login of [...people.keys()].sort((a, b) => a.localeCompare(b))) {
    const devices = (people.get(login) ?? []).sort(byName);
    sections.push({
      kind: 'person',
      login,
      ownerName: devices.find((d) => d.ownerName)?.ownerName ?? '',
      outside: devices.some((d) => d.outside),
      devices,
    });
  }
  return {
    sections,
    folded: folded.sort(byName),
    removed: removed.length ? section('removed', removed) : null,
  };
}

/**
 * How many rows the draft changes: devices with changed switches or name, forgotten devices, and
 * added or removed addresses. A name staged for a device the draft gives no permission is not
 * saved, so it is not a change.
 */
export function changeCount(data: AccessDevices, draft: AccessDraft): number {
  let n = draft.forget.length;
  for (const d of data.devices) {
    const p = draft.devices[d.id];
    if ((p && (p.proxy !== d.proxy || p.admin !== d.admin)) || draftLabel(d, draft) !== d.label) {
      n++;
    }
  }
  const saved = new Set(data.addresses.map(addressKey));
  const next = new Set(draft.addresses.map(addressKey));
  for (const key of next) if (!saved.has(key)) n++;
  for (const key of saved) if (!next.has(key)) n++;
  return n;
}

export type Validation<T> = { ok: true; update: T } | { ok: false; error: string; detail?: string };

const grantOf = (d: AccessDevice, p: Permissions, label = d.label): DeviceGrant => ({
  id: d.id,
  name: d.name,
  label,
  proxy: p.proxy,
  admin: p.admin,
});

/**
 * The access section to save: every device with a permission, with its draft label, in the
 * server's order, and the addresses. The server's own node is left out, since it always has
 * Proxy and never Admin, and a forgotten device is dropped.
 */
export function accessUpdate(
  data: AccessDevices,
  draft: AccessDraft,
  me: TaprootMe | null
): Validation<NonNullable<TaprootSettingsUpdate['access']>> {
  const devices: DeviceGrant[] = [];
  for (const d of data.devices) {
    if (d.server || draft.forget.includes(d.id)) continue;
    const p = draftPermissions(d, draft);
    if (p.proxy || p.admin) devices.push(grantOf(d, p, draftLabel(d, draft)));
  }
  const own = ownDeviceId(me);
  if (own && !devices.some((d) => d.id === own && d.admin)) return { ok: false, error: 'lockout' };
  const update = { devices, addresses: draft.addresses };
  // An older server sends no version and checks none.
  return { ok: true, update: data.version ? { version: data.version, ...update } : update };
}

export interface AllowForm extends Permissions {
  /** The label to save; empty for none. */
  name: string;
}

/**
 * The Allow form's starting values: the device's label when the owner gave it one, else the name
 * the device gave itself or its Tailscale name, and its current grants with Proxy on, so Allow
 * as shown gives at least Proxy and never takes Admin away.
 */
export function allowDefaults(request: AccessRequest, device?: AccessDevice): AllowForm {
  return { name: device?.label || request.name, proxy: true, admin: device?.admin ?? false };
}

/** Labels are limited to 64 characters, counted the way the server counts them. */
export const MAX_LABEL_LENGTH = 64;

/** Control and format characters, which the server refuses in a label. */
const HIDDEN_CHARACTERS = /[\p{Cc}\p{Cf}]/u;
/**
 * The space Go's strings.TrimSpace removes. String.prototype.trim also removes U+FEFF, a format
 * character the server refuses, and keeps U+0085.
 */
const EDGE_SPACE =
  /^[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g;

/**
 * Checks a name typed for a device the way the server does, and returns the label to save. A
 * name equal to the Tailscale name saves no label, so the row follows the tailnet.
 */
export function labelFor(raw: string, tailscaleName: string): Validation<string> {
  const label = raw.replace(EDGE_SPACE, '');
  if ([...label].length > MAX_LABEL_LENGTH) {
    return { ok: false, error: 'label_too_long', detail: String(MAX_LABEL_LENGTH) };
  }
  if (HIDDEN_CHARACTERS.test(label)) return { ok: false, error: 'label_invalid' };
  return { ok: true, update: label === tailscaleName ? '' : label };
}

/**
 * The update that allows one request: the requesting device's entry alone, which the server sets
 * and leaves every other device as saved, so a page loaded before another save cannot undo it.
 * Unsaved switch changes are not part of it.
 */
export function allowUpdate(
  data: AccessDevices,
  request: AccessRequest,
  form: AllowForm
): Validation<{ device: DeviceGrant }> {
  if (!form.proxy && !form.admin) return { ok: false, error: 'allow_nothing' };
  const device = data.devices.find((d) => d.id === request.nodeId);
  const name = device?.name ?? '';
  // An untouched name keeps the owner's label exactly.
  const label =
    device?.label && form.name === device.label
      ? ({ ok: true, update: device.label } as const)
      : labelFor(form.name, name);
  if (!label.ok) return label;
  return {
    ok: true,
    update: {
      device: { id: request.nodeId, name, label: label.update, proxy: form.proxy, admin: form.admin },
    },
  };
}

/** An address as its bytes (4 or 16) and a prefix length. */
interface Prefix {
  bytes: number[];
  bits: number;
}

function parseIPv4(text: string): number[] | null {
  const parts = text.split('.');
  if (parts.length !== 4 || parts.some((p) => !/^(0|[1-9]\d{0,2})$/.test(p) || Number(p) > 255)) {
    return null;
  }
  return parts.map(Number);
}

function parseIPv6(text: string): number[] | null {
  const halves = text.split('::');
  if (halves.length > 2) return null;
  const groups = (half: string): number[] | null => {
    if (half === '') return [];
    const out: number[] = [];
    const parts = half.split(':');
    for (const [i, part] of parts.entries()) {
      if (i === parts.length - 1 && part.includes('.')) {
        const v4 = parseIPv4(part);
        if (!v4) return null;
        out.push(...v4);
      } else if (/^[0-9a-fA-F]{1,4}$/.test(part)) {
        const n = parseInt(part, 16);
        out.push(n >> 8, n & 0xff);
      } else {
        return null;
      }
    }
    return out;
  };
  const head = groups(halves[0]);
  const tail = halves.length === 2 ? groups(halves[1]) : [];
  if (!head || !tail) return null;
  if (halves.length === 1) return head.length === 16 ? head : null;
  const zeros = 16 - head.length - tail.length;
  return zeros >= 2 ? [...head, ...new Array<number>(zeros).fill(0), ...tail] : null;
}

/**
 * Parses an address or CIDR the way the server does, or returns null. A bare IPv4-mapped IPv6
 * address is the IPv4 address, as on the server.
 */
function parsePrefix(text: string): Prefix | null {
  const [addr, bits, extra] = text.split('/');
  if (extra !== undefined) return null;
  const bytes = addr.includes(':') ? parseIPv6(addr) : parseIPv4(addr);
  if (!bytes) return null;
  if (bits === undefined) {
    const mapped =
      bytes.length === 16 && bytes.slice(0, 10).every((b) => b === 0) && bytes[10] === 0xff && bytes[11] === 0xff;
    return mapped ? { bytes: bytes.slice(12), bits: 32 } : { bytes, bits: bytes.length * 8 };
  }
  if (!/^(0|[1-9]\d{0,2})$/.test(bits) || Number(bits) > bytes.length * 8) return null;
  return { bytes, bits: Number(bits) };
}

function overlaps(a: Prefix, b: Prefix): boolean {
  if (a.bytes.length !== b.bytes.length) return false;
  const bits = Math.min(a.bits, b.bits);
  for (let i = 0; i < bits; i++) {
    const bit = (bytes: number[]) => (bytes[i >> 3] >> (7 - (i & 7))) & 1;
    if (bit(a.bytes) !== bit(b.bytes)) return false;
  }
  return true;
}

/**
 * Tailscale's address ranges. An address entry is matched before the device's identity, so one
 * covering these would give every tailnet device Proxy and take Admin from the admins; the server
 * refuses it.
 */
const TAILNET = [parsePrefix('100.64.0.0/10'), parsePrefix('fd7a:115c:a1e0::/48')].filter(
  (p): p is Prefix => p !== null
);

export function newAddress(
  cidr: string,
  note: string,
  draft: AccessDraft
): Validation<AddressGrant> {
  const value = cidr.trim();
  const prefix = parsePrefix(value);
  if (!prefix) return { ok: false, error: 'address_format', detail: value };
  if (TAILNET.some((t) => overlaps(prefix, t))) {
    return { ok: false, error: 'address_tailnet', detail: value };
  }
  const bare = (a: string) => a.replace(/\/(32|128)$/, '');
  if (draft.addresses.some((a) => bare(a.cidr) === bare(value))) {
    return { ok: false, error: 'address_duplicate', detail: value };
  }
  return { ok: true, update: { cidr: value, note: note.trim() } };
}

/** The steps of Allow, in order: save the grant, close the request, reload the page. */
export interface AllowSteps {
  save: () => Promise<void>;
  close: () => Promise<void>;
  reload: () => Promise<void>;
}

export type AllowOutcome =
  | { kind: 'allowed' }
  | { kind: 'save_failed'; error: unknown }
  | { kind: 'saved_not_closed'; error: unknown };

/**
 * Runs Allow. It saves first, so a failed save leaves the request open to try again. Once the
 * save succeeded the page reloads, even when closing the request failed, because the grant
 * changed.
 */
export async function runAllow(steps: AllowSteps): Promise<AllowOutcome> {
  try {
    await steps.save();
  } catch (error: unknown) {
    return { kind: 'save_failed', error };
  }
  let outcome: AllowOutcome = { kind: 'allowed' };
  try {
    await steps.close();
  } catch (error: unknown) {
    outcome = { kind: 'saved_not_closed', error };
  }
  await steps.reload();
  return outcome;
}
