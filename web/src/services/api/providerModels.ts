import type { ModelAlias } from '@/types/provider';
import { isRecord } from '@/utils/helpers';

const MODEL_BOOLEAN_FIELDS = [
  ['forceMapping', 'force-mapping'],
  ['isCompat', 'is-compat'],
  ['supportConfigurationUpdate', 'support-configuration-update'],
  ['useMaxCompletionTokens', 'use-max-completion-tokens'],
] as const;

export function normalizeModelOptions(raw: Record<string, unknown>): Partial<ModelAlias> {
  const result: Partial<ModelAlias> = {};
  if (typeof raw['display-name'] === 'string') result.displayName = raw['display-name'];
  const context = raw['max-context-length'];
  if (typeof context === 'number' && Number.isSafeInteger(context))
    result.maxContextLength = context;
  for (const [key, wire] of MODEL_BOOLEAN_FIELDS) {
    const value = raw[wire];
    if (typeof value === 'boolean') result[key] = value;
  }
  for (const [key, wire] of [
    ['inputModalities', 'input-modalities'],
    ['outputModalities', 'output-modalities'],
  ] as const) {
    const value = raw[wire];
    if (Array.isArray(value))
      result[key] = value.filter((item): item is string => typeof item === 'string');
  }
  return result;
}

/** Management v8 JSON is decoded as YAML; its field names follow the YAML tags. */
export function normalizeModelThinking(value: unknown): Record<string, unknown> | undefined {
  if (!isRecord(value)) return undefined;
  const result = { ...value };
  for (const [key, wire] of [
    ['zero_allowed', 'zero-allowed'],
    ['dynamic_allowed', 'dynamic-allowed'],
  ] as const) {
    if (wire in result) {
      result[key] = result[wire];
      delete result[wire];
    }
  }
  return result;
}

export function serializeModelThinking(value: Record<string, unknown>): Record<string, unknown> {
  const result = { ...value };
  for (const [key, wire] of [
    ['zero_allowed', 'zero-allowed'],
    ['dynamic_allowed', 'dynamic-allowed'],
  ] as const) {
    if (key in result) {
      result[wire] = result[key];
      delete result[key];
    }
  }
  return result;
}
