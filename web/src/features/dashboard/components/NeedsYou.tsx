import { useId, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { SeverityTag } from '@/components/ui/SeverityTag';
import { Skeleton } from '@/components/ui/Skeleton';
import {
  IconAlertTriangle,
  IconCheckCircle2,
  IconChevronDown,
  IconCircleAlert,
  IconExternalLink,
  IconSmartphone,
  IconXCircle,
} from '@/components/ui/icons';
import { useEmailMask } from '@/hooks/useEmailMask';
import { useReservedHeight } from '../hooks/useReservedHeight';
import type { Attention, AttentionItem } from '@/services/api/taprootStatus';
import { formatFileSize } from '@/utils/format';
import {
  itemAction,
  itemCategoryKey,
  resolvedResults,
  shortLabelKey,
  type FixRun,
  type ItemAction,
} from '../model/attentionActions';
import type { Read } from '../hooks/useDashboardData';
import { formatAge } from '../model/format';
import type { FixRuns } from '../hooks/useFixRuns';
import { backupNow, enableAccount } from '../model/inlineActions';
import styles from './Rail.module.scss';

interface NeedsYouProps {
  attention: Read<Attention>;
  /** No account yet: the calm state says nothing needs the user *yet*. */
  firstRun: boolean;
  loggingToFile: boolean | null;
  nowMs: number;
  onRetry: () => void;
  fixes: FixRuns;
  /**
   * Phones: compact rows, each with its fix's short label beside the title and its detail opening
   * in place; the first opens on its own.
   */
  compact?: boolean;
}

const SEVERITY_ICON: Record<AttentionItem['severity'], ReactNode> = {
  blocking: <IconXCircle size={16} />,
  at_risk: <IconAlertTriangle size={16} />,
  overdue: <IconAlertTriangle size={16} />,
  waiting: <IconSmartphone size={16} />,
};

/** The rail's first section: one card per item from `GET /attention`, each with one fix. */
export function NeedsYou({
  attention,
  firstRun,
  loggingToFile,
  nowMs,
  onRetry,
  fixes: { runs, run },
  compact = false,
}: NeedsYouProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const headingId = useId();
  const { data, error } = attention;
  const items = data?.items ?? [];
  const resolved = resolvedResults(runs, items, nowMs);
  const blocking = items.some((item) => item.severity === 'blocking');
  // Rows the user opened or closed; the rest follow the default: the item first shown at the top
  // opens, and no other row opens on its own when that one leaves.
  const [toggled, setToggled] = useState<Record<string, boolean>>({});
  const [firstId, setFirstId] = useState<string | null>(null);
  if (firstId === null && items.length > 0) setFirstId(items[0].id);
  const [sectionRef, reservedStyle] = useReservedHeight('needs', {
    loading: !data && !error,
    loaded: data !== null,
  });

  const errorRow =
    error && !attention.quiet ? (
      <div className={styles.error} role="alert">
        <IconCircleAlert size={14} />
        <span>{mask(t('console.section_error', { message: error }))}</span>
        <Button variant="secondary" size="sm" onClick={onRetry}>
          {t('console.retry')}
        </Button>
      </div>
    ) : null;

  const resolvedRows = resolved.map(([id, run]) => (
    <ResolvedRow key={`resolved:${id}`} run={run} />
  ));

  let body: ReactNode;
  if (!data) {
    // Shaped like two items, for a browser that has not loaded the rail before.
    body = !error ? (
      <div className={styles.skeleton} aria-hidden="true">
        {[0, 1].map((index) => (
          <div key={index} className={styles.skeletonItem}>
            <Skeleton height={14} width="40%" />
            <Skeleton height={12} width="90%" />
            <Skeleton height={12} width="70%" />
            <Skeleton height={28} width={96} />
          </div>
        ))}
      </div>
    ) : attention.quiet ? (
      <p className={styles.unavailable}>{t('console.not_loaded')}</p>
    ) : null;
  } else if (items.length === 0) {
    body = (
      <>
        {resolvedRows.length > 0 ? <ul className={styles.items}>{resolvedRows}</ul> : null}
        <div className={styles.calm}>
          <span className={firstRun ? styles.calmIconQuiet : styles.calmIcon}>
            <IconCheckCircle2 size={16} />
          </span>
          <div>
            <p className={styles.itemTitle}>
              {t(firstRun ? 'dashboard.needs.clear_first_run' : 'dashboard.needs.clear')}
            </p>
            <p className={styles.itemDetail}>
              {t(
                firstRun ? 'dashboard.needs.clear_first_run_detail' : 'dashboard.needs.clear_detail'
              )}
            </p>
          </div>
        </div>
      </>
    );
  } else {
    body = (
      <ul className={styles.items}>
        {resolvedRows}
        {items.map((item) => {
          const props = {
            item,
            action: itemAction(item, loggingToFile),
            age: item.since ? formatAge(Date.parse(item.since), nowMs, t) : '',
            run: runs[item.id] ?? IDLE,
            onRun: (task: () => Promise<string>) => void run(item, task),
          };
          if (!compact) return <NeedItem key={item.id} {...props} />;
          const open = toggled[item.id] ?? item.id === firstId;
          return (
            <CompactNeedItem
              key={item.id}
              {...props}
              open={open}
              onToggle={() => setToggled((prev) => ({ ...prev, [item.id]: !open }))}
            />
          );
        })}
      </ul>
    );
  }

  return (
    <section
      ref={sectionRef}
      style={reservedStyle}
      className={styles.block}
      data-compact={compact || undefined}
      aria-labelledby={headingId}
      aria-busy={!data && !error}
    >
      <div className={styles.head}>
        <h2 id={headingId} className={styles.title}>
          {t('dashboard.needs.title')}
        </h2>
        {items.length > 0 ? (
          <span
            className={styles.count}
            data-tone={blocking ? 'danger' : 'warning'}
            aria-label={t('dashboard.needs.count', { count: items.length })}
          >
            {items.length}
          </span>
        ) : null}
        {data && error ? (
          <span className={styles.stale} title={t('console.stale_hint')}>
            {t('console.stale')}
          </span>
        ) : null}
      </div>
      {errorRow}
      {body !== null ? <div className={styles.needs}>{body}</div> : null}
    </section>
  );
}

/**
 * A fix that worked, after its item left the list: the result leads, and the problem it settled
 * follows as context, so the row never reads as the failure it fixed.
 */
export function ResolvedRow({ run }: { run: Extract<FixRun, { kind: 'done' }> }) {
  const { t } = useTranslation();
  return (
    <li className={styles.resolved}>
      <span className={styles.calmIcon}>
        <IconCheckCircle2 size={16} />
      </span>
      <div className={styles.resolvedBody} role="status" tabIndex={-1} ref={focusIfFocusWasLost}>
        <p className={styles.itemTitle}>{run.message}</p>
        <p className={styles.itemDetail}>
          {t('dashboard.needs.resolved_was', { title: run.title })}
        </p>
      </div>
    </li>
  );
}

/** A fixed item's button is gone with its item; keep the keyboard where the result is. */
function focusIfFocusWasLost(node: HTMLElement | null) {
  if (node && (document.activeElement === null || document.activeElement === document.body)) {
    node.focus();
  }
}

function NeedItem({
  item,
  action,
  age,
  run,
  onRun,
}: {
  item: AttentionItem;
  action: ItemAction;
  age: string;
  run: RunState;
  onRun: (task: () => Promise<string>) => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  return (
    <li className={styles.item} data-severity={item.severity}>
      <span className={styles.itemIcon} data-severity={item.severity}>
        {SEVERITY_ICON[item.severity]}
      </span>
      <div className={styles.itemBody}>
        <p className={styles.itemMeta}>
          <SeverityTag severity={item.severity} />
          <span>
            {t(itemCategoryKey(item))}
            {age ? ` · ${age}` : ''}
          </span>
        </p>
        <p className={styles.itemTitle}>{mask(item.title)}</p>
        {item.detail ? <p className={styles.itemDetail}>{mask(item.detail)}</p> : null}
        <ItemFix
          action={action}
          primary={item.severity === 'blocking'}
          title={mask(item.title)}
          run={run}
          onRun={onRun}
        />
      </div>
    </li>
  );
}

/**
 * A phone's row: the tag, title, and short fix on one line, and the detail (with a fix's result or
 * note) opening in place under them. Without a detail, the title is not a button.
 */
function CompactNeedItem({
  item,
  action,
  age,
  run,
  onRun,
  open,
  onToggle,
}: {
  item: AttentionItem;
  action: ItemAction;
  age: string;
  run: RunState;
  onRun: (task: () => Promise<string>) => void;
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const detailId = useId();
  const title = mask(item.title);
  const note = action.kind === 'note' ? t(action.textKey) : null;
  const expandable = Boolean(item.detail || note);
  const summary = (
    <>
      <span className={styles.itemMeta}>
        <SeverityTag severity={item.severity} />
        <span>
          {t(itemCategoryKey(item))}
          {age ? ` · ${age}` : ''}
        </span>
      </span>
      <span className={styles.itemTitle}>
        {title}
        {expandable ? <IconChevronDown size={14} className={styles.chevron} /> : null}
      </span>
    </>
  );
  return (
    <li className={styles.compactItem} data-severity={item.severity} data-open={open || undefined}>
      <span className={styles.itemIcon} data-severity={item.severity}>
        {SEVERITY_ICON[item.severity]}
      </span>
      {expandable ? (
        <button
          type="button"
          className={styles.expander}
          aria-expanded={open}
          aria-controls={detailId}
          onClick={onToggle}
        >
          {summary}
        </button>
      ) : (
        <div className={styles.expander}>{summary}</div>
      )}
      <FixControl
        action={action}
        primary={item.severity === 'blocking'}
        title={title}
        run={run}
        onRun={onRun}
        short
      />
      <div id={detailId} className={styles.more} hidden={!open || !expandable}>
        {item.detail ? <p className={styles.itemDetail}>{mask(item.detail)}</p> : null}
        {note ? <p className={styles.itemDetail}>{note}</p> : null}
      </div>
      {run.kind === 'done' || run.kind === 'failed' ? (
        <div className={styles.more}>
          <FixResult run={run} />
        </div>
      ) : null}
    </li>
  );
}

type RunState = { kind: 'idle' } | FixRun;

const IDLE: RunState = { kind: 'idle' };

interface FixProps {
  action: ItemAction;
  primary: boolean;
  /** The item's title, so a screen reader hears which item a repeated button acts on. */
  title: string;
  run: RunState;
  onRun: (task: () => Promise<string>) => void;
  /** Shows the short label; the full one stays in the accessible name. */
  short?: boolean;
}

/** One item's fix: a link to the page that fixes it, or a fix that runs here and says how it went. */
function ItemFix(props: FixProps) {
  const { t } = useTranslation();
  if (props.action.kind === 'note') return <p className={styles.note}>{t(props.action.textKey)}</p>;
  if (props.action.kind === 'none') return null;
  return (
    <div className={styles.fixRow}>
      <FixControl {...props} />
      <FixResult run={props.run} />
    </div>
  );
}

/** How an inline fix went, beside or under its button. */
function FixResult({ run }: { run: RunState }) {
  const { mask } = useEmailMask();
  if (run.kind === 'done') {
    return (
      <span className={styles.fixDone} role="status">
        {run.message}
      </span>
    );
  }
  if (run.kind === 'failed') {
    return (
      <span className={styles.fixFailed} role="alert">
        {mask(run.message)}
      </span>
    );
  }
  return null;
}

function FixControl({ action, primary, title, run, onRun, short = false }: FixProps) {
  const { t } = useTranslation();
  const buttonClass = `btn ${primary ? 'btn-primary' : 'btn-secondary'} btn-sm ${styles.fix}`;
  const running = run.kind === 'running';
  const label = (key: string) => t(short ? shortLabelKey(key) : key);

  switch (action.kind) {
    case 'link':
      return (
        <Link className={buttonClass} to={action.to} aria-label={`${t(action.labelKey)}: ${title}`}>
          {label(action.labelKey)}
        </Link>
      );
    case 'external':
      return (
        <a
          className={buttonClass}
          href={action.href}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`${t(action.labelKey)}: ${title} ${t('dashboard.needs.opens_new_tab')}`}
        >
          {label(action.labelKey)}
          <IconExternalLink size={12} />
        </a>
      );
    case 'enable': {
      const authIndex = action.authIndex;
      return (
        <Button
          variant={primary ? 'primary' : 'secondary'}
          size="sm"
          className={styles.fix}
          loading={running}
          disabled={running}
          aria-label={`${t(action.labelKey)}: ${title}`}
          onClick={() =>
            onRun(async () => {
              await enableAccount(authIndex);
              return t('dashboard.needs.enable_done');
            })
          }
        >
          {label(action.labelKey)}
        </Button>
      );
    }
    case 'backup':
      return (
        <Button
          variant={primary ? 'primary' : 'secondary'}
          size="sm"
          className={styles.fix}
          loading={running}
          disabled={running}
          aria-label={`${t(action.labelKey)}: ${title}`}
          onClick={() =>
            onRun(async () => {
              const backup = await backupNow();
              return t('dashboard.needs.backup_done', { size: formatFileSize(backup.bytes) });
            })
          }
        >
          {label(action.labelKey)}
        </Button>
      );
    case 'note':
    case 'none':
      return null;
  }
}
