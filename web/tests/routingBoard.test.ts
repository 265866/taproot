import { describe, expect, test } from 'bun:test';
import {
  assignPriorities,
  RoutingPriorityError,
  buildRoutingPlan,
  canStepMember,
  collectModelOptions,
  draftFromBoard,
  moveMember,
  neededCredentialModels,
  resolveRoute,
  stepMember,
  tiersFromMembers,
} from '../src/features/routing/logic';
import {
  buildRoutingBoard,
  buildRoutingConfigPatch,
  credentialWrites,
  readRoutingSettings,
} from '../src/services/api/routing';
import type { AuthFileItem } from '../src/types/authFile';
import type { RoutingBoard, RoutingModelCatalog } from '../src/types/routing';

const RAW = {
  routing: {
    strategy: 'weighted-round-robin',
    'session-affinity': true,
    retry: { 'request-retry': 3, 'max-retry-credentials': 0 },
  },
  // Config API-key providers are not part of the dashboard; the board must ignore them.
  'api-keys': {
    codex: [{ name: 'opencode', priority: 50, keys: [{ 'api-key': 'sk-codex-aaaa' }] }],
  },
};

const FILES: AuthFileItem[] = [
  { name: 'top.json', type: 'codex', email: 'top@example.com', priority: 20 },
  { name: 'mid.json', type: 'claude', priority: 10, weight: 3 },
  { name: 'mid2.json', type: 'codex', priority: 10 },
  { name: 'main.json', type: 'codex', email: 'main@example.com', priority: 0 },
  { name: 'runtime', type: 'gemini', runtimeOnly: true, priority: 0 },
  {
    name: 'cool.json',
    type: 'codex',
    priority: 0,
    cooldownSnapshot: {
      receivedAtMs: 1_000,
      records: [
        {
          scope: 'model',
          modelKey: 'gpt-5',
          reason: 'quota',
          retryAt: '',
          remainingSeconds: 60,
        },
      ],
    },
  },
  { name: 'off.json', type: 'claude', priority: 0, disabled: true },
];

const board = (): RoutingBoard => buildRoutingBoard(RAW, FILES);

const catalog: RoutingModelCatalog = {
  credentials: {
    'top.json': ['gpt-5-mini'],
    'mid.json': ['claude-sonnet'],
    'mid2.json': ['gpt-5'],
    'main.json': ['gpt-5'],
    'cool.json': ['gpt-5'],
    'off.json': ['claude-opus'],
    runtime: ['gemini-3-pro'],
  },
};

describe('routing board reading', () => {
  test('reads settings with CPA defaults for absent values', () => {
    expect(readRoutingSettings(RAW)).toEqual({
      strategy: 'weighted-round-robin',
      sessionAffinity: true,
      requestRetry: 3,
      maxRetryCredentials: 0,
    });
    expect(readRoutingSettings({ routing: { strategy: 'bogus' } }).strategy).toBe('round-robin');
  });

  test('reads every credential as a member and ignores config API keys', () => {
    const members = board().members;
    expect(members.map((member) => member.key)).toEqual(FILES.map((file) => `cred:${file.name}`));
    const byKey = new Map(members.map((member) => [member.key, member]));
    expect(byKey.get('cred:main.json')?.name).toBe('main@example.com');
    expect(byKey.get('cred:main.json')?.credentialName).toBe('main.json');
    expect(byKey.get('cred:mid.json')?.weights[0].weight).toBe(3);
    expect(byKey.get('cred:mid2.json')?.weights[0].weight).toBeNull();
    expect(byKey.get('cred:runtime')?.locked).toBe(true);
    expect(byKey.get('cred:off.json')?.disabled).toBe(true);
    expect(JSON.stringify(members)).not.toContain('sk-codex-aaaa');
  });

  test('disabled credentials need no live model list', () => {
    expect(neededCredentialModels(board())).not.toContain('off.json');
    expect(neededCredentialModels(board())).toContain('main.json');
  });
});

