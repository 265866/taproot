import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { I18nextProvider } from 'react-i18next';
import { createInstance } from 'i18next';
import { SecretFieldInput } from '../src/features/plugins/components/SecretFieldInput';
import { en } from '../src/i18n/resources';
import {
  buildPluginConfigDraft,
  buildPluginConfigPatch,
  isSecretPluginField,
} from '../src/features/plugins/pluginConfigDraft';
import type { PluginConfigField } from '../src/types';

const fields: PluginConfigField[] = [
  { name: 'mixed', type: 'array', enumValues: [], description: '' },
  { name: 'optional', type: 'boolean', enumValues: [], description: '' },
  { name: 'label', type: 'string', enumValues: [], description: '' },
];

const t = (key: string) => key;

describe('plugin config draft', () => {
  test('represents arrays as JSON without marking missing booleans as touched', () => {
    const draft = buildPluginConfigDraft(
      { enabled: true, configFields: fields },
      { priority: 3, mixed: [1, true, { x: 1 }] }
    );

    expect(draft.values.mixed).toBe('[\n  1,\n  true,\n  {\n    "x": 1\n  }\n]');
    expect(draft.values.optional).toBe(false);
    expect(draft.touchedFields).toEqual({});
  });

  test('only patches touched base fields and preserves untouched plugin values', () => {
    const draft = buildPluginConfigDraft(
      { enabled: true, configFields: fields },
      { priority: 3, mixed: [1, true, { x: 1 }] }
    );
    draft.priority = '5';
    draft.priorityTouched = true;

    expect(buildPluginConfigPatch(draft, fields, t)).toEqual({
      patch: { priority: 5 },
      errors: {},
    });
  });

  test('parses touched array JSON without coercing item types', () => {
    const draft = buildPluginConfigDraft(
      { enabled: true, configFields: fields },
      { mixed: ['old'] }
    );
    draft.values.mixed = '[1, false, {"next": 2}]';
    draft.touchedFields.mixed = true;

    expect(buildPluginConfigPatch(draft, fields, t)).toEqual({
      patch: { mixed: [1, false, { next: 2 }] },
      errors: {},
    });
  });

  test('writes a missing boolean only after the user touches it', () => {
    const draft = buildPluginConfigDraft({ enabled: true, configFields: fields }, {});
    draft.touchedFields.optional = true;

    expect(buildPluginConfigPatch(draft, fields, t)).toEqual({
      patch: { optional: false },
      errors: {},
    });
  });

  test('deletes a cleared touched field with null', () => {
    const draft = buildPluginConfigDraft(
      { enabled: true, configFields: fields },
      { label: 'keep me' }
    );
    draft.values.label = '   ';
    draft.touchedFields.label = true;

    expect(buildPluginConfigPatch(draft, fields, t)).toEqual({
      patch: { label: null },
      errors: {},
    });
  });

  test('rejects non-array JSON for an array field', () => {
    const draft = buildPluginConfigDraft(
      { enabled: true, configFields: fields },
      { mixed: ['old'] }
    );
    draft.values.mixed = '{"not":"an array"}';
    draft.touchedFields.mixed = true;

    expect(buildPluginConfigPatch(draft, fields, t)).toEqual({
      patch: {},
      errors: { mixed: 'plugin_management.expected_array' },
    });
  });
});

describe('plugin secret fields', () => {
  const field = (name: string, type = 'string'): PluginConfigField => ({
    name,
    type,
    enumValues: [],
    description: '',
  });

  test('masks string fields whose names hold secrets', () => {
    for (const name of ['api_key', 'apiKey', 'token', 'client-secret', 'password', 'private_key']) {
      expect(isSecretPluginField(field(name))).toBe(true);
    }
    for (const name of ['label', 'base_url', 'model']) {
      expect(isSecretPluginField(field(name))).toBe(false);
    }
    expect(isSecretPluginField(field('token_limit', 'integer'))).toBe(false);
  });

  test('matches whole name segments, including camelCase and dotted names', () => {
    for (const name of [
      'accessToken',
      'APIKey',
      'x-api-key',
      'auth.token',
      'clientSecret',
      'apikey',
      'DB_PASSWORD',
      'service_credentials',
    ]) {
      expect(isSecretPluginField(field(name))).toBe(true);
    }
    for (const name of ['max_tokens', 'tokenizer', 'secretariat', 'passwords_policy', 'monkey']) {
      expect(isSecretPluginField(field(name))).toBe(false);
    }
  });

  test('leaves names that point at a secret or count something readable', () => {
    for (const name of [
      'api_key_env',
      'secret_name',
      'credentials_file',
      'private_key_path',
      'token_url',
      'token_limit',
      'apiKeyEnv',
    ]) {
      expect(isSecretPluginField(field(name))).toBe(false);
    }
  });
});

describe('masked plugin field', () => {
  test('starts hidden and offers a named show/hide toggle', async () => {
    const i18n = createInstance();
    await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
    const markup = renderToStaticMarkup(
      createElement(
        I18nextProvider,
        { i18n },
        createElement(SecretFieldInput, {
          id: 'plugin-field-api_key',
          label: 'api_key',
          value: 'sk-example',
          onChange: () => {},
        })
      )
    );
    expect(markup).toContain('type="password"');
    expect(markup).toContain('aria-label="Show api_key"');
    expect(markup).toContain('aria-pressed="false"');
    expect(markup).toContain('aria-controls="plugin-field-api_key"');
  });
});
