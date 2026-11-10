import { pluginsApi, pluginStoreApi } from '@/services/api';
import type {
  PluginListEntry,
  PluginListResponse,
  PluginStoreEntry,
  PluginStoreResponse,
} from '@/types';

const PLUGIN_STATE_TIMEOUT_MS = 15_000;
const PLUGIN_STATE_INTERVAL_MS = 500;

const wait = (ms: number) =>
  new Promise<void>((resolve) => {
    window.setTimeout(resolve, ms);
  });

/** Polling stops once its page is gone, so no state or toast lands after navigation. */
export const isPollAbort = (error: unknown): boolean =>
  error instanceof DOMException && error.name === 'AbortError';

export interface PluginStateWaitResult {
  response: PluginListResponse;
  plugin: PluginListEntry | null;
  timedOut: boolean;
}

export interface PluginStoreStateWaitResult {
  response: PluginStoreResponse;
  plugin: PluginStoreEntry | null;
  timedOut: boolean;
}

export async function waitForPluginState(
  id: string,
  predicate: (plugin: PluginListEntry, response: PluginListResponse) => boolean,
  timeoutMs = PLUGIN_STATE_TIMEOUT_MS,
  intervalMs = PLUGIN_STATE_INTERVAL_MS,
  signal?: AbortSignal
): Promise<PluginStateWaitResult> {
  const deadline = Date.now() + timeoutMs;
  signal?.throwIfAborted();
  let latest = await pluginsApi.list();

  for (;;) {
    signal?.throwIfAborted();
    const plugin = latest.plugins.find((item) => item.id === id) ?? null;
    if (plugin && predicate(plugin, latest)) {
      return { response: latest, plugin, timedOut: false };
    }
    if (Date.now() >= deadline) {
      return { response: latest, plugin, timedOut: true };
    }
    await wait(Math.min(intervalMs, Math.max(0, deadline - Date.now())));
    signal?.throwIfAborted();
    latest = await pluginsApi.list();
  }
}

export async function waitForPluginStoreState(
  id: string,
  sourceId: string,
  predicate: (plugin: PluginStoreEntry, response: PluginStoreResponse) => boolean,
  timeoutMs = PLUGIN_STATE_TIMEOUT_MS,
  intervalMs = PLUGIN_STATE_INTERVAL_MS,
  signal?: AbortSignal
): Promise<PluginStoreStateWaitResult> {
  const deadline = Date.now() + timeoutMs;
  signal?.throwIfAborted();
  let latest = await pluginStoreApi.list();

  for (;;) {
    signal?.throwIfAborted();
    const plugin =
      latest.plugins.find((item) => item.id === id && (!sourceId || item.sourceId === sourceId)) ??
      null;
    if (plugin && predicate(plugin, latest)) {
      return { response: latest, plugin, timedOut: false };
    }
    if (Date.now() >= deadline) {
      return { response: latest, plugin, timedOut: true };
    }
    await wait(Math.min(intervalMs, Math.max(0, deadline - Date.now())));
    signal?.throwIfAborted();
    latest = await pluginStoreApi.list();
  }
}
