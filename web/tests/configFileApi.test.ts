import { afterEach, describe, expect, mock, spyOn, test } from 'bun:test';
import type { AxiosResponse } from 'axios';
import { apiClient } from '../src/services/api/client';
import { configFileApi, isConfigChangedError } from '../src/services/api/configFile';

afterEach(() => {
  mock.restore();
});

describe('config.yaml conditional writes', () => {
  test('a read returns the ETag with the YAML', async () => {
    spyOn(apiClient, 'getRaw').mockResolvedValue({
      data: 'debug: false\n',
      headers: { etag: '"abc"' },
    } as unknown as AxiosResponse);
    expect(await configFileApi.fetchConfigSnapshot()).toEqual({
      yaml: 'debug: false\n',
      etag: '"abc"',
    });
  });

  test('a read without an ETag reports none', async () => {
    spyOn(apiClient, 'getRaw').mockResolvedValue({
      data: 'debug: false\n',
      headers: {},
    } as unknown as AxiosResponse);
    expect((await configFileApi.fetchConfigSnapshot()).etag).toBeNull();
  });

  test('a save sends If-Match only when given an ETag', async () => {
    const put = spyOn(apiClient, 'put').mockResolvedValue(undefined);
    await configFileApi.saveConfigYaml('a: 1\n', '"abc"');
    expect(put.mock.calls[0][2]?.headers).toMatchObject({ 'If-Match': '"abc"' });
    await configFileApi.saveConfigYaml('a: 1\n');
    expect(put.mock.calls[1][2]?.headers).not.toHaveProperty('If-Match');
  });

  test('recognizes a failed precondition', () => {
    expect(isConfigChangedError(Object.assign(new Error('x'), { status: 412 }))).toBe(true);
    expect(isConfigChangedError(Object.assign(new Error('x'), { status: 409 }))).toBe(false);
    expect(isConfigChangedError(null)).toBe(false);
  });
});
