import { useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { IconClock } from '@/components/ui/icons';
import { agoParts, intervalParts, type AgoParts } from '@/utils/time/ago';
import { SECOND_CLOCK } from '@/utils/time/sharedClock';
import styles from './UpdatedAgo.module.scss';

interface UpdatedAgoProps {
  /** When the data on screen loaded, in epoch milliseconds. Nothing renders without it. */
  updatedAt: number | null | undefined;
  /** How often the page reloads it; adds "refreshes every 30 s". */
  intervalMs?: number | null;
  /** "8 s ago" without "updated" or the interval, for the phone bar. */
  compact?: boolean;
  /** A fixed instant instead of the one-second clock, for tests. */
  now?: number;
  className?: string;
}

const noopSubscribe = () => () => {};
/** Two digits of minutes: the widest text under an hour, with tabular figures. */
const WIDEST: AgoParts = { unit: 'minutes', count: 88 };

/** "updated 8 s ago · refreshes every 30 s", ticking once a second. */
export function UpdatedAgo({ updatedAt, intervalMs, compact, now, className }: UpdatedAgoProps) {
  const { t } = useTranslation();
  const ticking = updatedAt != null && now === undefined;
  const clockNow = useSyncExternalStore(
    ticking ? SECOND_CLOCK.subscribe : noopSubscribe,
    SECOND_CLOCK.getSnapshot,
    SECOND_CLOCK.getSnapshot
  );
  if (updatedAt == null) return null;

  const prefix = compact ? 'ago' : 'updated';
  const text = (ago: AgoParts) => {
    const parts = [t(`console.${prefix}_${ago.unit}`, { count: ago.count })];
    if (!compact && intervalMs) {
      const every = intervalParts(intervalMs);
      parts.push(t(`console.refreshes_every_${every.unit}`, { count: every.count }));
    }
    return parts.join(' · ');
  };
  const at = new Date(updatedAt);

  return (
    <span
      className={[styles.stamp, className].filter(Boolean).join(' ')}
      title={t('console.updated_at', { time: at.toLocaleString() })}
    >
      {/* The text changes every second; the widest it usually gets reserves the width, so the
          stamp does not shift what sits beside it. */}
      <span className={styles.sizer} aria-hidden="true">
        {text(WIDEST)}
      </span>
      <span className={styles.shown}>
        <IconClock size={12} className={styles.icon} />
        <time dateTime={at.toISOString()}>{text(agoParts(updatedAt, now ?? clockNow))}</time>
      </span>
    </span>
  );
}
