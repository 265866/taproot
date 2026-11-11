import { afterAll, afterEach, beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { apiCallApi } from '@/services/api/apiCall';
import {
  AnthropicResetGrantUnknownOutcome,
  claimClaudeResetGrant,
  parseAnthropicResetGrantStatus,
  readClaudeResetGrants,
  type AnthropicResetGrantStatus,
} from '@/services/api/claudeResetGrants';
import { parseClaudeUsageLimits, type ClaudeUsageLimits } from '@/services/api/claudeUsageLimits';
import {
  activeCooldown,
  classifyGrant,
  grantReason,
  limitReached,
  predictReset,
  resetGroups,
  usageFromRecorded,
} from '@/features/taproot-quota/model/claudeResets';
import { createResetGrantOperations } from '@/features/taproot-quota/live/providers/claude/resetGrantOperations';
import {
  ClaudeLimits,
  ClaudeLimitNotice,
} from '@/features/taproot-quota/components/claude/ClaudeLimits';
import { ClaudeResets } from '@/features/taproot-quota/components/claude/ClaudeResets';
import { ResetConfirmBody } from '@/features/taproot-quota/components/claude/ResetConfirm';
import { resetConfirmTitle } from '@/features/taproot-quota/components/claude/resetText';
import { buildClaudeQuotaWindows } from '@/features/taproot-quota/live/providers/claude/data';
import { formatCountdown } from '@/features/taproot-quota/model/format';

type Json = Record<string, unknown>;
const fixture = (name: string): Json =>
  JSON.parse(readFileSync(`tests/fixtures/claude-limits/${name}.json`, 'utf8'));

/** Tue Nov 10 2026, 3:41 PM in Los Angeles, as in the mockups. */
const NOW = Date.parse('2026-11-10T23:41:00Z');
const organization = '11111111-2222-3333-4444-555555555555';

const cedar = (name = 'usage-cedar-acct-B') => fixture(name).cedar_ember as Json;
const status = (block: Json): AnthropicResetGrantStatus => {
  const parsed = parseAnthropicResetGrantStatus(block);
  if (!parsed) throw new Error('fixture did not parse');
  return parsed;
};
const grants = (block: Json) => block.grants as Json[];

/** Account B from the fixtures, with Fable's share of weekly set to `share`. */
function usageB(share: number, weekly = 63, fiveHour = 7): ClaudeUsageLimits {
  return {
    fiveHour: { usedPercent: fiveHour, resetsAt: Date.parse('2026-11-11T00:50:00Z') },
    weekly: { usedPercent: weekly, resetsAt: Date.parse('2026-11-12T12:00:00Z') },
    fable: {
      usedPercent: (share / 50) * 100,
      resetsAt: Date.parse('2026-11-12T12:00:00Z'),
      usedOfWeekly: share,
      allowanceOfWeekly: 50,
    },
    extraUsageEnabled: false,
  };
}

const originalRequest = apiCallApi.request;
// The mockups show Los Angeles times.
const previousZone = process.env.TZ;
process.env.TZ = 'America/Los_Angeles';
beforeAll(async () => {
  await i18n.changeLanguage('en');
});
afterAll(() => {
  if (previousZone === undefined) delete process.env.TZ;
  else process.env.TZ = previousZone;
});
afterEach(() => {
  apiCallApi.request = originalRequest;
});

describe('Claude usage limits', () => {
  test('reads limits[] and Fable’s share of weekly by limit_index', () => {
    expect(parseClaudeUsageLimits(fixture('usage-acct-B'))).toEqual({
      fiveHour: { usedPercent: 7, resetsAt: Date.parse('2026-11-11T00:50:00.270080+00:00') },
      weekly: { usedPercent: 63, resetsAt: Date.parse('2026-11-12T12:00:00.270100+00:00') },
      fable: {
        usedPercent: 0,
        resetsAt: Date.parse('2026-11-12T12:00:00+00:00'),
        usedOfWeekly: 0,
        allowanceOfWeekly: 50,
      },
      extraUsageEnabled: false,
    });
  });

  test('a null five_hour is a closed session, and the grant read says whether credits are on', () => {
    const limits = parseClaudeUsageLimits(fixture('usage-cedar-acct-A'));
    expect(limits?.fiveHour).toBe('closed');
    expect(limits?.fable?.usedOfWeekly).toBe(0);
    // The grant read skips spend; its event props say extra usage is disabled.
    expect(limits?.extraUsageEnabled).toBe(false);
    const enabled = fixture('usage-cedar-acct-A');
    (enabled.cedar_ember as Json).event_props = { extra_usage_state: 'enabled' };
    expect(parseClaudeUsageLimits(enabled)?.extraUsageEnabled).toBe(true);
  });

  test('matches Fable 5.1, and falls back to the legacy Fable key', () => {
    const body = fixture('usage-acct-B');
    ((body.limits as Json[])[2].scope as { model: Json }).model.display_name = 'Fable 5.1';
    expect(parseClaudeUsageLimits(body)?.fable?.allowanceOfWeekly).toBe(50);
    const legacy = parseClaudeUsageLimits({
      seven_day_overage_included: { utilization: 40, resets_at: '2026-11-12T12:00:00Z' },
    });
    expect(legacy?.fable).toMatchObject({ usedPercent: 40, usedOfWeekly: null });
    const windows = buildClaudeQuotaWindows(body as never, ((key: string) => key) as never);
    expect(windows.map((w) => w.id)).toContain('seven-day-fable');
  });

  test('recorded windows: an empty 5-hour window with no reset is a closed session', () => {
    expect(
      usageFromRecorded([
        { window: 'five_hour', utilization: 0, resetsAt: null },
        { window: 'seven_day', utilization: 0.63, resetsAt: 1 },
        { window: 'seven_day_fable', utilization: 0.64, resetsAt: 1 },
      ])
    ).toEqual({
      fiveHour: 'closed',
      weekly: { usedPercent: 63, resetsAt: 1 },
      fable: { usedPercent: 64, resetsAt: 1, usedOfWeekly: null, allowanceOfWeekly: null },
      extraUsageEnabled: null,
    });
  });
});

describe('reset grant parsing and kinds', () => {
  test('keeps every field Claude Code reads', () => {
    const parsed = status(cedar('usage-cedar-acct-A'));
    expect(parsed.grants.map((g) => [g.id, g.type, g.givesBackWeekly])).toEqual([
      ['full-reset-2611', 'weekly', false],
      ['launch-2610-fable', 'full', true],
      ['launch-2610-fable-6mo', 'full', true],
    ]);
    expect(parsed.grants[0].clears).toEqual(['seven_day', 'seven_day_overage_included']);
    const block = cedar();
    grants(block)[0] = {
      ...grants(block)[0],
      clears: ['five_hour', 'seven_day_opus', 'new_window'],
      blocking: ['five_hour'],
      unavailable_reason: 'blocked_five_hour',
      type: 'monthly',
    };
    block.exhausted = ['seven_day'];
    const widened = status(block);
    expect(widened.grants[0]).toMatchObject({
      clears: ['five_hour', 'seven_day_opus'],
      rawClears: ['five_hour', 'seven_day_opus', 'new_window'],
      clearsUnknown: true,
      type: null,
      rawType: 'monthly',
      blocking: ['five_hour'],
      unavailableReason: 'blocked_five_hour',
    });
    expect(widened.exhausted).toEqual(['seven_day']);
  });

  test('classifies by gives_back_weekly, then type, then clears', () => {
    const [full, fable] = status(cedar()).grants;
    expect(classifyGrant(full)).toBe('full');
    // A Fable grant reports type "full"; gives_back_weekly decides.
    expect(classifyGrant(fable)).toBe('fable');
    // With no session open, upstream calls the full grant weekly, and so does Claude Code.
    expect(classifyGrant(status(cedar('usage-cedar-acct-A')).grants[0])).toBe('weekly');
    const untyped = { ...full, type: null };
    expect(classifyGrant({ ...untyped, clears: ['five_hour'] })).toBe('session');
    expect(classifyGrant({ ...untyped, clears: ['seven_day'] })).toBe('weekly');
    expect(classifyGrant({ ...untyped, clears: ['five_hour', 'seven_day'] })).toBe('full');
  });

  test('a grant is unknown when nothing places it or it clears a window taproot does not know', () => {
    const [full] = status(cedar()).grants;
    expect(classifyGrant({ ...full, type: null, clears: [], rawClears: [] })).toBe('unknown');
    expect(classifyGrant({ ...full, clearsUnknown: true })).toBe('unknown');
    expect(classifyGrant({ ...full, clearsUnknown: true, givesBackWeekly: true })).toBe('unknown');
  });
});

describe('one action per kind', () => {
  test('nothing at a limit: soonest expiry first, none highlighted (state 1)', () => {
    const groups = resetGroups(status(cedar()), usageB(32), NOW);
    expect(groups.map((g) => [g.kind, g.left, g.target?.id, g.primary])).toEqual([
      ['full', 1, 'full-reset-2611', false],
      ['fable', 1, 'launch-2610-fable', false],
    ]);
  });

  test('a kind with two grants spends the one that expires first', () => {
    const block = cedar('usage-cedar-acct-A');
    grants(block)[2] = { ...grants(block)[2], ends_at: '2027-01-01T00:00:00Z' };
    const fable = resetGroups(status(block), usageB(10), NOW).find((g) => g.kind === 'fable');
    expect(fable?.left).toBe(2);
    expect(fable?.target?.id).toBe('launch-2610-fable-6mo');
    // Equal expiries fall back to the id, so the choice is stable.
    const same = resetGroups(status(cedar('usage-cedar-acct-A')), usageB(10), NOW);
    expect(same.find((g) => g.kind === 'fable')?.target?.id).toBe('launch-2610-fable');
  });

  test('a Fable reset with no Fable use this week is off, with the reason (state 2)', () => {
    const block = cedar();
    block.grants = [grants(block)[1]];
    const [fable] = resetGroups(status(block), usageB(0), NOW);
    expect(fable).toMatchObject({ kind: 'fable', target: null, reason: 'no_fable_use' });
    // Without a limits reading, the grant's own Fable percent decides.
    expect(resetGroups(status(block), null, NOW)[0].reason).toBe('no_fable_use');
    const html = renderResets(block, usageB(0));
    expect(html).toContain('No Fable usage this week to give back.');
    expect(html).toMatch(
      /<button[^>]*disabled=""[^>]*aria-describedby="[^"]+"[^>]*>.*Use Fable reset/
    );
  });

  test('at the weekly limit the full reset comes first and is primary (state 3)', () => {
    const usage = usageB(41, 100, 12);
    expect(limitReached(usage, null)).toBe('seven_day');
    const groups = resetGroups({ ...status(cedar()), atLimit: true }, usage, NOW);
    expect(groups.map((g) => [g.kind, g.primary, g.keepForWeekly])).toEqual([
      ['full', true, false],
      ['fable', false, false],
    ]);
  });

  test('at the Fable cap the Fable reset is primary and the full one is kept (state 4)', () => {
    const usage = usageB(50, 71, 22);
    expect(limitReached(usage, null)).toBe('seven_day_overage_included');
    const groups = resetGroups(status(cedar()), usage, NOW);
    expect(groups.map((g) => [g.kind, g.primary, g.keepForWeekly])).toEqual([
      ['fable', true, false],
      ['full', false, true],
    ]);
    const html = renderResets(cedar(), usage);
    expect(html).toContain('Clears this limit');
    expect(html).toContain('Keep it for when the weekly limit runs out.');
    expect(html).toContain(
      'Gives back Fable’s 50% of weekly and refills 5-hour. Weekly goes from 71% to 21% used.'
    );
  });

  test('an unknown kind is its own row, never first and never primary (R11)', () => {
    const block = cedar();
    grants(block).push({
      ...grants(block)[0],
      id: 'mystery-2611',
      label: 'Holiday reset',
      ends_at: '2026-11-20T00:00:00Z',
      clears: ['five_hour', 'seven_day', 'mystery_window'],
      type: 'full',
    });
    const usage = usageB(41, 100, 12);
    const groups = resetGroups({ ...status(block), atLimit: true }, usage, NOW);
    expect(groups.map((g) => [g.key, g.primary])).toEqual([
      ['full', true],
      ['fable', false],
      ['unknown:mystery-2611', false],
    ]);
    // Nothing at a limit: it expires first and still comes last.
    expect(resetGroups(status(block), usageB(32), NOW).map((g) => g.kind)).toEqual([
      'full',
      'fable',
      'unknown',
    ]);
    const html = renderResets(block, usage);
    expect(html).toContain('Holiday reset');
    expect(html).toContain('Clears 5-hour and weekly, plus limits taproot doesn&#x27;t recognize.');
    expect(html).toContain('Use this reset');
    expect(html).toContain('Other ×1');
  });

  test('upstream reasons, in Claude Code’s order', () => {
    const parsed = status(cedar());
    const [full] = parsed.grants;
    expect(grantReason(parsed, full, NOW)).toBeNull();
    expect(
      grantReason(parsed, { ...full, usableNow: false, unavailableReason: 'cooldown' }, NOW)
    ).toBe('cooldown');
    expect(grantReason(parsed, { ...full, usableNow: false, blocking: ['five_hour'] }, NOW)).toBe(
      'blocked_five_hour'
    );
    expect(
      grantReason({ ...parsed, atLimit: false }, { ...full, useRequiresLimit: true }, NOW)
    ).toBe('needs_limit');
    expect(
      grantReason(
        { ...parsed, atLimit: true, exhausted: ['five_hour'] },
        { ...full, usableNow: false, clears: ['seven_day'] },
        NOW
      )
    ).toBe('cannot_clear');
  });

  test('a cooldown turns every button off and says when resets come back (R14)', () => {
    const block = { ...cedar(), cooldown_until: '2026-11-10T23:52:00Z' };
    expect(activeCooldown(status(block), NOW)).toBe(Date.parse('2026-11-10T23:52:00Z'));
    expect(activeCooldown(status(cedar()), NOW)).toBeNull();
    const html = renderResets(block, usageB(32));
    expect(html).toContain('Resets available again at 3:52 PM.');
    expect(html.match(/<button[^>]*disabled=""/g)?.length).toBe(2);
  });
});

