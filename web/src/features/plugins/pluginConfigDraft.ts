import type { PluginConfigField, PluginConfigObject, PluginListEntry } from '@/types';
import { isRecord } from '@/utils/helpers';

export type PluginDraftValue = string | boolean;

export interface PluginConfigDraft {
  enabled: boolean;
  priority: string;
  values: Record<string, PluginDraftValue>;
  errors: Record<string, string>;
  enabledTouched: boolean;
  priorityTouched: boolean;
  touchedFields: Record<string, boolean>;
}

// Two signatures, not an optional parameter: i18next 27's TFunction accepts a key with or
// without options, but not an options argument that may be undefined.
type Translate = {
  (key: string): string;
  (key: string, options: Record<string, unknown>): string;
};

export const normalizePluginConfigFieldType = (field: PluginConfigField): string =>
  field.type.trim().toLowerCase();

// The plugin API has no secret field type, so mask by name, as a password manager would. A name
// segment that ends with a secret word counts, so accesstoken and clientsecret are masked, while
// max_tokens and tokenizer stay readable.
const SECRET_ENDINGS = [
  'secret',
  'token',
  'password',
  'passwd',
  'credential',
  'credentials',
  'apikey',
  'apikeys',
  'privatekey',
  'privatekeys',
];
const KEY_QUALIFIERS = new Set(['api', 'private']);
// A name ending in one of these holds a reference to a secret or a number, not the secret itself.
const NON_SECRET_SUFFIXES = new Set(['env', 'file', 'path', 'name', 'url', 'limit']);

const fieldNameSegments = (name: string): string[] =>
  name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .toLowerCase()
    .split(/[\s_.-]+/)
    .filter(Boolean);

export const isSecretPluginField = (field: PluginConfigField): boolean => {
  if (normalizePluginConfigFieldType(field) !== 'string') return false;
  const segments = fieldNameSegments(field.name);
  if (segments.length === 0 || NON_SECRET_SUFFIXES.has(segments[segments.length - 1])) {
    return false;
  }
  return segments.some(
    (segment, index) =>
      SECRET_ENDINGS.some((ending) => segment.endsWith(ending)) ||
      ((segment === 'key' || segment === 'keys') && KEY_QUALIFIERS.has(segments[index - 1]))
  );
};

const stringifyJSONValue = (value: unknown): string => {
  if (value === undefined || value === null) return '';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
};

const getFieldDraftValue = (field: PluginConfigField, value: unknown): PluginDraftValue => {
  const type = normalizePluginConfigFieldType(field);
  if (type === 'boolean') return value === true;
  if (type === 'array' || type === 'object') return stringifyJSONValue(value);
  if (value === undefined || value === null) return '';
  return String(value);
};

export function buildPluginConfigDraft(
  plugin: Pick<PluginListEntry, 'enabled' | 'configFields'>,
  currentConfig: PluginConfigObject
): PluginConfigDraft {
  const enabled =
    typeof currentConfig.enabled === 'boolean' ? currentConfig.enabled : plugin.enabled;
  const priority =
    typeof currentConfig.priority === 'number' || typeof currentConfig.priority === 'string'
      ? String(currentConfig.priority)
      : '0';
  const values: PluginConfigDraft['values'] = {};

  plugin.configFields.forEach((field) => {
    values[field.name] = getFieldDraftValue(field, currentConfig[field.name]);
  });

  return {
    enabled,
    priority,
    values,
    errors: {},
    enabledTouched: false,
    priorityTouched: false,
    touchedFields: {},
  };
}

const parseJSONField = (
  text: string,
  fieldType: string,
  fieldName: string,
  t: Translate,
  errors: Record<string, string>
) => {
  try {
    const parsed = JSON.parse(text);
    if (fieldType === 'array' && !Array.isArray(parsed)) {
      errors[fieldName] = t('plugin_management.expected_array');
      return undefined;
    }
    if (fieldType === 'object' && !isRecord(parsed)) {
      errors[fieldName] = t('plugin_management.expected_object');
      return undefined;
    }
    return parsed;
  } catch {
    errors[fieldName] = t('plugin_management.invalid_json');
    return undefined;
  }
};

export function buildPluginConfigPatch(
  draft: PluginConfigDraft,
  fields: PluginConfigField[],
  t: Translate
): { patch: PluginConfigObject; errors: Record<string, string> } {
  const errors: Record<string, string> = {};
  const patch: PluginConfigObject = {};

  if (draft.enabledTouched) patch.enabled = draft.enabled;

  if (draft.priorityTouched) {
    const priorityText = draft.priority.trim();
    if (!priorityText) {
      patch.priority = 0;
    } else if (!/^-?\d+$/.test(priorityText)) {
      errors.priority = t('plugin_management.invalid_priority');
    } else {
      patch.priority = Number.parseInt(priorityText, 10);
    }
  }

  fields.forEach((field) => {
    if (!draft.touchedFields[field.name]) return;

    const fieldType = normalizePluginConfigFieldType(field);
    const value = draft.values[field.name];

    if (fieldType === 'boolean') {
      patch[field.name] = value === true;
      return;
    }

    const text = typeof value === 'string' ? value.trim() : '';
    if (!text) {
      patch[field.name] = null;
      return;
    }

    if (fieldType === 'enum') {
      if (field.enumValues.length > 0 && !field.enumValues.includes(text)) {
        errors[field.name] = t('plugin_management.invalid_enum');
        return;
      }
      patch[field.name] = text;
      return;
    }

    if (fieldType === 'number') {
      const parsed = Number(text);
      if (!Number.isFinite(parsed)) {
        errors[field.name] = t('plugin_management.invalid_number');
        return;
      }
      patch[field.name] = parsed;
      return;
    }

    if (fieldType === 'integer') {
      if (!/^-?\d+$/.test(text)) {
        errors[field.name] = t('plugin_management.invalid_integer');
        return;
      }
      patch[field.name] = Number.parseInt(text, 10);
      return;
    }

    if (fieldType === 'array' || fieldType === 'object') {
      const parsed = parseJSONField(text, fieldType, field.name, t, errors);
      if (!errors[field.name]) patch[field.name] = parsed;
      return;
    }

    patch[field.name] = text;
  });

  return { patch, errors };
}
