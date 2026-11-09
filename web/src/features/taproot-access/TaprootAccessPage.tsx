import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconCopy, IconPlus, IconSearch } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import {
  taprootApi,
  type AccessDevice,
  type AccessDevices,
  type AccessRequest,
  type AddressGrant,
  type TaprootMe,
} from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';
import { copyToClipboard } from '@/utils/clipboard';
import { getErrorMessage } from '@/utils/helpers';
import {
  accessUpdate,
  adminLock,
  allowDefaults,
  allowUpdate,
  changeCount,
  deviceName,
  draftFrom,
  isNew,
  layoutDevices,
  matchesQuery,
  newAddress,
  ownDeviceId,
  proxyLock,
  rebaseDraft,
  type AccessDraft,
  type AllowForm,
  type DeviceName,
  type DeviceSection,
  type SwitchLock,
} from './model/accessModel';
import styles from './TaprootAccessPage.module.scss';

type Translate = ReturnType<typeof useTranslation>['t'];
type SetDraft = (update: (draft: AccessDraft | null) => AccessDraft | null) => void;

export function TaprootAccessPage() {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const { mask } = useEmailMask();
  const [data, setData] = useState<AccessDevices | null>(null);
  const [me, setMe] = useState<TaprootMe | null>(null);
  const [draft, setDraft] = useState<AccessDraft | null>(null);
  const [query, setQuery] = useState('');
  const [loadError, setLoadError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [saving, setSaving] = useState(false);
  // Relative times and New badges are as of the last load.
  const [now, setNow] = useState(() => Date.now());
  const dataRef = useRef<AccessDevices | null>(null);

  /** Loads the page. With keepDraft, unsaved switches survive the reload. */
  const load = useCallback(async (keepDraft = false) => {
    try {
      const [devices, who] = await Promise.all([taprootApi.accessDevices(), taprootApi.me()]);
      const before = dataRef.current;
      dataRef.current = devices;
      setData(devices);
      setMe(who);
      setNow(Date.now());
      setDraft((current) =>
        keepDraft && before && current ? rebaseDraft(before, current, devices) : draftFrom(devices)
      );
      setLoadError('');
      if (!keepDraft) setSaveError('');
    } catch (err: unknown) {
      setLoadError(getErrorMessage(err));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useHeaderRefresh(load);

  const save = async () => {
    if (!data || !draft) return;
    const result = accessUpdate(data, draft, me);
    if (!result.ok) {
      setSaveError(t(`taproot_access.errors.${result.error}`, { detail: result.detail ?? '' }));
      return;
    }
    setSaving(true);
    setSaveError('');
    try {
      await taprootApi.putSettings({ access: result.update });
      showNotification(t('taproot_access.saved'), 'success');
      await load();
    } catch (err: unknown) {
      setSaveError(getErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  /** Saves one request's grant; returns the message to show in its form, or null. */
  const allow = async (request: AccessRequest, form: AllowForm): Promise<string | null> => {
    if (!data) return null;
    const result = allowUpdate(data, request, form);
    if (!result.ok) {
      return t(`taproot_access.errors.${result.error}`, { detail: result.detail ?? '' });
    }
    try {
      await taprootApi.putSettings({ access: result.update });
    } catch (err: unknown) {
      return getErrorMessage(err);
    }
    showNotification(t('taproot_access.allowed'), 'success');
    await load(true);
    return null;
  };

  const deny = async (request: AccessRequest) => {
    try {
      await taprootApi.dismissRequest(request.nodeId);
      showNotification(t('taproot_access.denied'), 'success');
      await load(true);
    } catch (err: unknown) {
      showNotification(getErrorMessage(err), 'error');
    }
  };

  return (
    <div className={styles.page}>
      {loadError && (
        <div className="error-box" role="alert">
          {mask(t('taproot_access.load_failed', { message: loadError }))}
        </div>
      )}
      {!data && !loadError && (
        <>
          <h1 className={styles.title}>{t('taproot_access.title')}</h1>
          <div className={styles.loading} role="status">
            {t('taproot_access.loading')}
          </div>
        </>
      )}
      {data && draft && (
        <AccessView
          data={data}
          me={me}
          draft={draft}
          setDraft={setDraft}
          query={query}
          setQuery={setQuery}
          now={now}
          saving={saving}
          saveError={saveError}
          onSave={() => void save()}
          onReset={() => {
            setDraft(draftFrom(data));
            setSaveError('');
          }}
          onAllow={allow}
          onDeny={(request) => void deny(request)}
        />
      )}
    </div>
  );
}

export interface AccessViewProps {
  data: AccessDevices;
  me: TaprootMe | null;
  draft: AccessDraft;
  setDraft: SetDraft;
  query: string;
  setQuery: (query: string) => void;
  now: number;
  saving: boolean;
  saveError: string;
  onSave: () => void;
  onReset: () => void;
  onAllow: (request: AccessRequest, form: AllowForm) => Promise<string | null>;
  onDeny: (request: AccessRequest) => void;
  /** Starts with the folded devices shown, for tests. */
  initiallyUnfolded?: boolean;
}

/** The loaded page: requests, the device table, and the save bar. */
export function AccessView({
  data,
  me,
  draft,
  setDraft,
  query,
  setQuery,
  now,
  saving,
  saveError,
  onSave,
  onReset,
  onAllow,
  onDeny,
  initiallyUnfolded = false,
}: AccessViewProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const searchId = useId();
  const [unfolded, setUnfolded] = useState(initiallyUnfolded);
  const layout = useMemo(() => layoutDevices(data, me, query, now), [data, me, query, now]);
  const changes = changeCount(data, draft);
  const searching = query.trim() !== '';
  const showFolded = unfolded || searching;
  const nothingMatches =
    searching && layout.sections.length === 0 && layout.folded.length === 0 && !layout.removed;
  const rowProps = { draft, setDraft, me, now };

  return (
    <>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('taproot_access.title')}</h1>
        <label className={styles.search} htmlFor={searchId}>
          <IconSearch size={16} />
          <span className={styles.srOnly}>{t('taproot_access.search_label')}</span>
          <input
            id={searchId}
            className="input"
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('taproot_access.search_placeholder')}
            autoComplete="off"
            spellCheck={false}
          />
        </label>
      </header>

      {!data.enabled && (
        <div className="status-badge warning" role="status">
          {t('taproot_access.disabled')}
        </div>
      )}
      {data.tailnetError && (
        <div className="status-badge warning" role="status">
          {mask(t('taproot_access.tailnet_error', { message: data.tailnetError }))}
        </div>
      )}

      {data.requests.length > 0 && (
        <section className={styles.requests} aria-label={t('taproot_access.requests')}>
          <h2 className={styles.requestsTitle}>{t('taproot_access.requests')}</h2>
          {data.requests.map((request) => (
            <RequestRow
              key={request.nodeId}
              request={request}
              now={now}
              disabled={!data.enabled}
              onAllow={onAllow}
              onDeny={onDeny}
            />
          ))}
        </section>
      )}

      <div className={styles.table} role="table" aria-label={t('taproot_access.title')}>
        <div className={`${styles.row} ${styles.head}`} role="row">
          <span role="columnheader">{t('taproot_access.column_device')}</span>
          <span role="columnheader" className={styles.id}>
            {t('taproot_access.column_id')}
          </span>
          <span role="columnheader">{t('taproot_access.proxy')}</span>
          <span role="columnheader">{t('taproot_access.admin')}</span>
        </div>
        {layout.sections.map((section) => (
          <Section key={section.kind + section.login} section={section} {...rowProps} />
        ))}
        {layout.folded.length > 0 && (
          <div className={styles.group} role="rowgroup">
            <div className={styles.foldRow} role="row">
              <span role="cell">
                <button
                  type="button"
                  className={styles.foldButton}
                  aria-expanded={showFolded}
                  onClick={() => setUnfolded((open) => !open)}
                >
                  <span>{t('taproot_access.folded', { count: layout.folded.length })}</span>
                  <span className={styles.foldAction}>
                    {showFolded ? t('taproot_access.hide') : t('taproot_access.show')}
                  </span>
                </button>
              </span>
            </div>
            {showFolded &&
              layout.folded.map((device) => (
                <DeviceRow key={device.id} device={device} {...rowProps} />
              ))}
          </div>
        )}
        {layout.removed && <Section section={layout.removed} {...rowProps} />}
        {nothingMatches && (
          <p className={styles.empty}>{t('taproot_access.no_match', { query: query.trim() })}</p>
        )}
        <Addresses saved={data.addresses} draft={draft} setDraft={setDraft} query={query} />
      </div>

      {saveError && (
        <div className="error-box" role="alert">
          {mask(saveError)}
        </div>
      )}
      {changes > 0 && (
        <div className={styles.saveBar} role="region" aria-label={t('taproot_access.save')}>
          <span>{t('taproot_access.unsaved', { count: changes })}</span>
          <div className={styles.saveActions}>
            <Button variant="ghost" size="sm" disabled={saving} onClick={onReset}>
              {t('taproot_access.reset')}
            </Button>
            <Button size="sm" loading={saving} disabled={!data.enabled} onClick={onSave}>
              {t('taproot_access.save')}
            </Button>
          </div>
        </div>
      )}
    </>
  );
}

/** A device's name as the page shows it. */
function nameText(name: DeviceName, t: Translate): string {
  if (name.kind === 'named') return name.name;
  return name.owner
    ? t('taproot_access.unnamed', { owner: name.owner, suffix: name.suffix })
    : t('taproot_access.unnamed_no_owner', { suffix: name.suffix });
}

function relativeTime(iso: string | null, now: number): string {
  const at = iso ? Date.parse(iso) : NaN;
  if (!Number.isFinite(at)) return '';
  const seconds = Math.round((at - now) / 1000);
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['day', 86_400],
    ['hour', 3_600],
    ['minute', 60],
  ];
  const format = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit);
  }
  return format.format(seconds, 'second');
}

function RequestRow({
  request,
  now,
  disabled,
  onAllow,
  onDeny,
}: {
  request: AccessRequest;
  now: number;
  disabled: boolean;
  onAllow: AccessViewProps['onAllow'];
  onDeny: AccessViewProps['onDeny'];
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const nameId = useId();
  const [form, setForm] = useState<AllowForm | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const name = nameText(
    deviceName({
      label: '',
      name: request.name,
      ownerName: request.ownerName,
      login: request.login,
      id: request.nodeId,
    }),
    t
  );
  const meta = [
    request.login,
    request.client,
    relativeTime(request.lastSeen, now),
    t('taproot_access.request_count', { count: request.count }),
  ].filter(Boolean);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!form) return;
    setPending(true);
    const failure = await onAllow(request, form);
    setPending(false);
    if (failure) setError(failure);
  };

  return (
    <div className={styles.request}>
      <div className={styles.requestHead}>
        <span className={styles.device}>
          <span className={styles.name}>
            {name}
            {request.outside && (
              <span className={styles.badge}>{t('taproot_access.other_tailnet')}</span>
            )}
          </span>
          <span className={styles.details}>{meta.join(' · ')}</span>
        </span>
        {!form && (
          <span className={styles.requestActions}>
            <Button
              size="sm"
              disabled={disabled}
              aria-label={t('taproot_access.allow_label', { name })}
              onClick={() => setForm(allowDefaults(request))}
            >
              {t('taproot_access.allow')}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={disabled}
              aria-label={t('taproot_access.deny_label', { name })}
              onClick={() => onDeny(request)}
            >
              {t('taproot_access.deny')}
            </Button>
          </span>
        )}
      </div>
      {form && (
        <form className={styles.allowForm} onSubmit={(event) => void submit(event)}>
          <label htmlFor={nameId} className={styles.srOnly}>
            {t('taproot_access.name_label')}
          </label>
          <input
            id={nameId}
            className="input"
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            placeholder={t('taproot_access.name_label')}
            autoComplete="off"
            maxLength={64}
            autoFocus
          />
          <SelectionCheckbox
            checked={form.proxy}
            onChange={(proxy) => setForm({ ...form, proxy })}
            label={t('taproot_access.proxy')}
          />
          <SelectionCheckbox
            checked={form.admin}
            onChange={(admin) => setForm({ ...form, admin })}
            label={t('taproot_access.admin')}
          />
          <Button type="submit" size="sm" loading={pending} disabled={!form.proxy && !form.admin}>
            {t('taproot_access.allow')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => {
              setForm(null);
              setError('');
            }}
          >
            {t('taproot_access.cancel')}
          </Button>
          {error && (
            <p className={styles.formError} role="alert">
              {mask(error)}
            </p>
          )}
        </form>
      )}
    </div>
  );
}

function GroupHeader({ title, badge, hint }: { title: string; badge?: string; hint?: string }) {
  return (
    <div className={styles.groupHeader} role="row">
      <span role="rowheader">
        <strong>{title}</strong>
        {hint && <span className={styles.groupHint}>{hint}</span>}
        {badge && <span className={styles.badge}>{badge}</span>}
      </span>
    </div>
  );
}

interface RowProps {
  draft: AccessDraft;
  setDraft: SetDraft;
  me: TaprootMe | null;
  now: number;
}

function Section({ section, ...rowProps }: RowProps & { section: DeviceSection }) {
  const { t } = useTranslation();
  let header: ReactNode;
  if (section.kind === 'person') {
    const named = section.ownerName && section.ownerName !== section.login;
    header = (
      <GroupHeader
        title={named ? section.ownerName : section.login}
        hint={named ? section.login : undefined}
        badge={section.outside ? t('taproot_access.other_tailnet') : undefined}
      />
    );
  } else {
    header = <GroupHeader title={t(`taproot_access.${section.kind}`)} />;
  }
  return (
    <div className={styles.group} role="rowgroup">
      {header}
      {section.devices.map((device) =>
        section.kind === 'removed' ? (
          <GoneRow key={device.id} device={device} {...rowProps} />
        ) : (
          <DeviceRow key={device.id} device={device} {...rowProps} />
        )
      )}
    </div>
  );
}

function CopyId({ id, name }: { id: string; name: string }) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  return (
    <span className={styles.id} role="cell">
      <code>{id}</code>
      <button
        type="button"
        className={styles.copy}
        aria-label={t('taproot_access.copy_id', { name })}
        title={t('taproot_access.copy_id', { name })}
        onClick={() =>
          void copyToClipboard(id).then((ok) =>
            showNotification(
              ok ? t('taproot_access.copied', { id }) : t('taproot_access.copy_failed'),
              ok ? 'success' : 'error'
            )
          )
        }
      >
        <IconCopy size={14} />
      </button>
    </span>
  );
}

