import { useCallback, useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconX } from '@/components/ui/icons';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { NextBackup } from '@/features/taproot-backups/NextBackup';
import { serverZoneLabel, type ServerZone } from '@/features/taproot-backups/model/schedule';
import {
  taprootApi,
  type TaprootMe,
  type TaprootSettings,
  type TaprootSettingsUpdate,
} from '@/services/api/taproot';
import { configApi } from '@/services/api/config';
import { useConfigStore, useNotificationStore } from '@/stores';
import { getErrorMessage } from '@/utils/helpers';
import { formatBytes } from '@/features/taproot-logs/model/format';
import {
  accessUpdate,
  adminNodesUpdate,
  backupsForm,
  backupsUpdate,
  ingestUpdate,
  entryMatchesNode,
  normalizeLogin,
  normalizeNode,
  ownLogin,
  ownNode,
  ownNodeId,
  keepsForever,
  retentionForm,
  retentionUpdate,
  saveRetention,
  type BackupsForm,
  type Recording,
  type RetentionForm,
  type Validation,
} from './model/settingsForm';
import styles from './TaprootSettingsPage.module.scss';
import { AlertsSection } from './AlertsSection';

type Save = (update: TaprootSettingsUpdate) => Promise<void>;

export function TaprootSettingsPage() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<TaprootSettings | null>(null);
  const [me, setMe] = useState<TaprootMe | null>(null);
  const [error, setError] = useState('');
  const [diskBytes, setDiskBytes] = useState<number | null>(null);
  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const updateConfigValue = useConfigStore((state) => state.updateConfigValue);
  const recording: Recording = config ? (config.requestLog ?? false) : null;

  const load = useCallback(async () => {
    try {
      // config.yaml and the stats only add to the request-logs card, so their failures are
      // shown there rather than failing the page.
      const [next, who, stats] = await Promise.all([
        taprootApi.settings(),
        taprootApi.me(),
        taprootApi.requestLogStats().catch(() => null),
        fetchConfig(true).catch(() => null),
      ]);
      setSettings(next);
      setMe(who);
      setDiskBytes(stats?.bytesOnDisk ?? null);
      setError('');
    } catch (err: unknown) {
      setError(getErrorMessage(err));
    }
  }, [fetchConfig]);
  useEffect(() => {
    void load();
  }, [load]);
  useHeaderRefresh(load);

  const save: Save = useCallback(async (update) => {
    setSettings(await taprootApi.putSettings(update));
  }, []);

  const saveRecording = useCallback(
    async (enabled: boolean) => {
      await configApi.updateRequestLog(enabled);
      updateConfigValue('request-log', enabled);
    },
    [updateConfigValue]
  );

  return (
    <div className={styles.page}>
      <header>
        <h1 className={styles.title}>{t('taproot_settings.title')}</h1>
        <p className={styles.description}>{t('taproot_settings.description')}</p>
      </header>
      {error && (
        <div className="error-box" role="alert">
          {t('taproot_settings.load_failed', { message: error })}
        </div>
      )}
      {!settings && !error && (
        <div className={styles.loading} role="status">
          {t('taproot_settings.loading')}
        </div>
      )}
      {settings && (
        <div className={styles.sections}>
          <AccessSection settings={settings.access} me={me} save={save} />
          <AdminNodesSection settings={settings.access} me={me} save={save} />
          <RetentionSection
            settings={settings.requestLogs}
            recording={recording}
            diskBytes={diskBytes}
            save={save}
            saveRecording={saveRecording}
          />
          <BackupsSection settings={settings.backups} zone={settings.info} save={save} />
          <IngestSection settings={settings.ingest} save={save} />
          {settings.alerts && <AlertsSection settings={settings.alerts} save={save} />}
          <AboutSection settings={settings} me={me} />
        </div>
      )}
    </div>
  );
}

/**
 * A card with its own Save and Reset. Saving validates first, then sends only this section, so a
 * mistake in one section never blocks another.
 */
