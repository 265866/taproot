import { beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { en } from '@/i18n/resources';
import { AuthFileModelsList } from '@/features/authFiles/components/AuthFileModelsList';
import { isEditorBackEscape } from '@/features/authFiles/oauthEditorState';
import { maskCredentialSecrets, SECRET_PLACEHOLDER } from '@/features/authFiles/secretMask';
import { pageNow } from '@/features/accounts/model';
import { createOAuthAttempts, MAX_RETRY_DELAY_MS } from '@/features/oauth/oauthAttempts';
import { isTransientPollError } from '@/features/oauth/hooks/useOAuthFlows';
import {
  callbackExampleFromAuthUrl,
  isCallbackFromOtherAttempt,
} from '@/features/oauth/callbackUrl';
import { OAUTH_PROVIDERS, CALLBACK_SUPPORTED } from '@/features/oauth/providers';

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('Manage → Models', () => {
  test('marks no listed model disabled, because the server lists only served models', () => {
    const markup = renderToStaticMarkup(
      createElement(AuthFileModelsList, {
        fileType: 'claude',
        loading: false,
        error: null,
        models: [{ id: 'claude-opus-4-6' }, { id: 'claude-3-5-haiku-20241022' }],
        excluded: { claude: ['claude-opus-4-6', 'claude-3-*'] },
        onCopyText: () => {},
      } as Parameters<typeof AuthFileModelsList>[0])
    );
    expect(markup).toContain('claude-opus-4-6');
    expect(markup).not.toContain('Disabled');
    expect(markup).not.toContain('is disabled');
  });
});

describe('Accounts clock', () => {
  test('health uses the ticking clock, not a quota fetch minutes old', () => {
    const fetchedAt = 1_000_000;
    expect(pageNow(fetchedAt + 4 * 60_000, fetchedAt)).toBe(fetchedAt + 4 * 60_000);
    expect(pageNow(fetchedAt - 30_000, fetchedAt)).toBe(fetchedAt);
    expect(pageNow(fetchedAt, undefined)).toBe(fetchedAt);
  });
});

describe('OAuth editor Escape', () => {
  const key = (target: unknown, defaultPrevented = false) => ({
    key: 'Escape',
    defaultPrevented,
    target: target as EventTarget | null,
  });

  test('leaves only when nothing else claims the key', () => {
    expect(isEditorBackEscape(key({ tagName: 'BODY' }), false)).toBe(true);
    expect(isEditorBackEscape(key({ tagName: 'BUTTON' }), false)).toBe(true);
    expect(isEditorBackEscape(key({ tagName: 'INPUT' }), false)).toBe(false);
    expect(isEditorBackEscape(key({ tagName: 'textarea' }), false)).toBe(false);
    expect(isEditorBackEscape(key({ tagName: 'DIV', isContentEditable: true }), false)).toBe(false);
    expect(isEditorBackEscape(key({ tagName: 'BODY' }, true), false)).toBe(false);
    expect(isEditorBackEscape(key({ tagName: 'BODY' }), true)).toBe(false);
    expect(isEditorBackEscape({ ...key(null), key: 'Enter' }, false)).toBe(false);
  });
});

describe('credential secrets under privacy mode', () => {
  test('hides token, secret, and key values, nested or not, and keeps the rest', () => {
    const text = JSON.stringify(
      {
        type: 'claude',
        email: 'alice@example.com',
        access_token: 'demo-access',
        refresh_token: 'demo-refresh',
        id_token: 'demo-id',
        token_type: 'Bearer',
        expires_in: 3600,
        token_uri: 'https://oauth2.example.com/token',
        nested: { api_key: 'demo-key', client_secret: 'demo-secret', cookie: 'a=b' },
        note: 'has "quotes" and \\ backslash',
      },
      null,
      2
    );
    const masked = maskCredentialSecrets(text);
    for (const secret of ['demo-access', 'demo-refresh', 'demo-id', 'demo-key', 'demo-secret']) {
      expect(masked).not.toContain(secret);
    }
    expect(masked).not.toContain('a=b');
    const parsed = JSON.parse(masked) as Record<string, unknown>;
    expect(parsed.access_token).toBe(SECRET_PLACEHOLDER);
    expect(parsed.token_type).toBe('Bearer');
    expect(parsed.token_uri).toBe('https://oauth2.example.com/token');
    expect(parsed.email).toBe('alice@example.com');
    expect(parsed.note).toBe('has "quotes" and \\ backslash');
  });
});

describe('OAuth status polling', () => {
  test('retries a failed status request with a growing delay, then resumes', async () => {
    const tasks: Array<{ callback: () => void; delay: number }> = [];
    const attempts = createOAuthAttempts({
      setTimeout: (callback, delay) => tasks.push({ callback, delay }),
      clearTimeout: () => {},
    });
    const outcomes = ['fail', 'fail', 'fail', 'fail', 'wait', 'fail', 'ok'];
    const results: string[] = [];
    const attempt = attempts.begin('anthropic');
    attempt.poll(
      async () => {
        const next = outcomes.shift();
        if (next === 'fail') throw new Error('Network Error');
        return next;
      },
      (result) => {
        results.push(String(result));
        return result === 'wait';
      },
      () => true,
      3000
    );
    const delays: number[] = [];
    while (tasks.length) {
      const task = tasks.shift()!;
      delays.push(task.delay);
      task.callback();
      await Promise.resolve();
      await Promise.resolve();
    }
    expect(delays).toEqual([3000, 6000, 12000, 24000, MAX_RETRY_DELAY_MS, 3000, 6000]);
    expect(results).toEqual(['wait', 'ok']);
    expect(attempt.isPolling()).toBe(false);
  });

  test('only network, timeout, rate-limit, and server errors are worth a retry', () => {
    expect(isTransientPollError(new Error('Network Error'))).toBe(true);
    expect(isTransientPollError({ status: 502 })).toBe(true);
    expect(isTransientPollError({ status: 429 })).toBe(true);
    expect(isTransientPollError({ status: 408 })).toBe(true);
    expect(isTransientPollError({ status: 401 })).toBe(false);
    expect(isTransientPollError({ status: 403 })).toBe(false);
    expect(isTransientPollError({ status: 400 })).toBe(false);
  });
});

describe('OAuth callback paste', () => {
  test('a pasted address from another attempt is caught before it is sent', () => {
    const pasted = 'http://localhost:54545/callback?code=c&state=old-state';
    expect(isCallbackFromOtherAttempt(pasted, 'new-state')).toBe(true);
    expect(isCallbackFromOtherAttempt(pasted, 'old-state')).toBe(false);
    expect(isCallbackFromOtherAttempt('http://localhost:54545/callback?code=c', 'x')).toBe(false);
    expect(isCallbackFromOtherAttempt('abc123', 'x')).toBe(false);
  });

  test('the placeholder uses the redirect the provider will really use', () => {
    const authUrl =
      'https://claude.ai/oauth/authorize?client_id=x&redirect_uri=' +
      encodeURIComponent('http://localhost:54545/callback') +
      '&state=s';
    expect(callbackExampleFromAuthUrl(authUrl)).toBe(
      'http://localhost:54545/callback?code=...&state=...'
    );
    expect(callbackExampleFromAuthUrl('https://example.com/device')).toBeNull();
    expect(callbackExampleFromAuthUrl('not a url')).toBeNull();
    expect(callbackExampleFromAuthUrl(undefined)).toBeNull();
  });

  test('the copy says the paste is required and nothing happens automatically', () => {
    const login = en.auth_login as Record<string, string>;
    expect(login.required_step).toBe('Required');
    expect(login.oauth_callback_hint).toContain('Copy the whole address');
    expect(login.oauth_callback_hint).not.toMatch(/remote browser mode/i);
    expect(login.oauth_callback_placeholder).not.toContain('1455');
    for (const provider of OAUTH_PROVIDERS.filter((p) => CALLBACK_SUPPORTED.has(p.id))) {
      const hint = login[`${provider.id.replace('-', '_')}_oauth_hint`];
      expect(hint).toBeTruthy();
      expect(hint).not.toMatch(/automatically/i);
    }
    expect(login.oauth_callback_expired).not.toMatch(/update CLI Proxy API/i);
  });
});
