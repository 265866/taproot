import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useEmailMask } from '@/hooks/useEmailMask';
import type { AttentionItem } from '@/services/api/taprootStatus';
import { getErrorMessage } from '@/utils/helpers';
import type { FixRun } from '../model/attentionActions';
import { AccountGoneError } from '../model/inlineActions';

export interface FixRuns {
  runs: Record<string, FixRun>;
  run: (item: AttentionItem, task: () => Promise<string>) => Promise<void>;
}

/**
 * The inline fixes and their results, by item. The page holds them rather than the rail, which
 * remounts when it moves beside the page, and rather than each item: a fix that works removes its
 * item on the next read, and the result must outlive it. `onChanged` runs after a fix works, so
 * the item, its badge, the pools, and the verdict update together.
 */
export function useFixRuns(onChanged: () => Promise<void>): FixRuns {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const [runs, setRuns] = useState<Record<string, FixRun>>({});
  const run = async (item: AttentionItem, task: () => Promise<string>) => {
    const title = mask(item.title);
    setRuns((prev) => ({ ...prev, [item.id]: { kind: 'running', title } }));
    try {
      const message = await task();
      setRuns((prev) => ({ ...prev, [item.id]: { kind: 'done', message, title, at: Date.now() } }));
      await onChanged();
    } catch (failure: unknown) {
      const message =
        failure instanceof AccountGoneError
          ? t('dashboard.needs.enable_gone')
          : getErrorMessage(failure, t('dashboard.needs.action_failed'));
      setRuns((prev) => ({ ...prev, [item.id]: { kind: 'failed', message, title } }));
    }
  };
  return { runs, run };
}