function Switch({
  checked,
  lock,
  label,
  onChange,
}: {
  checked: boolean;
  lock: SwitchLock;
  label: string;
  onChange: (value: boolean) => void;
}) {
  const { t } = useTranslation();
  const note = lock ? t(`taproot_access.${LOCK_NOTES[lock]}`) : '';
  return (
    <span className={styles.switch} role="cell">
      <ToggleSwitch
        checked={checked}
        onChange={onChange}
        disabled={lock !== null}
        ariaLabel={label}
      />
      {note && <span className={styles.lockNote}>{note}</span>}
    </span>
  );
}

const LOCK_NOTES: Record<NonNullable<SwitchLock>, string> = {
  server_always: 'always',
  server_never: 'never',
  needed_by_you: 'needed_by_you',
  from_request: 'from_request',
};

const OS_NAMES: Record<string, string> = {
  linux: 'Linux',
  windows: 'Windows',
  macos: 'macOS',
  ios: 'iOS',
  ipados: 'iPadOS',
  android: 'Android',
  freebsd: 'FreeBSD',
  openbsd: 'OpenBSD',
  tvos: 'tvOS',
};

/** The name's tooltip: the Tailscale name under a label, the OS, and the full MagicDNS name. */
function nameTitle(device: AccessDevice): string {
  return [
    device.label && device.name !== device.label ? device.name : '',
    OS_NAMES[device.os.toLowerCase()] ?? device.os,
    device.dnsName,
  ]
    .filter(Boolean)
    .join(' · ');
}

