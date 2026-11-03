/**
 * Routing board API.
 *
 * Normalizes the credential list into routing members and the stored v8 configuration into
 * routing settings, and serializes a routing plan back. There is no backend transaction across
 * credentials and configuration: credentials are written one by one first (each can be retried on
 * its own), then the settings go out as a single `PATCH /config`.
 */

import { apiClient } from './client';
import { authFilesApi } from './authFiles';
import { guardConfigConnection } from './configValue';
import type { AuthFileItem } from '@/types/authFile';
import {
  type RoutingBoard,
  type RoutingMember,
  type RoutingPlan,
  type RoutingSettings,
  type RoutingStrategy,
} from '@/types/routing';
import { isRecord } from '@/utils/helpers';

const MODEL_REQUEST_CONCURRENCY = 4;

/* ---------------------------------------------------------------------------------------------
 * Reading
 * ------------------------------------------------------------------------------------------- */

const readInteger = (value: unknown): number | null => {
  if (typeof value === 'number' && Number.isInteger(value)) return value;
  if (typeof value === 'string' && /^[+-]?\d+$/.test(value.trim())) return Number(value.trim());
  return null;
};

const readText = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const readPath = (root: unknown, path: readonly string[]): unknown =>
  path.reduce<unknown>((value, key) => (isRecord(value) ? value[key] : undefined), root);

export function normalizeRoutingStrategy(value: unknown): RoutingStrategy {
  const text = readText(value)
    .toLowerCase()
    .replace(/[\s_]+/g, '-');
  if (['weighted-round-robin', 'weightedroundrobin', 'wrr'].includes(text)) {
    return 'weighted-round-robin';
  }
  if (['fill-first', 'fillfirst', 'ff'].includes(text)) return 'fill-first';
  if (['soonest-reset-first', 'soonestresetfirst', 'srf'].includes(text)) {
    return 'soonest-reset-first';
  }
  // CPA runs an unrecognised strategy as round-robin.
  return 'round-robin';
}

export function readRoutingSettings(raw: unknown): RoutingSettings {
  const routing = readPath(raw, ['routing']);
  return {
    strategy: normalizeRoutingStrategy(readPath(routing, ['strategy'])),
    sessionAffinity: readPath(routing, ['session-affinity']) === true,
    requestRetry: Math.max(0, readInteger(readPath(routing, ['retry', 'request-retry'])) ?? 0),
    maxRetryCredentials: Math.max(
      0,
      readInteger(readPath(routing, ['retry', 'max-retry-credentials'])) ?? 0
    ),
  };
}

const isTruthyFlag = (value: unknown): boolean =>
  value === true || (typeof value === 'string' && value.trim().toLowerCase() === 'true');

const stripJsonExtension = (name: string): string =>
  name.length > 5 && name.toLowerCase().endsWith('.json') ? name.slice(0, -5) : name;

function credentialMember(file: AuthFileItem): RoutingMember | null {
  const name = readText(file.name);
  if (!name) return null;
  // Identity fallback matches the credential page: email, then project, then the file name.
  // Never show account fields: API-key credentials may expose the key through them.
  const base = stripJsonExtension(name);
  const primary = readText(file.email) || readText(file.projectId) || base;
  return {
    key: `cred:${name}`,
    credentialName: name,
    family: readText(file.type) || readText(file.provider) || 'unknown',
    name: primary,
    detail: primary.toLowerCase() === base.toLowerCase() ? '' : base,
    priority: readInteger(file.priority) ?? 0,
    weights: [{ index: 0, weight: readInteger(file.weight), label: primary }],
    disabled: file.disabled === true,
    locked: isTruthyFlag(file.runtimeOnly),
    prefix: readText(file.prefix),
    unavailable: file.unavailable === true,
    cooldowns: file.cooldownSnapshot ?? null,
  };
}

/** Every credential as a schedulable member, plus the stored routing settings. */
export function buildRoutingBoard(raw: unknown, files: readonly AuthFileItem[]): RoutingBoard {
  return {
    members: files.flatMap((file) => credentialMember(file) ?? []),
    settings: readRoutingSettings(raw),
  };
}

/* ---------------------------------------------------------------------------------------------
 * Writing
 * ------------------------------------------------------------------------------------------- */

export interface CredentialWrite {
  key: string;
  name: string;
  fields: { priority?: number; weight?: number | null };
}