function SectionForm<T>({
  id,
  title,
  description,
  dirty,
  validate,
  save,
  onReset,
  children,
  disabled,
}: {
  id: string;
  title: string;
  description: ReactNode;
  dirty: boolean;
  validate: () => Validation<T>;
  save: (update: T) => Promise<void>;
  onReset: () => void;
  children: ReactNode;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const result = validate();
    if (!result.ok) {
      setError(t(`taproot_settings.errors.${result.error}`, { detail: result.detail ?? '' }));
      return;
    }
    setBusy(true);
    setError('');
    try {
      await save(result.update);
      showNotification(t('taproot_settings.saved', { section: title }), 'success');
    } catch (err: unknown) {
      setError(getErrorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className={styles.card} aria-labelledby={`${id}-title`} onSubmit={(e) => void submit(e)}>
      <div className={styles.cardHead}>
        <h2 id={`${id}-title`} className={styles.cardTitle}>
          {title}
        </h2>
        <p className={styles.cardDescription}>{description}</p>
      </div>
      <div className={styles.fields}>{children}</div>
      {error && (
        <div className="error-box" role="alert">
          {error}
        </div>
      )}
      <div className={styles.cardActions}>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            setError('');
            onReset();
          }}
          disabled={!dirty || busy}
        >
          {t('taproot_settings.reset')}
        </Button>
        <Button type="submit" size="sm" loading={busy} disabled={!dirty || disabled}>
          {t('taproot_settings.save')}
        </Button>
      </div>
    </form>
  );
}

/** Keeps a section's draft until the saved value it came from changes. */
function useDraft<T>(source: T): [T, (next: T) => void, boolean, () => void] {
  const key = JSON.stringify(source);
  const [state, setState] = useState<{ key: string; draft: T }>({ key, draft: source });
  const current = state.key === key ? state : { key, draft: source };
  if (current !== state) setState(current);
  const setDraft = (draft: T) => setState({ key, draft });
  const dirty = JSON.stringify(current.draft) !== key;
  return [current.draft, setDraft, dirty, () => setState({ key, draft: source })];
}

