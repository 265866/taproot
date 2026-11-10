import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Modal } from '@/components/ui/Modal';
import { IconDownload, IconPlus } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { REFRESH_OK, refreshFailed, useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { formatBytes } from '@/features/taproot-logs/model/format';
import { ROUTES } from '@/router/routes';
import {
  taprootApi,
  type Backup,
  type RestoreResult,
  type TaprootSettings,
} from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';
import { getErrorMessage } from '@/utils/helpers';
import {
  RESTART_PROBE_MS,
  confirmationMatches,
  startRestartWatch,
  stepRestartWatch,
  summarizeBackups,
  type RestartWatch,
} from './model/backups';
import { formatCreatedAt, serverZoneLabel } from './model/schedule';
import { NextBackup } from './NextBackup';
import styles from './TaprootBackupsPage.module.scss';

export function TaprootBackupsPage() {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const [backups, setBackups] = useState<Backup[] | null>(null);
  const [error, setError] = useState('');
  const [settings, setSettings] = useState<TaprootSettings | null>(null);
  const [creating, setCreating] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<Backup | null>(null);
  const [restored, setRestored] = useState<RestoreResult | null>(null);
  const [watch, setWatch] = useState<RestartWatch | null>(null);

  // The schedule line is informational; the list still works without it.
  const loadSettings = useCallback(
    () =>
      taprootApi
        .settings()
        .then(setSettings)
        .catch(() => {}),
    []
  );

  const load = useCallback(async () => {
    const settingsLoaded = loadSettings();
    let outcome = REFRESH_OK;
    try {
      const res = await taprootApi.backups();
      setBackups(res.backups);
      setError('');
    } catch (err: unknown) {
      setError(getErrorMessage(err));
      outcome = refreshFailed(err);
    }
    await settingsLoaded;
    return outcome;
  }, [loadSettings]);

  useEffect(() => {
    void load();
  }, [load]);
  useHeaderRefresh(load);

  // After a restore that restarts the server, probe until it answers again.
  useEffect(() => {
    if (!watch || watch.phase === 'back' || watch.phase === 'gave_up') return;
    const timer = window.setTimeout(() => {
      taprootApi
        .backups()
        .then((res) => {
          setWatch((current) => current && stepRestartWatch(current, true));
          setBackups(res.backups);
          // The restore may have replaced taproot.yaml and with it the schedule.
          void loadSettings();
        })
        .catch(() => setWatch((current) => current && stepRestartWatch(current, false)));
    }, RESTART_PROBE_MS);
    return () => window.clearTimeout(timer);
  }, [watch, loadSettings]);

  const create = async () => {
    setCreating(true);
    try {
      const entry = await taprootApi.createBackup();
      showNotification(t('taproot_backups.created', { id: entry.id }), 'success');
      await load();
    } catch (err: unknown) {
      showNotification(
        t('taproot_backups.create_failed', { message: getErrorMessage(err) }),
        'error'
      );
    } finally {
      setCreating(false);
    }
  };

  const onRestored = (result: RestoreResult) => {
    setRestoreTarget(null);
    setRestored(result);
    if (result.restart === 'scheduled') setWatch(startRestartWatch());
    else void load();
  };

  const totals = summarizeBackups(backups ?? []);

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>{t('taproot_backups.title')}</h1>
          <p className={styles.description}>{t('taproot_backups.description')}</p>
        </div>
        <Button onClick={() => void create()} loading={creating}>
          <span className={styles.buttonContent}>
            <IconPlus size={16} />
            {t('taproot_backups.create')}
          </span>
        </Button>
      </header>

      <section className={styles.summary} aria-label={t('taproot_backups.summary_label')}>
        <div className={styles.figure}>
          <span className={styles.label}>{t('taproot_backups.count')}</span>
          <span className="numeral-lg">{backups ? totals.count : '—'}</span>
          <span className={styles.sub}>{formatBytes(totals.bytes)}</span>
        </div>
        <div className={styles.figure}>
          <span className={styles.label}>{t('taproot_backups.latest')}</span>
          <span className={`numeral-lg ${styles.latest}`}>
            {totals.newest ? formatCreatedAt(totals.newest.createdAt) : '—'}
          </span>
          <span className={styles.sub}>
            {totals.newest ? t(`taproot_backups.reason.${totals.newest.reason}`) : ''}
          </span>
        </div>
        <div className={styles.figure}>
          <span className={styles.label}>{t('taproot_backups.schedule')}</span>
          <span className={`numeral-lg ${styles.latest}`}>
            {!settings
              ? '—'
              : settings.backups.dailyAt === 'off'
                ? t('taproot_backups.schedule_off')
                : t('taproot_backups.schedule_daily', { time: settings.backups.dailyAt })}
          </span>
          <span className={styles.sub}>
            {settings &&
              t('taproot_backups.keep', {
                count: settings.backups.keep,
                zone: serverZoneLabel(settings.info),
              })}{' '}
            <Link to={ROUTES.settings}>{t('taproot_backups.change_schedule')}</Link>
          </span>
          {settings?.backups.nextRunAt && (
            <NextBackup nextRunAt={settings.backups.nextRunAt} zone={settings.info} />
          )}
        </div>
      </section>

      {restored && (
        <RestoreBanner result={restored} watch={watch} onDismiss={() => setRestored(null)} />
      )}
      {error && (
        <div className="error-box" role="alert">
          {mask(t('taproot_backups.load_failed', { message: error }))}
        </div>
      )}

      {backups === null && !error ? (
        <div className={styles.loading} role="status">
          {t('taproot_backups.loading')}
        </div>
      ) : backups && backups.length === 0 ? (
        <EmptyState
          title={t('taproot_backups.empty_title')}
          description={t('taproot_backups.empty_description')}
        />
      ) : backups ? (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">{t('taproot_backups.columns.created')}</th>
                <th scope="col">{t('taproot_backups.columns.reason')}</th>
                <th scope="col" className={styles.num}>
                  {t('taproot_backups.columns.size')}
                </th>
                <th scope="col" className={styles.num}>
                  {t('taproot_backups.columns.files')}
                </th>
                <th scope="col" className={styles.actionsHead}>
                  <span className={styles.srOnly}>{t('taproot_backups.columns.actions')}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {backups.map((backup) => (
                <tr key={backup.id}>
                  <td>
                    <div className={styles.created}>{formatCreatedAt(backup.createdAt)}</div>
                    <code className={styles.id}>{backup.id}</code>
                  </td>
                  <td>
                    <span className={`${styles.reason} ${styles[`reason_${backup.reason}`] ?? ''}`}>
                      {t(`taproot_backups.reason.${backup.reason}`)}
                    </span>
                  </td>
                  <td className={styles.num} data-label={t('taproot_backups.columns.size')}>
                    {formatBytes(backup.bytes)}
                  </td>
                  <td className={styles.num} data-label={t('taproot_backups.columns.files')}>
                    {backup.files}
                  </td>
                  <td className={styles.actions}>
                    <a
                      className="btn btn-secondary btn-sm"
                      href={taprootApi.backupDownloadUrl(backup.id)}
                      download
                      aria-label={t('taproot_backups.download_label', { id: backup.id })}
                    >
                      <span className={styles.buttonContent}>
                        <IconDownload size={14} />
                        {t('taproot_backups.download')}
                      </span>
                    </a>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setRestoreTarget(backup)}
                      aria-label={t('taproot_backups.restore_label', { id: backup.id })}
                    >
                      {t('taproot_backups.restore')}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {restoreTarget && (
        <RestoreDialog
          backup={restoreTarget}
          onClose={() => setRestoreTarget(null)}
          onRestored={onRestored}
          onFailed={() => void load()}
        />
      )}
    </div>
  );
}

function RestoreDialog({
  backup,
  onClose,
  onRestored,
  onFailed,
}: {
  backup: Backup;
  onClose: () => void;
  onRestored: (result: RestoreResult) => void;
  /** A restore can fail after it saved the pre-restore backup, so the list may have changed. */
  onFailed: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const inputId = useId();
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const ready = confirmationMatches(typed, backup.id);

  // Modal focuses its first control on a zero-delay timer; land on the id field after it.
  useEffect(() => {
    const timer = window.setTimeout(() => inputRef.current?.focus(), 50);
    return () => window.clearTimeout(timer);
  }, []);

  const restore = async () => {
    if (!ready || busy) return;
    setBusy(true);
    setError('');
    try {
      onRestored(await taprootApi.restoreBackup(backup.id));
    } catch (err: unknown) {
      setError(getErrorMessage(err));
      setBusy(false);
      onFailed();
    }
  };

  return (
    <Modal
      open
      onClose={busy ? () => undefined : onClose}
      closeDisabled={busy}
      title={t('taproot_backups.restore_title')}
      width={560}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </Button>
          <Button variant="danger" onClick={() => void restore()} disabled={!ready} loading={busy}>
            {t('taproot_backups.restore_confirm')}
          </Button>
        </>
      }
    >
      <form
        className={styles.dialog}
        onSubmit={(event) => {
          event.preventDefault();
          void restore();
        }}
      >
        <p className={styles.dialogLead}>
          {t('taproot_backups.restore_lead', {
            when: formatCreatedAt(backup.createdAt),
          })}
        </p>
        <ol className={styles.steps}>
          <li>{t('taproot_backups.restore_step_pre')}</li>
          <li>{t('taproot_backups.restore_step_files')}</li>
          <li>{t('taproot_backups.restore_step_db')}</li>
          <li>{t('taproot_backups.restore_step_restart')}</li>
        </ol>
        <label htmlFor={inputId} className={styles.confirmLabel}>
          {t('taproot_backups.restore_type')} <code>{backup.id}</code>
        </label>
        <input
          ref={inputRef}
          id={inputId}
          className="input"
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
          autoComplete="off"
          spellCheck={false}
          disabled={busy}
          aria-invalid={typed !== '' && !ready}
        />
        {error && (
          <div className={`error-box ${styles.dialogError}`} role="alert">
            {mask(t('taproot_backups.restore_failed', { message: error }))}
          </div>
        )}
      </form>
    </Modal>
  );
}

function RestoreBanner({
  result,
  watch,
  onDismiss,
}: {
  result: RestoreResult;
  watch: RestartWatch | null;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const restartText =
    result.restart === 'manual'
      ? t('taproot_backups.restart_manual')
      : watch?.phase === 'back'
        ? t('taproot_backups.restart_back')
        : watch?.phase === 'gave_up'
          ? t('taproot_backups.restart_gave_up')
          : t('taproot_backups.restart_waiting');
  return (
    <div className={styles.banner} role="status">
      <div>
        <strong>{t('taproot_backups.restored')}</strong>
        <p>{mask(result.message)}</p>
        <p>
          {t('taproot_backups.pre_restore_saved')} <code>{result.preRestoreBackupId}</code>
        </p>
        <p className={result.restart === 'manual' ? styles.bannerAction : ''}>{restartText}</p>
      </div>
      <Button size="sm" variant="ghost" onClick={onDismiss}>
        {t('common.close')}
      </Button>
    </div>
  );
}
