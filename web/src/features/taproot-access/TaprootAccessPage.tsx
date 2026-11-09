import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconCopy, IconPlus, IconSearch } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import {
  taprootApi,
  type AccessDevice,
  type AccessDevices,
  type AddressGrant,
  type TaprootMe,
} from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';
import { copyToClipboard } from '@/utils/clipboard';
import { getErrorMessage } from '@/utils/helpers';
import {
  accessUpdate,
  adminLock,
  changeCount,
  draftFrom,
  groupDevices,
  isNew,
  matchesQuery,
  newAddress,
  ownDeviceId,
  proxyLock,
  type AccessDraft,
  type DeviceGroup,
  type SwitchLock,
} from './model/accessModel';
import styles from './TaprootAccessPage.module.scss';

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
  const searchId = useId();

  const load = useCallback(async () => {
    try {
      const [devices, who] = await Promise.all([taprootApi.accessDevices(), taprootApi.me()]);
      setData(devices);
      setMe(who);
      setNow(Date.now());
      setDraft(draftFrom(devices));
      setLoadError('');
      setSaveError('');
    } catch (err: unknown) {
      setLoadError(getErrorMessage(err));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useHeaderRefresh(load);

  const groups = useMemo(() => (data ? groupDevices(data, me, query) : []), [data, me, query]);
  const changes = data && draft ? changeCount(data, draft) : 0;

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

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>{t('taproot_access.title')}</h1>
          <p className={styles.description}>{t('taproot_access.description')}</p>
        </div>
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
      <p className={styles.legend}>
        <span>
          <strong>{t('taproot_access.proxy')}</strong> {t('taproot_access.proxy_hint')}
        </span>
        <span>
          <strong>{t('taproot_access.admin')}</strong> {t('taproot_access.admin_hint')}
        </span>
      </p>

      {loadError && (
        <div className="error-box" role="alert">
          {mask(t('taproot_access.load_failed', { message: loadError }))}
        </div>
      )}
      {!data && !loadError && (
        <div className={styles.loading} role="status">
          {t('taproot_access.loading')}
        </div>
      )}
      {data && !data.enabled && (
        <div className="status-badge warning" role="status">
          {t('taproot_access.disabled')}
        </div>
      )}
      {data?.tailnetError && (
        <div className="status-badge warning" role="status">
          {mask(t('taproot_access.tailnet_error', { message: data.tailnetError }))}
        </div>
      )}

      {data && draft && (
        <>
          <div className={styles.table} role="table" aria-label={t('taproot_access.title')}>
            <div className={`${styles.row} ${styles.head}`} role="row">
              <span role="columnheader">{t('taproot_access.column_device')}</span>
              <span role="columnheader">{t('taproot_access.column_id')}</span>
              <span role="columnheader">{t('taproot_access.proxy')}</span>
              <span role="columnheader">{t('taproot_access.admin')}</span>
            </div>
            {groups.map((group) => (
              <Group
                key={group.kind + group.login}
                group={group}
                data={data}
                draft={draft}
                setDraft={setDraft}
                me={me}
                now={now}
              />
            ))}
            {groups.length === 0 && query.trim() && (
              <p className={styles.empty}>
                {t('taproot_access.no_match', { query: query.trim() })}
              </p>
            )}
            <Addresses saved={data.addresses} draft={draft} setDraft={setDraft} query={query} />
          </div>

          {saveError && (
            <div className="error-box" role="alert">
              {mask(saveError)}
            </div>
          )}
          <div className={styles.saveBar} role="region" aria-label={t('taproot_access.save')}>
            <span>
              {changes === 0
                ? t('taproot_access.unsaved_none')
                : t('taproot_access.unsaved', { count: changes })}
            </span>
            <div className={styles.saveActions}>
              <Button
                variant="ghost"
                size="sm"
                disabled={changes === 0 || saving}
                onClick={() => {
                  setDraft(draftFrom(data));
                  setSaveError('');
                }}
              >
                {t('taproot_access.reset')}
              </Button>
              <Button
                size="sm"
                loading={saving}
                disabled={changes === 0 || !data.enabled}
                onClick={() => void save()}
              >
                {t('taproot_access.save')}
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

type SetDraft = (update: (draft: AccessDraft | null) => AccessDraft | null) => void;

function GroupHeader({ title, badge, hint }: { title: string; badge?: string; hint?: string }) {
  return (
    <div className={styles.groupHeader} role="row">
      <span role="rowheader">
        <strong>{title}</strong>
        {badge && <span className={styles.badge}>{badge}</span>}
        {hint && <span className={styles.groupHint}>{hint}</span>}
      </span>
    </div>
  );
}

function Group({
  group,
  data,
  draft,
  setDraft,
  me,
  now,
}: {
  group: DeviceGroup;
  data: AccessDevices;
  draft: AccessDraft;
  setDraft: SetDraft;
  me: TaprootMe | null;
  now: number;
}) {
  const { t } = useTranslation();
  const header =
    group.kind === 'owner' ? (
      <GroupHeader title={group.login} badge={group.own ? t('taproot_access.you') : undefined} />
    ) : group.kind === 'tagged' ? (
      <GroupHeader title={t('taproot_access.tagged')} hint={t('taproot_access.tagged_hint')} />
    ) : (
      <GroupHeader
        title={t('taproot_access.gone')}
        hint={t('taproot_access.gone_hint', { hours: data.forgetAfterHours })}
      />
    );
  return (
    <div className={styles.group} role="rowgroup">
      {header}
      {group.devices.map((device) =>
        group.kind === 'gone' ? (
          <GoneRow key={device.id} device={device} draft={draft} setDraft={setDraft} now={now} />
        ) : (
          <DeviceRow
            key={device.id}
            device={device}
            draft={draft}
            setDraft={setDraft}
            me={me}
            now={now}
          />
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
  const note =
    lock === 'server_always'
      ? t('taproot_access.always')
      : lock === 'server_never'
        ? t('taproot_access.never')
        : lock === 'needed_by_you'
          ? t('taproot_access.needed_by_you')
          : '';
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

function DeviceRow({
  device,
  draft,
  setDraft,
  me,
  now,
}: {
  device: AccessDevice;
  draft: AccessDraft;
  setDraft: SetDraft;
  me: TaprootMe | null;
  now: number;
}) {
  const { t } = useTranslation();
  const fresh = isNew(device, now);
  const own = device.id === ownDeviceId(me);
  const p = device.server
    ? { proxy: true, admin: false }
    : (draft.devices[device.id] ?? { proxy: device.proxy, admin: device.admin });
  const set = (key: 'proxy' | 'admin', value: boolean) =>
    setDraft((d) => d && { ...d, devices: { ...d.devices, [device.id]: { ...p, [key]: value } } });
  const details = [
    OS_NAMES[device.os.toLowerCase()] ?? device.os,
    fresh ? t('taproot_access.joined', { when: relativeTime(device.created, now) }) : '',
    device.tags.join(' '),
    device.dnsName,
  ].filter(Boolean);
  return (
    <div className={`${styles.row} ${fresh ? styles.fresh : ''}`} role="row">
      <span className={styles.device} role="cell">
        <span className={styles.name}>
          {device.name}
          {own && <span className={styles.badge}>{t('taproot_access.this_device')}</span>}
          {device.server && <span className={styles.badge}>{t('taproot_access.this_server')}</span>}
          {fresh && (
            <span className={`${styles.badge} ${styles.newBadge}`}>{t('taproot_access.new')}</span>
          )}
        </span>
        <span className={styles.details}>{details.join(' · ')}</span>
      </span>
      <CopyId id={device.id} name={device.name} />
      <Switch
        checked={p.proxy}
        lock={proxyLock(device)}
        label={t('taproot_access.proxy_label', { name: device.name })}
        onChange={(value) => set('proxy', value)}
      />
      <Switch
        checked={p.admin}
        lock={adminLock(device, me)}
        label={t('taproot_access.admin_label', { name: device.name })}
        onChange={(value) => set('admin', value)}
      />
    </div>
  );
}

function GoneRow({
  device,
  draft,
  setDraft,
  now,
}: {
  device: AccessDevice;
  draft: AccessDraft;
  setDraft: SetDraft;
  now: number;
}) {
  const { t } = useTranslation();
  const forgetting = draft.forget.includes(device.id);
  const had = [device.proxy && t('taproot_access.proxy'), device.admin && t('taproot_access.admin')]
    .filter(Boolean)
    .join(', ');
  const since = device.missingSince
    ? t('taproot_access.gone_since', { when: relativeTime(device.missingSince, now) })
    : t('taproot_access.gone_unknown');
  return (
    <div className={`${styles.row} ${styles.gone}`} role="row">
      <span className={styles.device} role="cell">
        <span className={styles.name}>{device.name || device.id}</span>
        <span className={styles.details}>
          {forgetting
            ? t('taproot_access.will_forget')
            : [since, had && t('taproot_access.had', { what: had })].filter(Boolean).join(' · ')}
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
            name: device.name || device.id,
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
      <GroupHeader
        title={t('taproot_access.addresses')}
        hint={t('taproot_access.addresses_hint')}
      />
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