/** A chip list with an add field, shared by the login and admin device lists. */
function ChipListEditor({
  items,
  setItems,
  adding,
  setAdding,
  normalize,
  isOwn,
  disabled,
  labels,
}: {
  items: string[];
  setItems: (items: string[]) => void;
  adding: string;
  setAdding: (value: string) => void;
  normalize: (value: string) => string;
  isOwn: (item: string) => boolean;
  disabled: boolean;
  labels: {
    list: string;
    own: string;
    remove: (item: string) => string;
    cannotRemoveOwn: string;
    add: string;
    addLabel: string;
    addPlaceholder: string;
    empty?: string;
  };
}) {
  const inputId = useId();
  const add = () => {
    const item = normalize(adding);
    if (!item) return;
    if (!items.includes(item)) setItems([...items, item]);
    setAdding('');
  };
  return (
    <>
      {items.length === 0 && labels.empty ? (
        <p className={styles.hint}>{labels.empty}</p>
      ) : (
        <ul className={styles.chips} aria-label={labels.list}>
          {items.map((item) => {
            const own = isOwn(item);
            return (
              <li key={item} className={styles.chip}>
                <span>{item}</span>
                {own && <span className={styles.you}>{labels.own}</span>}
                <button
                  type="button"
                  className={styles.chipRemove}
                  onClick={() => setItems(items.filter((value) => value !== item))}
                  disabled={own || disabled}
                  aria-label={labels.remove(item)}
                  title={own ? labels.cannotRemoveOwn : undefined}
                >
                  <IconX size={12} />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      <div className={styles.addRow}>
        <label htmlFor={inputId} className={styles.srOnly}>
          {labels.addLabel}
        </label>
        <input
          id={inputId}
          className="input"
          value={adding}
          onChange={(event) => setAdding(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              add();
            }
          }}
          placeholder={labels.addPlaceholder}
          autoComplete="off"
          spellCheck={false}
          disabled={disabled}
        />
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={add}
          disabled={!adding.trim() || disabled}
        >
          {labels.add}
        </Button>
      </div>
    </>
  );
}

function AccessSection({
  settings,
  me,
  save,
}: {
  settings: TaprootSettings['access'];
  me: TaprootMe | null;
  save: Save;
}) {
  const { t } = useTranslation();
  const [logins, setLogins, dirty, reset] = useDraft(settings.allowLogins);
  const [adding, setAdding] = useState('');
  const own = ownLogin(me);

  return (
    <SectionForm
      id="settings-access"
      title={t('taproot_settings.access.title')}
      description={
        settings.enabled
          ? t('taproot_settings.access.description')
          : t('taproot_settings.access.disabled')
      }
      dirty={dirty || adding.trim() !== ''}
      disabled={!settings.enabled}
      validate={() => accessUpdate(adding.trim() ? [...logins, adding] : logins, me)}
      save={async (update) => {
        await save({ access: update });
        setAdding('');
      }}
      onReset={() => {
        reset();
        setAdding('');
      }}
    >
      <ChipListEditor
        items={logins}
        setItems={setLogins}
        adding={adding}
        setAdding={setAdding}
        normalize={normalizeLogin}
        isOwn={(login) => login === own}
        disabled={!settings.enabled}
        labels={{
          list: t('taproot_settings.access.list_label'),
          own: t('taproot_settings.access.you'),
          remove: (login) => t('taproot_settings.access.remove', { login }),
          cannotRemoveOwn: t('taproot_settings.access.cannot_remove_self'),
          add: t('taproot_settings.access.add'),
          addLabel: t('taproot_settings.access.add_label'),
          addPlaceholder: t('taproot_settings.access.add_placeholder'),
        }}
      />
      <p className={styles.hint}>
        {own
          ? t('taproot_settings.access.own_hint', { login: own })
          : t('taproot_settings.access.local_hint')}
      </p>
    </SectionForm>
  );
}

function AdminNodesSection({
  settings,
  me,
  save,
}: {
  settings: TaprootSettings['access'];
  me: TaprootMe | null;
  save: Save;
}) {
  const { t } = useTranslation();
  const [nodes, setNodes, dirty, reset] = useDraft(settings.adminNodes);
  const [adding, setAdding] = useState('');
  const own = ownNode(me);
  const ownId = ownNodeId(me);

  return (
    <SectionForm
      id="settings-admin-nodes"
      title={t('taproot_settings.admin_nodes.title')}
      description={
        settings.enabled
          ? t('taproot_settings.admin_nodes.description')
          : t('taproot_settings.admin_nodes.disabled')
      }
      dirty={dirty || adding.trim() !== ''}
      disabled={!settings.enabled}
      validate={() => adminNodesUpdate(adding.trim() ? [...nodes, adding] : nodes, me)}
      save={async (update) => {
        await save({ access: update });
        setAdding('');
      }}
      onReset={() => {
        reset();
        setAdding('');
      }}
    >
      <ChipListEditor
        items={nodes}
        setItems={setNodes}
        adding={adding}
        setAdding={setAdding}
        normalize={normalizeNode}
        isOwn={(node) => own !== null && entryMatchesNode(node, own, ownId)}
        disabled={!settings.enabled}
        labels={{
          list: t('taproot_settings.admin_nodes.list_label'),
          own: t('taproot_settings.admin_nodes.this_device'),
          remove: (node) => t('taproot_settings.admin_nodes.remove', { node }),
          cannotRemoveOwn: t('taproot_settings.admin_nodes.cannot_remove_self'),
          add: t('taproot_settings.admin_nodes.add'),
          addLabel: t('taproot_settings.admin_nodes.add_label'),
          addPlaceholder: t('taproot_settings.admin_nodes.add_placeholder'),
          empty: t('taproot_settings.admin_nodes.empty'),
        }}
      />
      <p className={styles.hint}>
        {own
          ? ownId
            ? t('taproot_settings.admin_nodes.own_hint_id', { node: own, id: ownId })
            : t('taproot_settings.admin_nodes.own_hint', { node: own })
          : t('taproot_settings.admin_nodes.local_hint')}
      </p>
    </SectionForm>
  );
}

export function RetentionSection({
  settings,
  recording,
  diskBytes,
  save,
  saveRecording,
}: {
  settings: TaprootSettings['requestLogs'];
  recording: Recording;
  /** The request-log store's current size, or null when the stats could not be read. */
  diskBytes: number | null;
  save: Save;
  saveRecording: (enabled: boolean) => Promise<void>;
}) {
  const { t } = useTranslation();
  const ids = { days: useId(), size: useId(), exclude: useId() };
  const [form, setForm, dirty, reset] = useDraft<RetentionForm>(retentionForm(settings, recording));
  const set = <K extends keyof RetentionForm>(key: K, value: RetentionForm[K]) =>
    setForm({ ...form, [key]: value });
  const forever = keepsForever(form);

  return (
    <SectionForm
      id="settings-retention"
      title={t('taproot_settings.retention.title')}
      description={t('taproot_settings.retention.description')}
      dirty={dirty}
      validate={() => retentionUpdate(form)}
      save={(update) =>
        saveRetention(update, recording, {
          requestLogs: (requestLogs) => save({ requestLogs }),
          recording: saveRecording,
        })
      }
      onReset={reset}
    >
      <ToggleSwitch
        checked={form.recording ?? false}
        onChange={(value) => set('recording', value)}
        label={t('taproot_settings.retention.record')}
        disabled={form.recording === null}
      />
      {form.recording === null ? (
        <p className={styles.hint}>{t('taproot_settings.retention.record_unknown')}</p>
      ) : form.recording ? (
        <p className={styles.hint}>{t('taproot_settings.retention.record_hint')}</p>
      ) : (
        <p className={styles.notice}>{t('taproot_settings.retention.record_off')}</p>
      )}
      <div className={styles.limits} data-dimmed={form.recording === false ? '' : undefined}>
        <LimitChoice
          legend={t('taproot_settings.retention.age_legend')}
          limited={form.daysLimited}
          onChange={(value) => set('daysLimited', value)}
          limitLabel={t('taproot_settings.retention.delete_after')}
        >
          <label htmlFor={ids.days} className={styles.srOnly}>
            {t('taproot_settings.retention.days_label')}
          </label>
          <input
            id={ids.days}
            className={`input ${styles.short}`}
            inputMode="numeric"
            value={form.days}
            onChange={(event) => set('days', event.target.value)}
            disabled={!form.daysLimited}
          />
          <span>{t('taproot_settings.retention.days_unit')}</span>
        </LimitChoice>
        <LimitChoice
          legend={t('taproot_settings.retention.size_legend')}
          limited={form.sizeLimited}
          onChange={(value) => set('sizeLimited', value)}
          limitLabel={t('taproot_settings.retention.cap_at')}
        >
          <label htmlFor={ids.size} className={styles.srOnly}>
            {t('taproot_settings.retention.size_label')}
          </label>
          <input
            id={ids.size}
            className={`input ${styles.short}`}
            value={form.size}
            onChange={(event) => set('size', event.target.value)}
            disabled={!form.sizeLimited}
            spellCheck={false}
          />
        </LimitChoice>
        {!forever && <p className={styles.hint}>{t('taproot_settings.retention.limits_hint')}</p>}
      </div>
      {forever && (
        <p className={styles.summary}>
          {t('taproot_settings.retention.keeps_all')}
          {diskBytes !== null &&
            ` ${t('taproot_settings.retention.disk_use', { size: formatBytes(diskBytes) })}`}
        </p>
      )}
      <label htmlFor={ids.exclude} className={styles.label}>
        {t('taproot_settings.retention.exclude_label')}
      </label>
      <textarea
        id={ids.exclude}
        className={`input ${styles.textarea}`}
        rows={3}
        value={form.excludePaths}
        onChange={(event) => set('excludePaths', event.target.value)}
        placeholder={'HEAD /api/hello\n/v1/models'}
        spellCheck={false}
      />
      <p className={styles.hint}>{t('taproot_settings.retention.exclude_hint')}</p>
      <ToggleSwitch
        checked={form.keepUnrouted404}
        onChange={(value) => set('keepUnrouted404', value)}
        label={t('taproot_settings.retention.keep_404')}
      />
    </SectionForm>
  );
}

/** "Keep forever" or a limit, with the limit's own field after its option. */
function LimitChoice({
  legend,
  limited,
  onChange,
  limitLabel,
  children,
}: {
  legend: string;
  limited: boolean;
  onChange: (limited: boolean) => void;
  limitLabel: string;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const name = useId();
  return (
    <fieldset className={styles.choice}>
      <legend className={styles.label}>{legend}</legend>
      <label className={styles.inline}>
        <input type="radio" name={name} checked={!limited} onChange={() => onChange(false)} />
        <span>{t('taproot_settings.retention.keep_forever')}</span>
      </label>
      <div className={styles.inline}>
        <label className={styles.inline}>
          <input type="radio" name={name} checked={limited} onChange={() => onChange(true)} />
          <span>{limitLabel}</span>
        </label>
        {children}
      </div>
    </fieldset>
  );
}

export function BackupsSection({
  settings,
  zone,
  save,
}: {
  settings: TaprootSettings['backups'];
  zone: ServerZone;
  save: Save;
}) {
  const { t } = useTranslation();
  const ids = { time: useId(), keep: useId() };
  const [form, setForm, dirty, reset] = useDraft<BackupsForm>(backupsForm(settings));
  const set = <K extends keyof BackupsForm>(key: K, value: BackupsForm[K]) =>
    setForm({ ...form, [key]: value });

  return (
    <SectionForm
      id="settings-backups"
      title={t('taproot_settings.backups.title')}
      description={t('taproot_settings.backups.description')}
      dirty={dirty}
      validate={() => backupsUpdate(form)}
      save={(update) => save({ backups: update })}
      onReset={reset}
    >
      <div className={styles.row}>
        <ToggleSwitch
          checked={form.scheduled}
          onChange={(value) => set('scheduled', value)}
          label={t('taproot_settings.backups.scheduled')}
        />
        <label htmlFor={ids.time} className={styles.inline}>
          <span>{t('taproot_settings.backups.at')}</span>
          <input
            id={ids.time}
            type="time"
            className={`input ${styles.short}`}
            value={form.dailyAt}
            onChange={(event) => set('dailyAt', event.target.value)}
            disabled={!form.scheduled}
          />
          <span className={styles.muted}>
            {t('taproot_settings.backups.server_time', { zone: serverZoneLabel(zone) })}
          </span>
        </label>
      </div>
      {settings.nextRunAt && <NextBackup nextRunAt={settings.nextRunAt} zone={zone} />}
      <label htmlFor={ids.keep} className={styles.inline}>
        <span>{t('taproot_settings.backups.keep')}</span>
        <input
          id={ids.keep}
          className={`input ${styles.short}`}
          inputMode="numeric"
          value={form.keep}
          onChange={(event) => set('keep', event.target.value)}
        />
        <span className={styles.muted}>{t('taproot_settings.backups.keep_unit')}</span>
      </label>
      <p className={styles.hint}>{t('taproot_settings.backups.keep_hint')}</p>
    </SectionForm>
  );
}

function IngestSection({ settings, save }: { settings: TaprootSettings['ingest']; save: Save }) {
  const { t } = useTranslation();
  const inputId = useId();
  const [interval, setPollInterval, dirty, reset] = useDraft(settings.pollInterval);

  return (
    <SectionForm
      id="settings-ingest"
      title={t('taproot_settings.ingest.title')}
      description={t('taproot_settings.ingest.description')}
      dirty={dirty}
      validate={() => ingestUpdate(interval)}
      save={(update) => save({ ingest: update })}
      onReset={reset}
    >
      <label htmlFor={inputId} className={styles.inline}>
        <span>{t('taproot_settings.ingest.label')}</span>
        <input
          id={inputId}
          className={`input ${styles.short}`}
          value={interval}
          onChange={(event) => setPollInterval(event.target.value)}
          spellCheck={false}
        />
      </label>
      <p className={styles.hint}>{t('taproot_settings.ingest.hint')}</p>
      {settings.restartRequired && (
        <div className="status-badge warning" role="status">
          {t('taproot_settings.ingest.restart_required', {
            active: settings.activePollInterval,
            saved: settings.pollInterval,
          })}
        </div>
      )}
    </SectionForm>
  );
}

function AboutSection({ settings, me }: { settings: TaprootSettings; me: TaprootMe | null }) {
  const { t } = useTranslation();
  const rows: [string, ReactNode][] = [
    [
      t('taproot_settings.about.you'),
      !me
        ? '—'
        : me.via === 'tailnet'
          ? t('taproot_settings.about.you_tailnet', {
              name: me.displayName || me.login,
              login: me.login,
              node: me.node,
            })
          : t(`taproot_settings.about.you_${me.via}`, { node: me.node }),
    ],
    [t('taproot_settings.about.server_version'), formatVersion(settings.info)],
    [t('taproot_settings.about.dashboard_version'), __APP_VERSION__ || 'dev'],
    [t('taproot_settings.about.data_dir'), <code key="data">{settings.info.dataDir}</code>],
    [t('taproot_settings.about.config_path'), <code key="cfg">{settings.info.configPath}</code>],
  ];
  return (
    <section className={styles.card} aria-labelledby="settings-about-title">
      <div className={styles.cardHead}>
        <h2 id="settings-about-title" className={styles.cardTitle}>
          {t('taproot_settings.about.title')}
        </h2>
      </div>
      <dl className={styles.about}>
        {rows.map(([label, value]) => (
          <div key={label} className={styles.aboutRow}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function formatVersion(info: TaprootSettings['info']): string {
  const parts = [info.version];
  if (info.commit && info.commit !== 'none') parts.push(info.commit.slice(0, 12));
  if (info.buildDate && info.buildDate !== 'unknown') parts.push(info.buildDate);
  return parts.join(' · ');
}