function renderResets(block: Json, usage: ClaudeUsageLimits) {
  const parsed = status(block);
  return renderToStaticMarkup(
    createElement(ClaudeResets, {
      status: parsed,
      groups: resetGroups(parsed, usage, NOW),
      usage,
      now: NOW,
      blocked: false,
      busyKey: null,
      pendingGrantId: null,
      result: null,
      onUse: () => {},
    })
  );
}

describe('claims', () => {
  test('a refusal reason settles the claim with nothing spent', async () => {
    for (const statusCode of [200, 409]) {
      apiCallApi.request = async () => ({
        statusCode,
        body: { result: 'nope', reason: 'not_next_grant' },
        bodyText: '',
        header: {},
      });
      expect(await claimClaudeResetGrant('a', organization, 'launch-2610-fable', 'r')).toEqual({
        code: 'refused',
        reason: 'not_next_grant',
      });
    }
    for (const reason of ['reset_unconfirmed', 'stamp_indeterminate']) {
      apiCallApi.request = async () => ({
        statusCode: 200,
        body: { result: 'reset', reason },
        bodyText: '',
        header: {},
      });
      await expect(claimClaudeResetGrant('a', organization, 'g', 'r')).rejects.toBeInstanceOf(
        AnthropicResetGrantUnknownOutcome
      );
    }
  });

  test('the journal treats a refusal as settled and lets the next claim start fresh', async () => {
    const sent: string[] = [];
    let n = 0;
    const operations = createResetGrantOperations({
      revision: () => 1,
      now: () => NOW,
      requestId: () => `request-${++n}`,
      readOrganization: async () => organization,
      readStatus: async () => status(cedar()),
      resetCooldown: async (authIndex: string) => ({
        status: 'ok',
        auth_index: authIndex,
        models: [],
      }),
      claim: async (_a: string, _o: string, grantId: string, requestId: string) => {
        sent.push(`${grantId}/${requestId}`);
        return { code: 'refused' as const, reason: 'not_next_grant' };
      },
    });
    expect(await operations.run('k', 'a', 'launch-2610-fable')).toEqual({
      code: 'refused',
      reason: 'not_next_grant',
      unresolved: false,
    });
    expect(operations.inspect('k')?.code).toBe('refused');
    await operations.run('k', 'a', 'full-reset-2611');
    expect(sent).toEqual(['launch-2610-fable/request-1', 'full-reset-2611/request-2']);
  });

  test('the grant read carries the limits and when it arrived', async () => {
    apiCallApi.request = async () => ({
      statusCode: 200,
      body: fixture('usage-cedar-acct-B'),
      bodyText: '',
      header: {},
    });
    const read = await readClaudeResetGrants('a');
    expect(read.usage?.weekly?.usedPercent).toBe(63);
    expect(typeof read.readAt).toBe('number');
  });
});

