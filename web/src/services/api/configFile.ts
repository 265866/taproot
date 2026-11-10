/**
 * 配置文件相关 API（/config.yaml）
 */

import { apiClient } from './client';

/** config.yaml as read, with the ETag a later write can send back as If-Match. */
export interface ConfigYamlSnapshot {
  yaml: string;
  etag: string | null;
}

/** The server answered a conditional write: config.yaml changed since that read. */
export const isConfigChangedError = (error: unknown): boolean =>
  typeof error === 'object' && error !== null && 'status' in error && error.status === 412;

export const ifMatchHeaders = (etag: string | null | undefined): Record<string, string> =>
  etag ? { 'If-Match': etag } : {};

export const configFileApi = {
  async fetchConfigYaml(): Promise<string> {
    return (await configFileApi.fetchConfigSnapshot()).yaml;
  },

  async fetchConfigSnapshot(): Promise<ConfigYamlSnapshot> {
    const response = await apiClient.getRaw('/config.yaml', {
      responseType: 'text',
      headers: { Accept: 'application/yaml, text/yaml, text/plain' },
    });
    const data: unknown = response.data;
    const etag: unknown = response.headers?.etag;
    return {
      yaml:
        typeof data === 'string' ? data : data === undefined || data === null ? '' : String(data),
      etag: typeof etag === 'string' && etag ? etag : null,
    };
  },

  /** With an ETag, the write fails with 412 instead of replacing a newer config.yaml. */
  async saveConfigYaml(content: string, etag?: string | null): Promise<void> {
    await apiClient.put('/config.yaml', content, {
      headers: {
        'Content-Type': 'application/yaml',
        Accept: 'application/json, text/plain, */*',
        ...ifMatchHeaders(etag),
      },
    });
  },
};