export function credentialWrites(board: RoutingBoard, plan: RoutingPlan): CredentialWrite[] {
  return board.members.flatMap((member) => {
    const fields: CredentialWrite['fields'] = {};
    if (plan.priorities[member.key] !== undefined) fields.priority = plan.priorities[member.key];
    if (plan.weights[member.key]) fields.weight = plan.weights[member.key][0] ?? null;
    return Object.keys(fields).length
      ? [{ key: member.key, name: member.credentialName, fields }]
      : [];
  });
}

/** The single `PATCH /config` body for the routing settings, or null when none changed. */
export function buildRoutingConfigPatch(plan: RoutingPlan): Record<string, unknown> | null {
  const patch: Record<string, unknown> = {};

  const routing: Record<string, unknown> = {};
  const retry: Record<string, unknown> = {};
  if (plan.settings.strategy !== undefined) routing.strategy = plan.settings.strategy;
  if (plan.settings.sessionAffinity !== undefined) {
    routing['session-affinity'] = plan.settings.sessionAffinity;
  }
  if (plan.settings.requestRetry !== undefined) retry['request-retry'] = plan.settings.requestRetry;
  if (plan.settings.maxRetryCredentials !== undefined) {
    retry['max-retry-credentials'] = plan.settings.maxRetryCredentials;
  }
  if (Object.keys(retry).length) routing.retry = retry;
  if (Object.keys(routing).length) patch.routing = routing;

  return Object.keys(patch).length ? patch : null;
}

/* ---------------------------------------------------------------------------------------------
 * Requests
 * ------------------------------------------------------------------------------------------- */

export interface RoutingApplyResult {
  /** Member keys (and 'settings') whose changes were written. */
  applied: string[];
  /** The first write that failed: a member key, or 'config' for the configuration patch. */
  failed: { key: string; error: unknown } | null;
  /** Writes skipped because an earlier one failed. */
  notAttempted: string[];
}

const modelIds = (models: { id?: unknown }[]): string[] =>
  models.flatMap((model) =>
    typeof model.id === 'string' && model.id.trim() ? [model.id.trim()] : []
  );

async function forEachWithConcurrency<T>(
  items: readonly string[],
  task: (item: string) => Promise<T>,
  onResult: (item: string, result: T | null) => void
): Promise<void> {
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const item = items[next];
      next += 1;
      let result: T | null;
      try {
        result = await task(item);
      } catch {
        result = null;
      }
      onResult(item, result);
    }
  };
  await Promise.all(
    Array.from({ length: Math.min(MODEL_REQUEST_CONCURRENCY, items.length) }, worker)
  );
}

export const routingApi = {
  /** The stored v8 configuration document, unnormalized. */
  getRawConfig: () => apiClient.get<unknown>('/config'),

  /** The credentials the board shows next to the stored configuration. */
  async listCredentials(): Promise<AuthFileItem[]> {
    const response = await authFilesApi.list();
    return Array.isArray(response.files) ? response.files : [];
  },

  /** Live registry model ids per credential; a credential that cannot be read reports null. */
  loadCredentialModels: (
    names: readonly string[],
    onResult: (name: string, models: string[] | null) => void
  ) =>
    forEachWithConcurrency(
      names,
      async (name) => modelIds(await authFilesApi.getModelsForAuthFile(name)),
      onResult
    ),

  async applyPlan(board: RoutingBoard, plan: RoutingPlan): Promise<RoutingApplyResult> {
    const assertConnection = guardConfigConnection();
    const writes = credentialWrites(board, plan);
    const configTargets = Object.keys(plan.settings).length ? ['settings'] : [];
    const result: RoutingApplyResult = { applied: [], failed: null, notAttempted: [] };

    for (let index = 0; index < writes.length; index += 1) {
      const write = writes[index];
      assertConnection();
      try {
        await authFilesApi.patchFields(write.name, write.fields);
        result.applied.push(write.key);
      } catch (error) {
        result.failed = { key: write.key, error };
        result.notAttempted = [
          ...writes.slice(index + 1).map((item) => item.key),
          ...configTargets,
        ];
        return result;
      }
    }

    if (configTargets.length === 0) return result;
    try {
      assertConnection();
      const patch = buildRoutingConfigPatch(plan);
      if (patch) await apiClient.patch('/config', patch);
      result.applied.push(...configTargets);
    } catch (error) {
      result.failed = { key: 'config', error };
      result.notAttempted = configTargets;
    }
    return result;
  },
};
