import { afterEach, describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import {
  authFilesApi,
  normalizeOauthModelAlias,
  serializeOauthModelAliases,
} from '@/services/api/authFiles';
import {
  ModelAliasConflictError,
  linkModelAlias,
  removeModelAlias,
  renameModelAlias,
  setModelAliasFork,
  unlinkModelAlias,
} from '@/features/authFiles/modelAliasMutations';
import { getMissingMappingField } from '@/features/authFiles/oauthEditorState';

const spies: Array<{ mockRestore(): void }> = [];
afterEach(() => spies.splice(0).forEach((spy) => spy.mockRestore()));

/** A config map the mocked GET and PUT share, so each write sees the previous one. */
function serveAliasMap(initial: Record<string, unknown>) {
  let stored = structuredClone(initial);
  const get = spyOn(apiClient, 'get').mockImplementation((async () =>
    structuredClone(stored)) as never);
  const put = spyOn(apiClient, 'put').mockImplementation((async (_path: string, body: unknown) => {
    // A slow write leaves room for a second edit to start from the old map.
    await new Promise((resolve) => setTimeout(resolve, 5));
    stored = structuredClone(body) as Record<string, unknown>;
    return {};
  }) as never);
  spies.push(get, put);
  return { read: () => stored, put };
}

describe('OAuth model alias fields the dashboard does not edit', () => {
  test('a round trip keeps display-name, openapi, and unknown fields', () => {
    const raw = {
      name: 'claude-opus-4-6',
      alias: 'best',
      fork: true,
      'display-name': 'Best Model',
      openapi: false,
      future: { nested: 1 },
    };
    const [entry] = normalizeOauthModelAlias({ claude: [raw] }).claude;
    expect(serializeOauthModelAliases([{ ...entry, alias: 'best2' }])).toEqual([
      { ...raw, alias: 'best2' },
    ]);
  });

  test('saving one alias of a provider keeps the other aliases display names', async () => {
    const server = serveAliasMap({
      claude: [
        { name: 'claude-opus-4-6', alias: 'best', 'display-name': 'Best Model' },
        { name: 'claude-sonnet-4-5', alias: 'fast', openapi: true },
      ],
    });
    const current = await authFilesApi.getOauthModelAlias();
    await authFilesApi.saveOauthModelAlias('claude', [
      { ...current.claude[0], alias: 'best2' },
      current.claude[1],
    ]);
    expect(server.read()).toEqual({
      claude: [
        { name: 'claude-opus-4-6', alias: 'best2', 'display-name': 'Best Model' },
        { name: 'claude-sonnet-4-5', alias: 'fast', openapi: true },
      ],
    });
  });
});

describe('OAuth model alias edits apply to the server copy', () => {
  test('two quick links on one provider both persist', async () => {
    const server = serveAliasMap({ codex: [{ name: 'gpt-a', alias: 'one' }] });
    await Promise.all([
      authFilesApi.updateOauthModelAlias('codex', (current) =>
        linkModelAlias(current, 'gpt-b', 'two')
      ),
      authFilesApi.updateOauthModelAlias('codex', (current) =>
        linkModelAlias(current, 'gpt-c', 'three')
      ),
    ]);
    expect(server.read()).toEqual({
      codex: [
        { name: 'gpt-a', alias: 'one' },
        { name: 'gpt-b', alias: 'two', fork: true },
        { name: 'gpt-c', alias: 'three', fork: true },
      ],
    });
  });

  test('a no-op skips the write and an empty list removes the provider', async () => {
    const server = serveAliasMap({ codex: [{ name: 'gpt-a', alias: 'one' }], claude: [] });
    expect(
      await authFilesApi.updateOauthModelAlias('codex', (current) =>
        linkModelAlias(current, 'GPT-A', 'ONE')
      )
    ).toBe(false);
    expect(server.put).not.toHaveBeenCalled();
    expect(
      await authFilesApi.updateOauthModelAlias('codex', (current) =>
        removeModelAlias(current, 'one')
      )
    ).toBe(true);
    expect(server.read()).toEqual({ claude: [] });
  });

  test('a conflict rejects without writing', async () => {
    const server = serveAliasMap({ codex: [{ name: 'gpt-a', alias: 'shared' }] });
    await expect(
      authFilesApi.updateOauthModelAlias('codex', (current) =>
        linkModelAlias(current, 'gpt-b', 'Shared')
      )
    ).rejects.toBeInstanceOf(ModelAliasConflictError);
    expect(server.put).not.toHaveBeenCalled();
  });
});

describe('model alias mutations', () => {
  const current = [
    { name: 'gpt-a', alias: 'one', fork: true, extra: { 'display-name': 'One' } },
    { name: 'gpt-b', alias: 'two' },
  ];

  test('linking an alias another model in the provider uses is a conflict', () => {
    expect(() => linkModelAlias(current, 'gpt-c', 'ONE')).toThrow(ModelAliasConflictError);
    try {
      linkModelAlias(current, 'gpt-c', 'one');
    } catch (err) {
      expect((err as ModelAliasConflictError).reason).toBe('alias_taken');
    }
  });

  test('an alias equal to its source is a conflict, an existing link is a no-op', () => {
    expect(() => linkModelAlias(current, 'gpt-c', ' gpt-c ')).toThrow(ModelAliasConflictError);
    expect(linkModelAlias(current, 'GPT-A', 'one')).toBeNull();
    expect(linkModelAlias(current, 'gpt-c', 'three')).toEqual([
      ...current,
      { name: 'gpt-c', alias: 'three', fork: true },
    ]);
  });

  test('rename, unlink, and fork keep the other fields of each entry', () => {
    expect(renameModelAlias(current, 'one', 'uno')?.[0]).toEqual({
      ...current[0],
      alias: 'uno',
    });
    expect(() => renameModelAlias(current, 'one', 'two')).toThrow(ModelAliasConflictError);
    expect(renameModelAlias(current, 'one', 'One')?.[0].alias).toBe('One');
    expect(renameModelAlias(current, 'missing', 'x')).toBeNull();
    expect(unlinkModelAlias(current, 'gpt-a', 'one')).toEqual([current[1]]);
    expect(unlinkModelAlias(current, 'gpt-a', 'two')).toBeNull();
    expect(setModelAliasFork(current, 'gpt-a', 'one', false)?.[0]).toEqual({
      ...current[0],
      fork: undefined,
    });
    expect(setModelAliasFork(current, 'gpt-a', 'one', true)).toBeNull();
  });
});

describe('half-filled alias rows', () => {
  test('a row with only one field reports the missing one', () => {
    expect(getMissingMappingField({ name: 'gpt-a', alias: ' ' })).toBe('alias');
    expect(getMissingMappingField({ name: '', alias: 'one' })).toBe('name');
    expect(getMissingMappingField({ name: '', alias: '' })).toBeUndefined();
    expect(getMissingMappingField({ name: 'gpt-a', alias: 'one' })).toBeUndefined();
  });
});
