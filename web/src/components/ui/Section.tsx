import { Children, useId, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Skeleton } from '@/components/ui/Skeleton';
import { IconCircleAlert } from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import styles from './Section.module.scss';

interface SectionProps {
  title: ReactNode;
  /** Muted context after the title, such as "5 in 24h". */
  caption?: ReactNode;
  /** The right side of the header, usually a link such as "Open in Request Logs". */
  action?: ReactNode;
  /** The first load is running: without content yet, the body shows a skeleton. */
  loading?: boolean;
  /**
   * The last load failed. Without content, the error takes the body's place; with content, it sits
   * above the last good data, which is marked stale. Server text: it is masked like other errors.
   */
  error?: string | null;
  onRetry?: () => void;
  /** Marks the content as out of date even without an error. */
  stale?: boolean;
  /** A placeholder shaped like the content; defaults to a few skeleton rows. */
  skeleton?: ReactNode;
  /** Draws the body in a hairline card (the default) or leaves it bare. */
  card?: boolean;
  headingLevel?: 2 | 3;
  className?: string;
  children?: ReactNode;
}

/**
 * A titled block of a console page with its loading, error, and stale states built in, so every
 * section keeps the last good data and offers Retry the same way.
 */
export function Section({
  title,
  caption,
  action,
  loading,
  error,
  onRetry,
  stale,
  skeleton,
  card = true,
  headingLevel = 2,
  className,
  children,
}: SectionProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const titleId = useId();
  const Heading = headingLevel === 2 ? 'h2' : 'h3';
  // Children.toArray drops null, booleans, and empty arrays, so `rows.map(...)` over no rows is
  // no content and the skeleton or the bare error still shows.
  const hasContent = Children.toArray(children).length > 0;
  const showStale = hasContent && (stale || Boolean(error));
  const showSkeleton = Boolean(loading) && !hasContent && !error;

  const errorRow = error ? (
    <div className={styles.error} role="alert">
      <IconCircleAlert size={14} className={styles.errorIcon} />
      <span className={styles.errorText}>
        {error.trim()
          ? t('console.section_error', { message: mask(error) })
          : t('console.section_error_bare')}
      </span>
      {onRetry ? (
        <Button variant="secondary" size="sm" onClick={onRetry} className={styles.retry}>
          {t('console.retry')}
        </Button>
      ) : null}
    </div>
  ) : null;

  const skeletonBody = (
    <div className={styles.skeleton} aria-hidden="true">
      {skeleton ?? (
        <>
          <Skeleton height={12} width="92%" />
          <Skeleton height={12} width="78%" />
          <Skeleton height={12} width="85%" />
        </>
      )}
    </div>
  );
  const body = hasContent ? children : (errorRow ?? (showSkeleton ? skeletonBody : null));

  return (
    <section
      className={[styles.section, className].filter(Boolean).join(' ')}
      aria-labelledby={titleId}
      aria-busy={showSkeleton || undefined}
    >
      <div className={styles.head}>
        <Heading id={titleId} className={styles.title}>
          {title}
        </Heading>
        {caption ? <span className={styles.caption}>{caption}</span> : null}
        {showStale ? (
          <span className={styles.stale} title={t('console.stale_hint')}>
            {t('console.stale')}
          </span>
        ) : null}
        {action ? <div className={styles.action}>{action}</div> : null}
      </div>
      {hasContent ? errorRow : null}
      {showSkeleton ? <span className="visually-hidden">{t('console.loading')}</span> : null}
      {body !== null ? <div className={card ? styles.card : styles.bare}>{body}</div> : null}
    </section>
  );
}