function DeviceRow({ device, draft, setDraft, me, now }: RowProps & { device: AccessDevice }) {
  const { t } = useTranslation();
  const fresh = isNew(device, now);
  const own = device.id === ownDeviceId(me);
  const name = nameText(deviceName(device), t);
  const p = device.server
    ? { proxy: true, admin: false }
    : (draft.devices[device.id] ?? { proxy: device.proxy, admin: device.admin });
  const set = (key: 'proxy' | 'admin', value: boolean) =>
    setDraft((d) => d && { ...d, devices: { ...d.devices, [device.id]: { ...p, [key]: value } } });
  const presence = device.online
    ? t('taproot_access.online')
    : device.lastSeen
      ? t('taproot_access.last_seen', { when: relativeTime(device.lastSeen, now) })
      : '';
  const title = nameTitle(device);
  return (
    <div className={`${styles.row} ${fresh ? styles.fresh : ''}`} role="row">
      <span className={styles.device} role="cell">
        <span className={styles.name} title={title || undefined}>
          {name}
          {own && <span className={styles.badge}>{t('taproot_access.this_device')}</span>}
          {device.server && <span className={styles.badge}>{t('taproot_access.this_server')}</span>}
          {fresh && (
            <span className={`${styles.badge} ${styles.newBadge}`}>{t('taproot_access.new')}</span>
          )}
        </span>
        {presence && <span className={styles.details}>{presence}</span>}
      </span>
      <CopyId id={device.id} name={name} />
      <Switch
        checked={p.proxy}
        lock={proxyLock(device)}
        label={t('taproot_access.proxy_label', { name })}
        onChange={(value) => set('proxy', value)}
      />
      <Switch
        checked={p.admin}
        lock={adminLock(device, me)}
        label={t('taproot_access.admin_label', { name })}
        onChange={(value) => set('admin', value)}
      />
    </div>
  );
}

