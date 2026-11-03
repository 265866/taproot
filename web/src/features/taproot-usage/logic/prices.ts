import type { Price } from '@/services/api/taproot';

export const PRICE_FIELDS = [
  'input',
  'output',
  'cacheRead',
  'cacheWrite5m',
  'cacheWrite1h',
] as const;
export type PriceField = (typeof PRICE_FIELDS)[number];

/** One editable row. Numbers stay strings while typing so `1.` and `` are representable. */
export interface PriceDraft {
  /** Client-only row identity; patterns can be renamed. */
  rowId: string;
  modelPattern: string;
  values: Record<PriceField, string>;
  /** Source and time of the saved row this draft started from; empty for new rows. */
  source: string;
  updatedAt: string;
}

export type PriceFieldError = 'required' | 'duplicate' | 'number';

export interface DraftErrors {
  modelPattern?: PriceFieldError;
  values: Partial<Record<PriceField, PriceFieldError>>;
}

export function toDrafts(prices: readonly Price[]): PriceDraft[] {
  return prices.map((p, index) => ({
    rowId: `saved-${index}-${p.modelPattern}`,
    modelPattern: p.modelPattern,
    values: {
      input: String(p.input),
      output: String(p.output),
      cacheRead: String(p.cacheRead),
      cacheWrite5m: String(p.cacheWrite5m),
      cacheWrite1h: String(p.cacheWrite1h),
    },
    source: p.source,
    updatedAt: p.updatedAt,
  }));
}

/** A finite, non-negative decimal, as the server requires; null otherwise. */
export function parsePriceValue(raw: string): number | null {
  const trimmed = raw.trim();
  if (!/^(\d+(\.\d*)?|\.\d+)(e[+-]?\d+)?$/i.test(trimmed)) return null;
  const value = Number(trimmed);
  return Number.isFinite(value) && value >= 0 ? value : null;
}

/** Mirrors validatePrices in internal/taproot/ingest/pricing.go, per field. */
export function validateDrafts(drafts: readonly PriceDraft[]): Map<string, DraftErrors> {
  const errors = new Map<string, DraftErrors>();
  const counts = new Map<string, number>();
  for (const d of drafts) {
    const pattern = d.modelPattern.trim();
    if (pattern) counts.set(pattern, (counts.get(pattern) ?? 0) + 1);
  }
  for (const d of drafts) {
    const pattern = d.modelPattern.trim();
    const rowErrors: DraftErrors = { values: {} };
    if (!pattern) rowErrors.modelPattern = 'required';
    else if ((counts.get(pattern) ?? 0) > 1) rowErrors.modelPattern = 'duplicate';
    for (const field of PRICE_FIELDS) {
      const raw = d.values[field];
      if (!raw.trim()) rowErrors.values[field] = 'required';
      else if (parsePriceValue(raw) === null) rowErrors.values[field] = 'number';
    }
    if (rowErrors.modelPattern || Object.keys(rowErrors.values).length > 0) {
      errors.set(d.rowId, rowErrors);
    }
  }
  return errors;
}

/** The PUT body. Only call after validateDrafts returned no errors. */
export function draftsToPrices(drafts: readonly PriceDraft[]): Price[] {
  return drafts.map((d) => ({
    modelPattern: d.modelPattern.trim(),
    input: parsePriceValue(d.values.input) ?? 0,
    output: parsePriceValue(d.values.output) ?? 0,
    cacheRead: parsePriceValue(d.values.cacheRead) ?? 0,
    cacheWrite5m: parsePriceValue(d.values.cacheWrite5m) ?? 0,
    cacheWrite1h: parsePriceValue(d.values.cacheWrite1h) ?? 0,
    source: d.source,
    updatedAt: d.updatedAt,
  }));
}

function priceKey(p: Price): string {
  return [p.modelPattern, ...PRICE_FIELDS.map((f) => p[f])].join('|');
}

/** Whether saving the drafts would change the table (row order does not matter to the server). */
export function draftsChanged(drafts: readonly PriceDraft[], saved: readonly Price[]): boolean {
  if (drafts.length !== saved.length) return true;
  if (validateDrafts(drafts).size > 0) return true;
  const a = draftsToPrices(drafts).map(priceKey).sort();
  const b = saved.map(priceKey).sort();
  return a.some((key, i) => key !== b[i]);
}

function globMatch(pattern: string, value: string): boolean {
  const parts = pattern.split('*');
  if (parts.length === 1) return pattern === value;
  if (!value.startsWith(parts[0])) return false;
  let rest = value.slice(parts[0].length);
  for (const part of parts.slice(1, -1)) {
    const index = rest.indexOf(part);
    if (index < 0) return false;
    rest = rest.slice(index + part.length);
  }
  return rest.endsWith(parts[parts.length - 1]);
}

/**
 * The price row the server applies to a model: an exact pattern wins, then the glob with the
 * most literal text, ties broken by pattern order. Mirrors priceBook.lookup in pricing.go.
 */
export function matchPrice<T extends Pick<Price, 'modelPattern'>>(
  model: string,
  prices: readonly T[]
): T | null {
  const exact = prices.find((p) => !p.modelPattern.includes('*') && p.modelPattern === model);
  if (exact) return exact;
  const globs = prices
    .filter((p) => p.modelPattern.includes('*'))
    .sort((a, b) => {
      const la = a.modelPattern.split('*').join('').length;
      const lb = b.modelPattern.split('*').join('').length;
      if (la !== lb) return lb - la;
      return a.modelPattern < b.modelPattern ? -1 : a.modelPattern > b.modelPattern ? 1 : 0;
    });
  return globs.find((p) => globMatch(p.modelPattern, model)) ?? null;
}
