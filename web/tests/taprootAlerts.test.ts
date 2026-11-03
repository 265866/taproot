import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { AlertsSection } from '@/features/taproot-settings/AlertsSection';
import {
  alertsForm,
  alertsUpdate,
  isDiscordWebhook,
  type AlertsSettings,
} from '@/features/taproot-settings/model/alertsForm';
import { en } from '@/i18n/resources';
import { createTaprootClient } from '@/services/api/taproot';

const i18n = createInstance();
await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
const render = (settings: AlertsSettings) =>
  renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(AlertsSection, { settings, save: async () => {} })
    )
  );

const none: AlertsSettings = { enabled: true, webhookSet: false, webhookHint: '', active: false };
const active: AlertsSettings = {
  enabled: true,
  webhookSet: true,
  webhookHint: '…a1B2c3',
  active: true,
};
const hook = 'https://discord.com/api/webhooks/1234567890/abc_DEF-123';

describe('discord webhook check', () => {
  test('accepts the webhook URL forms Discord issues', () => {
    for (const url of [
      hook,
      'https://discordapp.com/api/webhooks/1/x',
      'https://canary.discord.com/api/webhooks/1/x',
      'https://discord.com/api/v10/webhooks/1/x',
    ])
      expect(isDiscordWebhook(url)).toBe(true);
  });

  test('refuses other hosts, schemes, ports, queries, and malformed paths', () => {
    for (const url of [
      'http://discord.com/api/webhooks/1/x',
      'https://discord.com.evil.test/api/webhooks/1/x',
      'https://discord.com:8443/api/webhooks/1/x',
      'https://u:p@discord.com/api/webhooks/1/x',
      'https://discord.com/api/webhooks/1/x?wait=true',
      'https://discord.com/api/webhooks/1/x#frag',
      'https://discord.com/api/webhooks/abc/x',
      'https://discord.com/api/webhooks/1',
      'not a url',
    ])
      expect(isDiscordWebhook(url)).toBe(false);
  });
});

describe('alerts update', () => {
  test('an untouched form sends nothing', () => {
    expect(alertsUpdate(alertsForm(active), active)).toEqual({ ok: true, update: {} });
  });

  test('sends a trimmed webhook and a changed switch', () => {
    expect(alertsUpdate({ webhook: `  ${hook} `, enabled: false }, active)).toEqual({
      ok: true,
      update: { discordWebhook: hook, enabled: false },
    });
  });

  test('refuses a URL that is not a Discord webhook', () => {
    expect(alertsUpdate({ webhook: 'https://example.com/hook', enabled: true }, none)).toEqual({
      ok: false,
      error: 'webhook_format',
    });
    expect(en.taproot_settings.errors.webhook_format).toContain('discord.com/api/webhooks');
  });
});

describe('alerts section', () => {
  test('without a webhook, offers only the field', () => {
    const html = render(none);
    expect(html).toContain('No webhook');
    expect(html).toContain('Discord webhook URL');
    expect(html).not.toContain('Send test');
    expect(html).not.toContain('Remove webhook');
  });

  test('with a webhook, shows its hint and the test and remove actions', () => {
    const html = render(active);
    expect(html).toContain('Webhook ending in …a1B2c3');
    expect(html).toContain('Replace the webhook');
    expect(html).toContain('Send test');
    expect(html).toContain('Remove webhook');
    expect(html).toContain('type="password"');
  });

  test('a saved webhook with alerts switched off reads as off', () => {
    expect(render({ ...active, enabled: false, active: false })).toContain('>Off<');
  });
});

describe('alerts client', () => {
  test('the test button posts to /alerts/test with the request header', async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    const client = createTaprootClient('/taproot/api/v1', (async (
      url: RequestInfo | URL,
      init?: RequestInit
    ) => {
      calls.push({ url: String(url), init: init ?? {} });
      return new Response('{"sent":true}', { status: 200 });
    }) as typeof fetch);
    expect(await client.testAlert()).toEqual({ sent: true });
    expect(calls[0].url).toBe('/taproot/api/v1/alerts/test');
    expect(calls[0].init.method).toBe('POST');
    expect((calls[0].init.headers as Record<string, string>)['X-Taproot-Request']).toBe('1');
  });
});