describe('limits display', () => {
  test('state 1 rows: weekly hatches Fable’s share and Fable shows its own headroom', () => {
    const html = renderToStaticMarkup(
      createElement(ClaudeLimits, { usage: usageB(32), wall: null, surface: 'card', now: NOW })
    );
    expect(html).toContain('93% <small>left</small>');
    expect(html).toContain('37% <small>left</small>');
    expect(html).toContain('Fable used 32% of weekly');
    expect(html).toContain('up to 50% of weekly');
    expect(html).toContain('36% <small>left</small>');
    expect(html).toContain('Can use 18% more of weekly · resets with weekly');
    expect(html).toContain('Resets 4:50 PM · in 1 h 9 m');
    expect(html).toContain('Resets Thu 4:00 AM · in 1 d 12 h');
  });

  test('a closed session reads as such, and Fable at its cap', () => {
    const usage = { ...usageB(50, 71), fiveHour: 'closed' as const };
    const html = renderToStaticMarkup(
      createElement(ClaudeLimits, {
        usage,
        wall: 'seven_day_overage_included',
        surface: 'sheet',
        now: NOW,
      })
    );
    expect(html).toContain('No session open · starts with the next request');
    expect(html).toContain('At its cap · resets with weekly');
  });

  test('notices: weekly reached, and Fable capped with or without usage credits (R15)', () => {
    const weekly = renderToStaticMarkup(
      createElement(ClaudeLimitNotice, { usage: usageB(41, 100), wall: 'seven_day', now: NOW })
    );
    expect(weekly).toContain(
      '<b>Weekly limit reached.</b> Every model is blocked until Thu 4:00 AM.'
    );
    const capped = renderToStaticMarkup(
      createElement(ClaudeLimitNotice, {
        usage: usageB(50, 71),
        wall: 'seven_day_overage_included',
        now: NOW,
      })
    );
    expect(capped).toContain(
      '<b>Fable is capped until Thu 4:00 AM.</b> Other models keep working.'
    );
    const credits = renderToStaticMarkup(
      createElement(ClaudeLimitNotice, {
        usage: { ...usageB(50, 71), extraUsageEnabled: true },
        wall: 'seven_day_overage_included',
        now: NOW,
      })
    );
    expect(credits).toContain('Fable now uses your usage credits.');
  });

  test('countdowns', () => {
    expect(formatCountdown(NOW + 69 * 60_000, NOW)).toBe('in 1 h 9 m');
    expect(formatCountdown(NOW + 36 * 3600_000, NOW)).toBe('in 1 d 12 h');
    expect(formatCountdown(NOW - 1, NOW)).toBe('now');
  });
});

