import type { UsageTotals } from '@/services/api/taproot';

/** Fields that add across buckets and groups. Percentiles do not, so merged totals drop them. */
export type AdditiveTotals = Omit<
  UsageTotals,
  'latencyP50Ms' | 'latencyP95Ms' | 'ttftP50Ms' | 'ttftP95Ms'
>;

export const EMPTY_TOTALS: AdditiveTotals = {
  requests: 0,
  failures: 0,
  inputTokens: 0,
  outputTokens: 0,
  reasoningTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  cacheWrite5mTokens: 0,
  cacheWrite1hTokens: 0,
  costUSD: 0,
};

export function addTotals(into: AdditiveTotals, from: AdditiveTotals): AdditiveTotals {
  return {
    requests: into.requests + from.requests,
    failures: into.failures + from.failures,
    inputTokens: into.inputTokens + from.inputTokens,
    outputTokens: into.outputTokens + from.outputTokens,
    reasoningTokens: into.reasoningTokens + from.reasoningTokens,
    cacheReadTokens: into.cacheReadTokens + from.cacheReadTokens,
    cacheWriteTokens: into.cacheWriteTokens + from.cacheWriteTokens,
    cacheWrite5mTokens: into.cacheWrite5mTokens + from.cacheWrite5mTokens,
    cacheWrite1hTokens: into.cacheWrite1hTokens + from.cacheWrite1hTokens,
    costUSD: into.costUSD + from.costUSD,
  };
}

/** Prompt tokens: uncached input plus cache reads and writes (the server's input excludes both). */
export function promptTokens(t: AdditiveTotals): number {
  return t.inputTokens + t.cacheReadTokens + t.cacheWriteTokens;
}

/** Share of prompt tokens served from cache: read / (read + write + input). Null with no prompt. */
export function cacheHitRatio(t: AdditiveTotals): number | null {
  const prompt = promptTokens(t);
  return prompt > 0 ? t.cacheReadTokens / prompt : null;
}

/** Share of prompt tokens written to cache. Null with no prompt. */
export function cacheWriteShare(t: AdditiveTotals): number | null {
  const prompt = promptTokens(t);
  return prompt > 0 ? t.cacheWriteTokens / prompt : null;
}

export function failureRate(t: Pick<AdditiveTotals, 'requests' | 'failures'>): number | null {
  return t.requests > 0 ? t.failures / t.requests : null;
}

/**
 * Cache writes the upstream reported without a TTL split. The server prices them at the 5m rate,
 * Anthropic's default TTL.
 */
export function cacheWriteUnsplit(t: AdditiveTotals): number {
  return Math.max(0, t.cacheWriteTokens - t.cacheWrite5mTokens - t.cacheWrite1hTokens);
}
