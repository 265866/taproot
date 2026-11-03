import { describe, expect, test } from 'bun:test';
import { en } from '@/i18n/resources';
import { resolvePlanTier } from '@/utils/quota';

describe('Codex Pro display names', () => {
  test('labels Pro plans as Pro 100, Pro 200, and Pro 500', () => {
    const messages = en.codex_quota as Record<string, string>;
    expect(messages.plan_prolite).toBe('Pro 100');
    expect(messages.plan_pro).toBe('Pro 200');
    expect(messages.plan_promax).toBe('Pro 500');
  });
});

describe('resolvePlanTier', () => {
  test('only Pro 500 is elite, in every spelling', () => {
    expect(resolvePlanTier('promax')).toBe('elite');
    expect(resolvePlanTier('  ProMax  ')).toBe('elite');
    expect(resolvePlanTier('pro-max')).toBe('elite');
    expect(resolvePlanTier('pro_max')).toBe('elite');
  });

  test('Pro 200 and Pro 100 share the premium tier', () => {
    expect(resolvePlanTier('pro')).toBe('premium');
    expect(resolvePlanTier('  PRO  ')).toBe('premium');
    expect(resolvePlanTier('prolite')).toBe('premium');
    expect(resolvePlanTier('Pro-Lite')).toBe('premium');
    expect(resolvePlanTier('pro_lite')).toBe('premium');
  });

  test('Business plans, including Business Premium, rank with Plus', () => {
    expect(resolvePlanTier('self_serve_business_prolite')).toBe('plain');
    expect(resolvePlanTier('  SELF_SERVE_BUSINESS_PROLITE  ')).toBe('plain');
    expect(resolvePlanTier('self_serve_business_usage_based')).toBe('plain');
  });

  test('maps ordinary and unknown plans to plain', () => {
    expect(resolvePlanTier('plus')).toBe('plain');
    expect(resolvePlanTier('team')).toBe('plain');
    expect(resolvePlanTier('free')).toBe('plain');
    expect(resolvePlanTier('enterprise')).toBe('plain');
  });

  test('maps missing values to plain', () => {
    expect(resolvePlanTier(null)).toBe('plain');
    expect(resolvePlanTier(undefined)).toBe('plain');
    expect(resolvePlanTier('')).toBe('plain');
    expect(resolvePlanTier('   ')).toBe('plain');
  });
});