describe('confirmation dialogs', () => {
  const t = i18n.t.bind(i18n);
  const render = (kind: 'full' | 'fable', usage: ClaudeUsageLimits) => {
    const parsed = status(cedar());
    const group = resetGroups(parsed, usage, NOW).find((g) => g.kind === kind)!;
    return {
      title: resetConfirmTitle(t, group, group.target!, 'alice@example.com'),
      html: renderToStaticMarkup(
        createElement(ResetConfirmBody, {
          group,
          grant: group.target!,
          usage,
          weeklyResetsAt: '2026-11-12T12:00:00Z',
          readAt: Date.parse('2026-11-10T23:39:00Z'),
          now: NOW,
        })
      ).replace(/<[^>]+>/g, '|'),
    };
  };

  test('full reset', () => {
    const { title, html } = render('full', usageB(32));
    expect(title).toBe('Use a full reset on alice@example.com?');
    for (const text of [
      'Refills the 5-hour, weekly, and Fable limits. Every model is back to full.',
      '|5-hour||7% used||→||0% used|',
      '|Weekly · all models||63% used||→||0% used|',
      '|Fable||32% of weekly||→||0% of weekly|',
      'The weekly limit still resets on its own Thu, Nov 12, 4:00 AM (in 1 d 12 h). This doesn’t move that day.',
      'Spends “One full limit reset (November 2026)”, which expires Dec 10. No full resets left after this.',
      'Usage as of 3:39 PM.',
    ]) {
      expect(html).toContain(text);
    }
  });

  test('Fable reset', () => {
    const { title, html } = render('fable', usageB(32));
    expect(title).toBe('Use a Fable reset on alice@example.com?');
    for (const text of [
      'Gives back what Fable used this week (32% of weekly) and refills your 5-hour limit. Other models’ weekly usage stays.',
      '|5-hour||7% used||→||0% used|',
      '|Weekly · all models||63% used||→||31% used|',
      '|Fable||32% of weekly||→||0% of weekly|',
      'The weekly limit still resets Thu, Nov 12, 4:00 AM. This doesn’t move that day.',
      'Spends “New model launch: one Fable reset for Max, Team Premium and Enterprise Premium”, which expires Mar 8. No Fable resets left after this.',
    ]) {
      expect(html).toContain(text);
    }
  });

  test('an unknown reset names its windows and shows no table (R11)', () => {
    const block = cedar();
    block.grants = [
      {
        ...grants(block)[0],
        id: 'mystery',
        label: 'Holiday reset',
        clears: ['seven_day', 'mystery_window'],
      },
    ];
    const parsed = status(block);
    const [group] = resetGroups(parsed, usageB(32), NOW);
    expect(resetConfirmTitle(t, group, group.target!, 'alice@example.com')).toBe(
      'Use “Holiday reset” on alice@example.com?'
    );
    expect(predictReset(group.kind, group.target!, usageB(32))).toEqual([]);
    const html = renderToStaticMarkup(
      createElement(ResetConfirmBody, {
        group,
        grant: group.target!,
        usage: usageB(32),
        weeklyResetsAt: null,
        now: NOW,
      })
    );
    expect(html).toContain('Anthropic says it clears weekly and mystery_window.');
    expect(html).toContain(
      'Taproot doesn&#x27;t recognize this kind of reset, so it can&#x27;t show what will change.'
    );
    expect(html).not.toContain('role="table"');
  });
});
