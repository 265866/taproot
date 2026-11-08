import { configApi } from '@/services/api/config';
import { useConfigStore } from '@/stores';

/** Writes request-log to config.yaml, then to the config store so other pages see it. */
export async function saveRecording(enabled: boolean): Promise<void> {
  await configApi.updateRequestLog(enabled);
  useConfigStore.getState().updateConfigValue('request-log', enabled);
}
