import { expect, test } from 'bun:test';
import { createResetGrantCache } from '../src/features/taproot-quota/live/providers/claude/resetGrantCache';
import type { AnthropicResetGrantStatus } from '../src/services/api/claudeResetGrants';

const status = (atLimit: boolean): AnthropicResetGrantStatus => ({
  eligible: true,
  ineligibleReason: null,
  atLimit,
  grants: [],
  nextGrantId: null,
  weeklyResetsAt: null,
  cooldownUntil: null,
});

const setup = () => {
  const state = { revision: 1, now: 1_000, reads: 0, next: status(true) };
  const cache = createResetGrantCache({
    revision: () => state.revision,
    now: () => state.now,
    ttlMs: 60_000,
    read: async () => {
      state.reads += 1;
      return state.next;
    },
  });
  return { state, cache };
};

test('keeps a read for the TTL, then reads again', async () => {
  const { state, cache } = setup();
  expect(cache.peek('a')).toBeUndefined();
  const first = await cache.read('a');
  expect(state.reads).toBe(1);
  state.now += 59_999;
  expect(cache.peek('a')).toBe(first);
  expect(await cache.read('a')).toBe(first);
  expect(state.reads).toBe(1);
  state.now += 1;
  expect(cache.peek('a')).toBeUndefined();
  await cache.read('a');
  expect(state.reads).toBe(2);
});

test('invalidate and a new connection drop cached reads', async () => {
  const { state, cache } = setup();
  await cache.read('a');
  await cache.read('b');
  cache.invalidate('a');
  expect(cache.peek('a')).toBeUndefined();
  expect(cache.peek('b')).toBeDefined();
  state.revision = 2;
  expect(cache.peek('b')).toBeUndefined();
});

test('a read that finishes after the connection changed is not kept', async () => {
  const state = { revision: 1 };
  let resolve!: (value: AnthropicResetGrantStatus) => void;
  const cache = createResetGrantCache({
    revision: () => state.revision,
    now: () => 0,
    ttlMs: 60_000,
    read: () => new Promise((yes) => (resolve = yes)),
  });
  const pending = cache.read('a');
  state.revision = 2;
  resolve(status(false));
  await pending;
  expect(cache.peek('a')).toBeUndefined();
});
