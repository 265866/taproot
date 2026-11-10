import type { AttentionSnapshot } from './useAttentionStore';

/**
 * Sample attention for development and tests: the approved mockup's three items. `bun run dev`
 * loads it with `?demo-attention` in the address; production builds leave it out.
 */
export const DEMO_ATTENTION: AttentionSnapshot = {
  count: 3,
  badges: {
    accounts: { severity: 'danger', count: 1, label: '1 account needs you' },
    backups: { severity: 'warning', label: 'Backup overdue' },
    access: { severity: 'info', count: 1, label: '1 access request' },
  },
};