function GoneRow({ device, draft, setDraft, now }: RowProps & { device: AccessDevice }) {
  const { t } = useTranslation();
  const forgetting = draft.forget.includes(device.id);
  const name = device.label || device.name || device.id;
  const since = device.missingSince
    ? t('taproot_access.gone_since', { when: relativeTime(device.missingSince, now) })
    : t('taproot_access.gone_unknown');
  return (
    <div className={`${styles.row} ${styles.gone}`} role="row">
      <span className={styles.device} role="cell">
        <span className={styles.name}>{name}</span>
        <span className={styles.details}>
          {forgetting ? t('taproot_access.will_forget') : since}
        </span>
      </span>
      <span className={styles.id} role="cell">
        <code>{device.id}</code>
      </span>
      <span className={styles.forget} role="cell">
        <Button
          variant="ghost"
          size="sm"
          aria-label={t(forgetting ? 'taproot_access.keep_label' : 'taproot_access.forget_label', {
            name,
          })}
          onClick={() =>
            setDraft(
              (d) =>
                d && {
                  ...d,
                  forget: forgetting
                    ? d.forget.filter((id) => id !== device.id)
                    : [...d.forget, device.id],
                }
            )
          }
        >
          {forgetting ? t('taproot_access.keep') : t('taproot_access.forget')}
        </Button>
      </span>
    </div>
  );
}

