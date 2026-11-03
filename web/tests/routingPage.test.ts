import { afterAll, afterEach, describe, expect, mock, spyOn, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '../src/i18n/index';
import { authFilesApi } from '../src/services/api/authFiles';
import { apiClient } from '../src/services/api/client';
import { buildRoutingBoard, routingApi } from '../src/services/api/routing';
import {
  buildRoutingPlan,
  draftFromBoard,
  moveMember,
  resolveRoute,
} from '../src/features/routing/logic';
import { TierLadder } from '../src/features/routing/components/TierLadder';
import { PolicyPanel } from '../src/features/routing/components/PolicyPanel';
import type { AuthFileItem } from '../src/types/authFile';

test('language changes do not trigger board reloads or reset the draft', () => {
  const source = readFileSync('src/features/routing/RoutingPage.tsx', 'utf8');
  const load = source.slice(
    source.indexOf('  const load = useCallback'),
    source.indexOf('  useEffect(() => {')
  );
  expect(load).toContain('[connected, fetchConfig, loadModels]');
  expect(load).not.toContain("t('common.unknown_error')");
  expect(source).toContain("message: error || t('common.unknown_error')");
});

const RAW = { routing: { strategy: 'round-robin' } };
const FILES: AuthFileItem[] = [
  { name: 'top.json', type: 'claude', email: 'top@example.com', priority: 20 },
  { name: 'main.json', type: 'codex', email: 'main@example.com', priority: 0 },
  { name: 'runtime', type: 'gemini', runtimeOnly: true },
  { name: 'off.json', type: 'claude', email: 'off@example.com', priority: 0, disabled: true },
];

/** One card's markup: from its key to the next card. */
const cardMarkup = (html: string, key: string): string => {
  const rest = html.slice(html.indexOf(`data-member-key="${key}"`));
  const next = rest.indexOf('data-member-key=', 1);
  return next < 0 ? rest : rest.slice(0, next);
};

const restores: Array<{ mockRestore: () => void }> = [];
afterEach(() => {
  restores.splice(0).forEach((spy) => spy.mockRestore());
});

// i18n is a process-wide singleton shared with other test files.
const initialLanguage = i18n.language;
afterAll(async () => {
  await i18n.changeLanguage(initialLanguage);
});

describe('routing board rendering', () => {
  test('renders tiers on a rail with drop gaps, states and accessible controls', async () => {
    await i18n.changeLanguage('en');
    const board = buildRoutingBoard(RAW, FILES);
    const draft = draftFromBoard(board);
    const html = renderToStaticMarkup(
      createElement(TierLadder, {
        members: new Map(board.members.map((member) => [member.key, member])),
        draft,
        preview: resolveRoute(board, draft, { credentials: {} }, null, 0),
        priorities: { 'cred:top.json': 20, 'cred:main.json': 0, 'cred:runtime': 0 },
        model: null,
        nowMs: 0,
        theme: 'light',
        entryControl: null,
        onMove: () => {},
        onStep: () => {},
        onEditWeights: () => {},
      })
    );
    expect(html).toContain('Request in');
    expect(html).toContain('Preferred');
    expect(html).toContain('Fallback 1');
    expect(html).toContain('Serving requests');
    expect(html).toContain('Falls to the next tier when the whole tier is unavailable');
    expect(html).toContain('main@example.com');
    expect(html).toContain('aria-haspopup="menu"');
    // Runtime-only credentials show a lock instead of a drag handle and are not draggable.
    expect(html).toMatch(/draggable="false"[^>]*data-member-key="cred:runtime"/);
  });

  test('disabled credentials are marked and dimmed; enabled ones are not', async () => {
    await i18n.changeLanguage('en');
    const board = buildRoutingBoard(RAW, FILES);
    const draft = draftFromBoard(board);
    const html = renderToStaticMarkup(
      createElement(TierLadder, {
        members: new Map(board.members.map((member) => [member.key, member])),
        draft,
        preview: resolveRoute(board, draft, { credentials: {} }, null, 0),
        priorities: {},
        model: null,
        nowMs: 0,
        theme: 'light',
        entryControl: null,
        onMove: () => {},
        onStep: () => {},
        onEditWeights: () => {},
      })
    );
    // CSS Modules are not compiled under bun:test; the route state drives the dimmed style.
    const cardState = (key: string) =>
      html.match(new RegExp(`data-member-key="${key}" data-state="([^"]*)"`))?.[1] ?? '';
    expect(cardMarkup(html, 'cred:off.json')).toContain('Disabled');
    expect(cardState('cred:off.json')).toBe('disabled');
    expect(cardMarkup(html, 'cred:main.json')).not.toContain('Disabled');
    expect(cardState('cred:main.json')).toBe('serving');
    const card = readFileSync('src/features/routing/components/MemberCard.tsx', 'utf8');
    expect(card).toContain("const muted = state === 'disabled' || state === 'ineligible';");
  });

  test('the strategy picker is a keyboard radio group', async () => {
    await i18n.changeLanguage('en');
    const html = renderToStaticMarkup(
      createElement(PolicyPanel, {
        settings: {
          strategy: 'weighted-round-robin',
          sessionAffinity: false,
          requestRetry: 3,
          maxRetryCredentials: 0,
        },
        onChange: () => {},
      })
    );
    expect(html).toContain('role="radiogroup"');
    expect(html).toMatch(/role="radio" aria-checked="true" tabindex="0"[^>]*>.*Weighted/);
    expect(html).toContain('Requests are split across the tier by weight.');
  });

  test('styles respect reduced motion and the route is registered behind navigation', () => {
    const page = readFileSync('src/features/routing/RoutingPage.module.scss', 'utf8');
    const card = readFileSync('src/features/routing/components/MemberCard.module.scss', 'utf8');
    const bar = readFileSync('src/features/routing/components/RoutingSaveBar.module.scss', 'utf8');
    expect(page).toContain('@media (prefers-reduced-motion: reduce)');
    expect(card).toContain('@media (prefers-reduced-motion: reduce)');
    expect(bar).toContain('@media (prefers-reduced-motion: reduce)');
    expect(readFileSync('src/router/MainRoutes.tsx', 'utf8')).toContain(
      '{ path: ROUTES.routing, element: <RoutingPage /> }'
    );
    expect(readFileSync('src/components/layout/MainLayout.tsx', 'utf8')).toContain(
      "link('routing', 'nav.routing')"
    );
  });
});

describe('routing save bar', () => {
  test('floats over the policy column, outside the transition layer', () => {
    const bar = readFileSync('src/features/routing/components/RoutingSaveBar.tsx', 'utf8');
    const policy = readFileSync('src/features/routing/components/PolicyPanel.tsx', 'utf8');
    const page = readFileSync('src/features/routing/RoutingPage.tsx', 'utf8');
    // Portalled: sticky/fixed inside the page-transition layer does not track the viewport.
    expect(bar).toContain('createPortal(');
    // The bar measures the column the policy panel marks.
    expect(bar).toContain("POLICY_COLUMN_ATTR = 'data-routing-policy-column'");
    expect(policy).toContain('data-routing-policy-column=""');
    // A page sliding out must not leave its bar behind.
    expect(page).toContain('visible={isCurrentLayer && dirty}');
  });
});

describe('applying a routing plan', () => {
  const planFor = () => {
    const board = buildRoutingBoard(RAW, FILES);
    const draft = draftFromBoard(board);
    draft.tiers = moveMember(draft.tiers, 'cred:main.json', { kind: 'gap', index: 0 });
    draft.settings = { ...draft.settings, sessionAffinity: true };
    return { board, plan: buildRoutingPlan(board, draft) };
  };

  test('writes credentials first, then the settings once', async () => {
    const calls: string[] = [];
    const patchFields = mock(async (name: string) => {
      calls.push(`fields:${name}`);
      return {};
    });
    restores.push(spyOn(authFilesApi, 'patchFields').mockImplementation(patchFields));
    restores.push(
      spyOn(apiClient, 'get').mockImplementation((async (url: string) => {
        calls.push(`get:${url}`);
        return RAW;
      }) as typeof apiClient.get)
    );
    const patch = mock(async (url: string, body: unknown) => {
      calls.push(`patch:${url}`);
      return body;
    });
    restores.push(spyOn(apiClient, 'patch').mockImplementation(patch as typeof apiClient.patch));

    const { board, plan } = planFor();
    const result = await routingApi.applyPlan(board, plan);

    expect(calls).toEqual(['fields:main.json', 'patch:/config']);
    expect(result.failed).toBeNull();
    expect(result.applied).toEqual(['cred:main.json', 'settings']);
    expect(patch.mock.calls[0][1]).toEqual({ routing: { 'session-affinity': true } });
    expect(plan.priorities['cred:main.json']).toBeGreaterThan(20);
  });

  test('stops at the first failed credential and reports what was not attempted', async () => {
    restores.push(
      spyOn(authFilesApi, 'patchFields').mockImplementation(async () => {
        throw new Error('boom');
      })
    );
    const patch = spyOn(apiClient, 'patch');
    restores.push(patch);

    const { board, plan } = planFor();
    const result = await routingApi.applyPlan(board, plan);

    expect(result.failed?.key).toBe('cred:main.json');
    expect(result.applied).toEqual([]);
    expect(result.notAttempted).toEqual(['settings']);
    expect(patch).not.toHaveBeenCalled();
  });
});
