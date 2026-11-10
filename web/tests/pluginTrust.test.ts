import { describe, expect, test } from 'bun:test';
import {
  isDefaultPluginStoreSource,
  isOfficialPlugin,
} from '../src/features/plugins/pluginResources';
import type { PluginStoreEntry } from '../src/types';

const pluginEntry = (sourceId: string, repository: string) =>
  ({ sourceId, repository }) as PluginStoreEntry;

describe('plugin store trust', () => {
  test('trusts only the official source with an official repository', () => {
    expect(isOfficialPlugin(pluginEntry('official', 'router-for-me/example-plugin'))).toBe(true);
    expect(isOfficialPlugin(pluginEntry('third-party', 'router-for-me/example-plugin'))).toBe(
      false
    );
    expect(isOfficialPlugin(pluginEntry('official', 'someone-else/example-plugin'))).toBe(false);
  });
});

describe('default plugin store source', () => {
  test('is decided by the backend-reserved source ID, never by the host name', () => {
    expect(isDefaultPluginStoreSource({ sourceId: 'official' })).toBe(true);
    // A third-party registry at https://official/registry.json is named "official".
    expect(isDefaultPluginStoreSource({ sourceId: 'source-0123456789ab' })).toBe(false);
  });
});
