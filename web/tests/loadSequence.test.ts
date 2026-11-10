import { expect, test } from 'bun:test';
import { createLoadSequence } from '@/utils/loadSequence';

test('only the newest load lands, and a write makes loads in flight stale', () => {
  const loads = createLoadSequence();
  const first = loads.start();
  const second = loads.start();
  expect(first()).toBe(false);
  expect(second()).toBe(true);
  loads.invalidate();
  expect(second()).toBe(false);
  expect(loads.start()()).toBe(true);
});
