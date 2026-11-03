import { useCallback, useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { IconX } from '@/components/ui/icons';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import {
  taprootApi,
  type TaprootMe,
  type TaprootSettings,
  type TaprootSettingsUpdate,
} from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';
import { getErrorMessage } from '@/utils/helpers';
import {
  accessUpdate,
  backupsForm,
  backupsUpdate,
  ingestUpdate,
  normalizeLogin,
  ownLogin,
  retentionForm,
  retentionUpdate,
  type BackupsForm,
  type RetentionForm,
  type Validation,
} from './model/settingsForm';
import styles from './TaprootSettingsPage.module.scss';

type Save = (update: TaprootSettingsUpdate) => Promise<void>;

export function TaprootSettingsPage() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<TaprootSettings | null>(null);
  const [me, setMe] = useState<TaprootMe | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const [next, who] = await Promise.all([taprootApi.settings(), taprootApi.me()]);
      setSettings(next);
      setMe(who);
      setError('');
    } catch (err: unknown) {
      setError(getErrorMessage(err));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useHeaderRefresh(load);

  const save: Save = useCallback(async (update) => {
    setSettings(await taprootApi.putSettings(update));
  }, []);

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
          <RetentionSection settings={settings.requestLogs} save={save} />
          <BackupsSection settings={settings.backups} save={save} />
          <IngestSection settings={settings.ingest} save={save} />
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
  const inputId = useId();
  const [logins, setLogins, dirty, reset] = useDraft(settings.allowLogins);
  const [adding, setAdding] = useState('');
  const own = ownLogin(me);

  const add = () => {
    const login = normalizeLogin(adding);
    if (!login) return;
    if (!logins.includes(login)) setLogins([...logins, login]);
    setAdding('');
  };

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
      <ul className={styles.chips} aria-label={t('taproot_settings.access.list_label')}>
        {logins.map((login) => {
          const isOwn = login === own;
          return (
            <li key={login} className={styles.chip}>
              <span>{login}</span>
              {isOwn && <span className={styles.you}>{t('taproot_settings.access.you')}</span>}
              <button
                type="button"
                className={styles.chipRemove}
                onClick={() => setLogins(logins.filter((value) => value !== login))}
                disabled={isOwn || !settings.enabled}
                aria-label={t('taproot_settings.access.remove', { login })}
                title={isOwn ? t('taproot_settings.access.cannot_remove_self') : undefined}
              >
                <IconX size={12} />
              </button>
            </li>
          );
        })}
      </ul>
      <div className={styles.addRow}>
        <label htmlFor={inputId} className={styles.srOnly}>
          {t('taproot_settings.access.add_label')}
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
          placeholder={t('taproot_settings.access.add_placeholder')}
          autoComplete="off"
          spellCheck={false}
          disabled={!settings.enabled}
        />
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={add}
          disabled={!adding.trim() || !settings.enabled}
        >
          {t('taproot_settings.access.add')}
        </Button>
      </div>
      <p className={styles.hint}>
        {own
          ? t('taproot_settings.access.own_hint', { login: own })
          : t('taproot_settings.access.local_hint')}
      </p>
    </SectionForm>
  );
}

function RetentionSection({
  settings,
  save,
}: {
  settings: TaprootSettings['requestLogs'];
  save: Save;
}) {
  const { t } = useTranslation();
  const ids = { days: useId(), size: useId(), exclude: useId() };
  const [form, setForm, dirty, reset] = useDraft<RetentionForm>(retentionForm(settings));
  const set = <K extends keyof RetentionForm>(key: K, value: RetentionForm[K]) =>
    setForm({ ...form, [key]: value });

  return (
    <SectionForm
      id="settings-retention"
      title={t('taproot_settings.retention.title')}
      description={t('taproot_settings.retention.description')}
      dirty={dirty}
      validate={() => retentionUpdate(form)}
      save={(update) => save({ requestLogs: update })}
      onReset={reset}
    >
      <div className={styles.row}>
        <ToggleSwitch
          checked={form.daysLimited}
          onChange={(value) => set('daysLimited', value)}
          label={t('taproot_settings.retention.days_limited')}
        />
        <label htmlFor={ids.days} className={styles.inline}>
          <input
            id={ids.days}
            className={`input ${styles.short}`}
            inputMode="numeric"
            value={form.days}
            onChange={(event) => set('days', event.target.value)}
            disabled={!form.daysLimited}
          />
          <span>{t('taproot_settings.retention.days_unit')}</span>
        </label>
      </div>
      <div className={styles.row}>
        <ToggleSwitch
          checked={form.sizeLimited}
          onChange={(value) => set('sizeLimited', value)}
          label={t('taproot_settings.retention.size_limited')}
        />
        <label htmlFor={ids.size} className={styles.inline}>
          <span className={styles.srOnly}>{t('taproot_settings.retention.size_label')}</span>
          <input
            id={ids.size}
            className={`input ${styles.short}`}
            value={form.size}
            onChange={(event) => set('size', event.target.value)}
            disabled={!form.sizeLimited}
            spellCheck={false}
          />
        </label>
      </div>
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

function BackupsSection({ settings, save }: { settings: TaprootSettings['backups']; save: Save }) {
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
          <span className={styles.muted}>{t('taproot_settings.backups.server_time')}</span>
        </label>
      </div>
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
