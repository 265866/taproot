/**
 * The alerts section's form. The webhook field starts empty because the server never sends the
 * saved URL back; typing one replaces it.
 */
import type { TaprootSettings, TaprootSettingsUpdate } from '@/services/api/taproot';
import type { Validation } from './settingsForm';

export type AlertsSettings = NonNullable<TaprootSettings['alerts']>;
export type AlertsUpdate = NonNullable<TaprootSettingsUpdate['alerts']>;

export interface AlertsForm {
  webhook: string;
  enabled: boolean;
}

export function alertsForm(settings: AlertsSettings): AlertsForm {
  return { webhook: '', enabled: settings.enabled };
}

const WEBHOOK_HOSTS = new Set([
  'discord.com',
  'discordapp.com',
  'ptb.discord.com',
  'canary.discord.com',
]);
const WEBHOOK_PATH = /^\/api\/(v\d+\/)?webhooks\/\d+\/[A-Za-z0-9_-]+$/;

/** Mirrors the server's check in internal/taproot/alerts/config.go. */
export function isDiscordWebhook(raw: string): boolean {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return false;
  }
  return (
    url.protocol === 'https:' &&
    !url.username &&
    !url.password &&
    !url.port &&
    !url.search &&
    !url.hash &&
    !raw.includes('#') &&
    WEBHOOK_HOSTS.has(url.hostname.toLowerCase()) &&
    WEBHOOK_PATH.test(url.pathname)
  );
}

export function alertsUpdate(form: AlertsForm, settings: AlertsSettings): Validation<AlertsUpdate> {
  const update: AlertsUpdate = {};
  const webhook = form.webhook.trim();
  if (webhook) {
    if (!isDiscordWebhook(webhook)) return { ok: false, error: 'webhook_format' };
    update.discordWebhook = webhook;
  }
  if (form.enabled !== settings.enabled) update.enabled = form.enabled;
  return { ok: true, update };
}