describe('arranging tiers', () => {
  test('groups members by priority, highest first', () => {
    const tiers = tiersFromMembers(board().members);
    expect(tiers.map((tier) => tier.id)).toEqual(['p20', 'p10', 'p0']);
  });

  test('moves into a tier, into a gap, and refuses no-op moves', () => {
    const tiers = tiersFromMembers(board().members);
    const joined = moveMember(tiers, 'cred:main.json', { kind: 'tier', tierId: 'p20' });
    expect(joined[0].memberKeys).toContain('cred:main.json');
    const top = moveMember(tiers, 'cred:main.json', { kind: 'gap', index: 0 });
    expect(top[0].memberKeys).toEqual(['cred:main.json']);
    expect(top).toHaveLength(4);
    // The lone member of the top tier cannot move above itself.
    expect(moveMember(tiers, 'cred:top.json', { kind: 'gap', index: 0 })).toEqual(tiers);
    expect(canStepMember(tiers, 'cred:top.json', 'up')).toBe(false);
    expect(stepMember(tiers, 'cred:top.json', 'down')[0].memberKeys).toContain('cred:top.json');
  });

  test('keeps stored numbers that still fit and numbers the gaps', () => {
    const current = board();
    const tiers = moveMember(tiersFromMembers(current.members), 'cred:main.json', {
      kind: 'gap',
      index: 0,
    });
    const priorities = assignPriorities(tiers, current.members);
    expect(priorities['cred:main.json']).toBe(30);
    expect(priorities['cred:top.json']).toBe(20);
    expect(priorities['cred:mid.json']).toBe(10);
    expect(priorities['cred:runtime']).toBe(0);
  });

  test('squeezes a new tier between neighbours and renumbers when there is no room', () => {
    const current = board();
    const between = moveMember(tiersFromMembers(current.members), 'cred:main.json', {
      kind: 'gap',
      index: 1,
    });
    expect(assignPriorities(between, current.members)['cred:main.json']).toBe(15);

    const tight = buildRoutingBoard({}, [
      { name: 'a.json', type: 'codex', priority: 2 },
      { name: 'b.json', type: 'codex', priority: 1 },
      { name: 'c.json', type: 'codex', priority: 0 },
    ]);
    const squeezed = moveMember(tiersFromMembers(tight.members), 'cred:c.json', {
      kind: 'gap',
      index: 1,
    });
    const values = assignPriorities(squeezed, tight.members);
    const order = squeezed.map((tier) => values[tier.memberKeys[0]]);
    expect(order).toEqual([...order].sort((left, right) => right - left));
    expect(new Set(order).size).toBe(order.length);
  });

  test('rejects inserting between adjacent locked priorities without changing the draft', () => {
    const current = buildRoutingBoard({}, [
      { name: 'a', priority: 1, runtimeOnly: true },
      { name: 'b', priority: 0, runtimeOnly: true },
      { name: 'c', priority: -10 },
    ]);
    const draft = draftFromBoard(current);
    const moved = moveMember(draft.tiers, 'cred:c', { kind: 'gap', index: 1 });
    expect(() => assignPriorities(moved, current.members)).toThrow(RoutingPriorityError);
    expect(() => buildRoutingPlan(current, { ...draft, tiers: moved })).toThrow(
      RoutingPriorityError
    );
    expect(buildRoutingPlan(current, draft).changes).toEqual([]);
  });

  test('renumbers editable tiers while preserving locked anchors', () => {
    const current = buildRoutingBoard({}, [
      { name: 'a', priority: 2 },
      { name: 'b', priority: 1, runtimeOnly: true },
      { name: 'c', priority: -10 },
    ]);
    const draft = draftFromBoard(current);
    draft.tiers = moveMember(draft.tiers, 'cred:c', { kind: 'gap', index: 1 });
    const values = assignPriorities(draft.tiers, current.members);
    expect(values['cred:b']).toBe(1);
    expect(values['cred:a']).toBeGreaterThan(values['cred:c']);
    expect(values['cred:c']).toBeGreaterThan(1);
    expect(buildRoutingPlan(current, draft).priorities['cred:b']).toBeUndefined();
  });

  test('plans only real changes and never moves locked credentials', () => {
    const current = board();
    const draft = draftFromBoard(current);
    expect(buildRoutingPlan(current, draft).changes).toEqual([]);

    draft.tiers = moveMember(draft.tiers, 'cred:main.json', { kind: 'gap', index: 0 });
    draft.weights['cred:mid.json'] = [5];
    draft.settings = { ...draft.settings, strategy: 'fill-first' };
    const plan = buildRoutingPlan(current, draft);
    expect(plan.priorities).toEqual({ 'cred:main.json': 30 });
    expect(plan.weights).toEqual({ 'cred:mid.json': [5] });
    expect(plan.settings).toEqual({ strategy: 'fill-first' });
    expect(plan.priorities['cred:runtime']).toBeUndefined();
  });
});

describe('writing a plan', () => {
  test('writes each changed credential once and settings as one patch', () => {
    const current = board();
    const draft = draftFromBoard(current);
    draft.tiers = moveMember(draft.tiers, 'cred:main.json', { kind: 'tier', tierId: 'p10' });
    draft.weights['cred:mid.json'] = [4];
    draft.settings = { ...draft.settings, sessionAffinity: false };
    const plan = buildRoutingPlan(current, draft);

    expect(credentialWrites(current, plan)).toEqual([
      { key: 'cred:mid.json', name: 'mid.json', fields: { weight: 4 } },
      { key: 'cred:main.json', name: 'main.json', fields: { priority: 10 } },
    ]);
    expect(buildRoutingConfigPatch(plan)).toEqual({ routing: { 'session-affinity': false } });
  });

  test('a settings-only plan sends only the retry block', () => {
    const current = board();
    const draft = draftFromBoard(current);
    draft.settings = { ...draft.settings, requestRetry: 1, maxRetryCredentials: 2 };
    const plan = buildRoutingPlan(current, draft);
    expect(credentialWrites(current, plan)).toEqual([]);
    expect(buildRoutingConfigPatch(plan)).toEqual({
      routing: { retry: { 'request-retry': 1, 'max-retry-credentials': 2 } },
    });
  });

  test('a credential-only plan sends no configuration patch', () => {
    const current = board();
    const draft = draftFromBoard(current);
    draft.tiers = moveMember(draft.tiers, 'cred:main.json', { kind: 'gap', index: 0 });
    expect(buildRoutingConfigPatch(buildRoutingPlan(current, draft))).toBeNull();
  });
});

