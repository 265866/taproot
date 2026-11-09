/**
 * Connection state for the same-origin management API.
 *
 * Taproot admits callers by Tailscale identity and injects the management
 * credential server-side, so the dashboard never asks for or stores a key.
 */

import { create } from 'zustand';
import type { ConnectionFailure, ConnectionStatus, DeniedDevice } from '@/types';
import { apiClient } from '@/services/api/client';
import { detectApiBaseFromLocation } from '@/utils/connection';
import { isRecord } from '@/utils/helpers';
import { useConfigStore } from './useConfigStore';
import { useModelsStore } from './useModelsStore';
import { useQuotaStore } from './useQuotaStore';

interface AuthStoreState {
  apiBase: string;
  connectionStatus: ConnectionStatus;
  connectionFailure: ConnectionFailure | null;
  /** The refused device, when the failure is `not_admin`. */
  connectionFailureDevice: DeniedDevice | null;
  serverVersion: string | null;
  serverBuildDate: string | null;
  supportsPlugin: boolean;

  connect: () => Promise<boolean>;
  updateServerVersion: (version: string | null, buildDate?: string | null) => void;
  updateServerPluginSupport: (supportsPlugin: boolean) => void;
}

export const classifyConnectionError = (error: unknown): ConnectionFailure => {
  const { status, apiCode } = (error as { status?: unknown; apiCode?: unknown } | null) ?? {};
  // The access guard's 403 for a tailnet device without Admin.
  if (status === 403 && apiCode === 'not_admin') return 'not_admin';
  return status === 401 || status === 403 ? 'forbidden' : 'unreachable';
};

/** The device the guard names in a `not_admin` denial. */
export const deniedDevice = (error: unknown): DeniedDevice | null => {
  const details = (error as { details?: unknown } | null)?.details;
  const body = isRecord(details) && isRecord(details.error) ? details.error : null;
  const text = (value: unknown) => (typeof value === 'string' ? value.trim() : '');
  // A sharee's device has no name, but always an ID.
  if (!body || (!text(body.node) && !text(body.nodeId))) return null;
  return {
    node: text(body.node),
    nodeId: text(body.nodeId),
    login: text(body.login),
    proxy: body.proxy === true,
    claimable: body.claimable === true,
    requested: body.requested === true,
  };
};

const initialApiBase = () => (typeof window === 'undefined' ? '' : detectApiBaseFromLocation());

let connectPromise: Promise<boolean> | null = null;

export const useAuthStore = create<AuthStoreState>()((set, get) => ({
  apiBase: initialApiBase(),
  connectionStatus: 'disconnected',
  connectionFailure: null,
  connectionFailureDevice: null,
  serverVersion: null,
  serverBuildDate: null,
  supportsPlugin: false,

  connect: () => {
    if (connectPromise) return connectPromise;

    connectPromise = (async () => {
      const apiBase = get().apiBase || initialApiBase();
      set({
        apiBase,
        connectionStatus: 'connecting',
        connectionFailure: null,
        connectionFailureDevice: null,
      });
      useConfigStore.getState().clearCache();
      useModelsStore.getState().clearCache();
      useQuotaStore.getState().clearQuotaCache();
      apiClient.setConfig({ apiBase });

      try {
        await useConfigStore.getState().fetchConfig(true);
        set({ connectionStatus: 'connected' });
        return true;
      } catch (error) {
        set({
          connectionStatus: 'error',
          connectionFailure: classifyConnectionError(error),
          connectionFailureDevice: deniedDevice(error),
        });
        return false;
      } finally {
        connectPromise = null;
      }
    })();

    return connectPromise;
  },

  updateServerVersion: (version, buildDate) => {
    set({
      serverVersion: version || null,
      serverBuildDate: buildDate || null,
    });
  },

  updateServerPluginSupport: (supportsPlugin) => {
    set({ supportsPlugin });
  },
}));

if (typeof window !== 'undefined') {
  // A 401 after connecting means the server stopped admitting this caller.
  window.addEventListener('unauthorized', () => {
    useAuthStore.setState({
      connectionStatus: 'error',
      connectionFailure: 'forbidden',
      connectionFailureDevice: null,
    });
  });

  window.addEventListener('server-version-update', ((e: CustomEvent) => {
    const detail = e.detail || {};
    useAuthStore.getState().updateServerVersion(detail.version || null, detail.buildDate || null);
  }) as EventListener);

  window.addEventListener('server-plugin-support-update', ((e: CustomEvent) => {
    useAuthStore.getState().updateServerPluginSupport(e.detail?.supportsPlugin === true);
  }) as EventListener);
}
