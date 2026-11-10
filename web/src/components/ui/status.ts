export type Severity = 'blocking' | 'waiting' | 'overdue' | 'at_risk' | 'ok';
export type StatusTone = 'ok' | 'warning' | 'danger' | 'info';

/** The status color each severity wears. Waiting is neutral: it needs a decision, not a fix. */
export const SEVERITY_TONE: Record<Severity, StatusTone> = {
  blocking: 'danger',
  overdue: 'warning',
  at_risk: 'warning',
  waiting: 'info',
  ok: 'ok',
};