describe('route preview', () => {
  test('credential models are final callable ids, prefix included', () => {
    const current = board();
    const credential = current.members.find((member) => member.key === 'cred:main.json')!;
    current.members = [{ ...credential, prefix: 'team' }];
    const models = ['gpt-5', 'team/gpt-5'];
    const liveCatalog: RoutingModelCatalog = { credentials: { 'main.json': models } };
    expect(collectModelOptions(current, liveCatalog)).toEqual(models);
    const draft = draftFromBoard(current);
    for (const model of models) {
      expect(resolveRoute(current, draft, liveCatalog, model, 0).tiers[0].members[0].state).toBe(
        'serving'
      );
    }
    const invalid = resolveRoute(current, draft, liveCatalog, 'team/team/gpt-5', 0);
    expect(invalid.tiers[0].members[0].state).toBe('ineligible');
  });

  test('lists callable models of enabled credentials, sorted and deduplicated', () => {
    expect(collectModelOptions(board(), catalog)).toEqual([
      'claude-sonnet',
      'gemini-3-pro',
      'gpt-5',
      'gpt-5-mini',
    ]);
  });

  test('the first tier with a serving member takes the request; lower tiers stand by', () => {
    const current = board();
    const draft = draftFromBoard(current);
    const preview = resolveRoute(current, draft, catalog, 'gpt-5', 2_000);
    // p20 only has gpt-5-mini: skipped.
    expect(preview.tiers[0].state).toBe('skipped');
    expect(preview.tiers[1].state).toBe('active');
    const byKey = new Map(preview.tiers[1].members.map((member) => [member.key, member]));
    expect(byKey.get('cred:mid2.json')?.state).toBe('serving');
    expect(byKey.get('cred:mid2.json')?.share).toBe(100);
    expect(byKey.get('cred:mid.json')?.state).toBe('ineligible');
    expect(preview.tiers[2].state).toBe('standby');
  });

  test('weighted round-robin splits a tier by weight (absent counts as 1)', () => {
    const current = board();
    const draft = draftFromBoard(current);
    const preview = resolveRoute(current, draft, catalog, null, 0);
    // p20 is a single credential; p10 holds mid (3) and mid2 (1) but stands by.
    expect(preview.tiers[0].members.map((member) => member.share)).toEqual([100]);
    draft.tiers = moveMember(draft.tiers, 'cred:top.json', { kind: 'tier', tierId: 'p10' });
    const weighted = resolveRoute(current, draft, catalog, null, 0);
    const shares = Object.fromEntries(
      weighted.tiers[0].members.map((member) => [member.key, member.share])
    );
    expect(shares).toEqual({ 'cred:mid.json': 60, 'cred:mid2.json': 20, 'cred:top.json': 20 });
    draft.settings = { ...draft.settings, strategy: 'round-robin' };
    const even = resolveRoute(current, draft, catalog, null, 0);
    expect(even.tiers[0].members.map((member) => member.share)).toEqual([33.3, 33.3, 33.3]);
  });

  test('model cooldowns and unknown model lists are not counted as serving', () => {
    const current = board();
    const draft = draftFromBoard(current);
    draft.tiers = moveMember(draft.tiers, 'cred:cool.json', { kind: 'gap', index: 0 });
    const preview = resolveRoute(current, draft, catalog, 'gpt-5', 2_000);
    expect(preview.tiers[0].state).toBe('cooling');
    expect(preview.tiers[0].members[0].state).toBe('cooling');
    expect(preview.tiers[1].state).toBe('skipped');
    expect(preview.tiers[2].state).toBe('active');

    const loading = resolveRoute(current, draft, { credentials: {} }, 'gpt-5', 0);
    expect(loading.tiers[0].members[0].state).toBe('unknown');
  });

  test('disabled credentials never serve', () => {
    const current = board();
    const draft = draftFromBoard(current);
    const preview = resolveRoute(current, draft, catalog, 'claude-opus', 0);
    expect(preview.hasActive).toBe(false);
    const off = preview.tiers[2].members.find((member) => member.key === 'cred:off.json');
    expect(off?.state).toBe('disabled');
  });

  test('fill-first has no fixed split', () => {
    const current = board();
    const draft = draftFromBoard(current);
    draft.settings = { ...draft.settings, strategy: 'fill-first' };
    const preview = resolveRoute(current, draft, catalog, null, 0);
    expect(preview.tiers[0].state).toBe('active');
    expect(preview.tiers[0].members[0].share).toBeNull();
  });
});