const addressKey = (a: AddressGrant) => `${a.cidr}\n${a.note}`;

/**
 * The addresses group. A saved address switched off stays listed until the draft is saved, so the
 * switch can turn it back on.
 */
function Addresses({
  saved,
  draft,
  setDraft,
  query,
}: {
  saved: AddressGrant[];
  draft: AccessDraft;
  setDraft: SetDraft;
  query: string;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const ids = { cidr: useId(), note: useId() };
  const [adding, setAdding] = useState(false);
  const [cidr, setCidr] = useState('');
  const [note, setNote] = useState('');
  const [error, setError] = useState('');
  const keptKeys = new Set(draft.addresses.map(addressKey));
  const shown = [...draft.addresses, ...saved.filter((a) => !keptKeys.has(addressKey(a)))].filter(
    (a) => matchesQuery(`${a.cidr} ${a.note}`, query)
  );

  const add = (event: FormEvent) => {
    event.preventDefault();
    const result = newAddress(cidr, note, draft);
    if (!result.ok) {
      setError(t(`taproot_access.errors.${result.error}`, { detail: result.detail ?? '' }));
      return;
    }
    setDraft((d) => d && { ...d, addresses: [...d.addresses, result.update] });
    setCidr('');
    setNote('');
    setError('');
    setAdding(false);
  };

  const toggle = (address: AddressGrant, on: boolean) =>
    setDraft(
      (d) =>
        d && {
          ...d,
          addresses: on
            ? [...d.addresses, address]
            : d.addresses.filter((a) => addressKey(a) !== addressKey(address)),
        }
    );

  let addRow: ReactNode = (
    <button type="button" className={styles.addLink} onClick={() => setAdding(true)}>
      <IconPlus size={14} />
      <span>{t('taproot_access.add_address')}</span>
    </button>
  );
  if (adding) {
    addRow = (
      <form className={styles.addForm} onSubmit={add}>
        <label htmlFor={ids.cidr} className={styles.srOnly}>
          {t('taproot_access.address_label')}
        </label>
        <input
          id={ids.cidr}
          className="input"
          value={cidr}
          onChange={(event) => setCidr(event.target.value)}
          placeholder={t('taproot_access.address_placeholder')}
          autoComplete="off"
          spellCheck={false}
          autoFocus
        />
        <label htmlFor={ids.note} className={styles.srOnly}>
          {t('taproot_access.note_label')}
        </label>
        <input
          id={ids.note}
          className="input"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          placeholder={t('taproot_access.note_placeholder')}
          autoComplete="off"
        />
        <Button type="submit" size="sm" variant="secondary" disabled={!cidr.trim()}>
          {t('taproot_access.add')}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            setAdding(false);
            setError('');
          }}
        >
          {t('taproot_access.cancel')}
        </Button>
      </form>
    );
  }

  return (
    <div className={styles.group} role="rowgroup">
      <GroupHeader title={t('taproot_access.addresses')} />
      {shown.map((address) => (
        <div key={addressKey(address)} className={styles.row} role="row">
          <span className={styles.device} role="cell">
            <span className={styles.name}>{address.cidr}</span>
            {address.note && <span className={styles.details}>{address.note}</span>}
          </span>
          <span className={styles.id} role="cell">
            <span className={styles.muted}>{t('taproot_access.no_id')}</span>
          </span>
          <Switch
            checked={keptKeys.has(addressKey(address))}
            lock={null}
            label={t('taproot_access.proxy_label', { name: address.cidr })}
            onChange={(on) => toggle(address, on)}
          />
          <Switch
            checked={false}
            lock="server_never"
            label={t('taproot_access.admin_label', { name: address.cidr })}
            onChange={() => undefined}
          />
        </div>
      ))}
      <div className={styles.addRow}>{addRow}</div>
      {error && (
        <div className="error-box" role="alert">
          {mask(error)}
        </div>
      )}
    </div>
  );
}
