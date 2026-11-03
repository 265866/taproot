import { useId, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { taprootApi, type TaprootSettingsUpdate } from '@/services/api/taproot';
import { useNotificationStore } from '@/stores';
import { getErrorMessage } from '@/utils/helpers';
import { alertsForm, alertsUpdate, type AlertsForm, type AlertsSettings } from './model/alertsForm';
import pageStyles from './TaprootSettingsPage.module.scss';
import styles from './AlertsSection.module.scss';

type Busy = 'save' | 'clear' | 'test' | null;

/**
 * Discord alerts: set, replace, or clear the webhook, switch alerts off, and send a test. The
 * saved webhook is shown only by its last characters.
 */
export function AlertsSection({
  settings,
  save,
}: {
  settings: AlertsSettings;
  save: (update: TaprootSettingsUpdate) => Promise<void>;
}) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const inputId = useId();
  const hintId = useId();
  const sourceKey = JSON.stringify(settings);
  const [state, setState] = useState<{ key: string; form: AlertsForm }>({
    key: sourceKey,
    form: alertsForm(settings),
  });
  const form = state.key === sourceKey ? state.form : alertsForm(settings);
  const setForm = (next: AlertsForm) => setState({ key: sourceKey, form: next });
  const [busy, setBusy] = useState<Busy>(null);
  const [error, setError] = useState('');

  const dirty = form.webhook.trim() !== '' || form.enabled !== settings.enabled;
  const title = t('taproot_settings.alerts.title');

  const run = async (kind: Exclude<Busy, null>, action: () => Promise<void>) => {
    setBusy(kind);
    setError('');
    try {
      await action();
    } catch (err: unknown) {
      setError(getErrorMessage(err));
    } finally {
      setBusy(null);
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const result = alertsUpdate(form, settings);
    if (!result.ok) {
      setError(t(`taproot_settings.errors.${result.error}`));
      return;
    }
    void run('save', async () => {
      await save({ alerts: result.update });
      showNotification(t('taproot_settings.saved', { section: title }), 'success');
    });
  };

  const clear = () =>
    void run('clear', async () => {
      await save({ alerts: { discordWebhook: '' } });
      showNotification(t('taproot_settings.alerts.cleared'), 'success');
    });

  const sendTest = () =>
    void run('test', async () => {
      await taprootApi.testAlert();
      showNotification(t('taproot_settings.alerts.test_sent'), 'success');
    });

  return (
    <form
      className={pageStyles.card}
      aria-labelledby="settings-alerts-title"
      onSubmit={submit}
      autoComplete="off"
    >
      <div className={pageStyles.cardHead}>
        <h2 id="settings-alerts-title" className={pageStyles.cardTitle}>
          {title}
        </h2>
        <p className={pageStyles.cardDescription}>{t('taproot_settings.alerts.description')}</p>
      </div>
      <div className={pageStyles.fields}>
        <div className={styles.status} role="status">
          <span
            className={`status-badge ${
              settings.webhookInvalid
                ? 'error'
                : settings.active
                  ? 'success'
                  : settings.webhookSet
                    ? 'warning'
                    : 'muted'
            }`}
          >
            {settings.webhookInvalid
              ? t('taproot_settings.alerts.state_invalid')
              : settings.active
                ? t('taproot_settings.alerts.state_active')
                : settings.webhookSet
                  ? t('taproot_settings.alerts.state_off')
                  : t('taproot_settings.alerts.state_none')}
          </span>
          {settings.webhookSet && (
            <span className={pageStyles.muted}>
              {t('taproot_settings.alerts.webhook_set', { hint: settings.webhookHint })}
            </span>
          )}
        </div>
        {settings.webhookInvalid && (
          <div className="error-box" role="alert">
            {t('taproot_settings.alerts.webhook_invalid')}
          </div>
        )}
        <label htmlFor={inputId} className={pageStyles.label}>
          {settings.webhookSet
            ? t('taproot_settings.alerts.replace_label')
            : t('taproot_settings.alerts.webhook_label')}
        </label>
        <input
          id={inputId}
          type="password"
          className="input"
          value={form.webhook}
          onChange={(event) => setForm({ ...form, webhook: event.target.value })}
          placeholder="https://discord.com/api/webhooks/…"
          autoComplete="off"
          spellCheck={false}
          aria-describedby={hintId}
        />
        <p id={hintId} className={pageStyles.hint}>
          {t('taproot_settings.alerts.webhook_hint')}
        </p>
        <ToggleSwitch
          checked={form.enabled}
          onChange={(enabled) => setForm({ ...form, enabled })}
          label={t('taproot_settings.alerts.enabled')}
          disabled={!settings.webhookSet && !form.webhook.trim()}
        />
      </div>
      {error && (
        <div className="error-box" role="alert">
          {error}
        </div>
      )}
      <div className={`${pageStyles.cardActions} ${styles.actions}`}>
        {settings.webhookSet && (
          <div className={styles.leading}>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              loading={busy === 'test'}
              disabled={busy !== null || settings.webhookInvalid}
              onClick={sendTest}
            >
              {t('taproot_settings.alerts.send_test')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              loading={busy === 'clear'}
              disabled={busy !== null}
              onClick={clear}
            >
              {t('taproot_settings.alerts.clear')}
            </Button>
          </div>
        )}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            setError('');
            setForm(alertsForm(settings));
          }}
          disabled={!dirty || busy !== null}
        >
          {t('taproot_settings.reset')}
        </Button>
        <Button
          type="submit"
          size="sm"
          loading={busy === 'save'}
          disabled={!dirty || busy !== null}
        >
          {t('taproot_settings.save')}
        </Button>
      </div>
    </form>
  );
}
