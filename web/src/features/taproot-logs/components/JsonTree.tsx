import { memo, useContext, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconChevronDown } from '@/components/ui/icons';
import { splitBlobRefs } from '../model/blobs';
import { BlobRef } from './BlobRef';
import { BlobsContext } from './blobsContext';
import {
  CHILD_PAGE,
  STRING_PREVIEW,
  STRING_STEP,
  childCount,
  childPage,
  containerSummary,
  isContainer,
  jsonKind,
  openByDefault,
} from '../model/jsonTree';
import { useEmailMask } from '@/hooks/useEmailMask';
import { textWindow } from '../model/body';
import styles from './JsonTree.module.scss';

/** A collapsible JSON viewer that renders only what is open. */
export function JsonTree({ value }: { value: unknown }) {
  return (
    <div className={styles.tree}>
      <JsonNode name={null} value={value} depth={0} />
    </div>
  );
}

const JsonNode = memo(function JsonNode({
  name,
  value,
  depth,
}: {
  name: string | null;
  value: unknown;
  depth: number;
}) {
  const { t } = useTranslation();
  const size = childCount(value);
  const [open, setOpen] = useState(() => openByDefault(depth, size));
  const [shown, setShown] = useState(CHILD_PAGE);
  const { mask } = useEmailMask();
  const label = name === null ? null : <span className={styles.key}>{mask(name)}</span>;

  if (!isContainer(value)) {
    return (
      <div className={styles.row}>
        <span className={styles.spacer} aria-hidden="true" />
        {label}
        {label && <span className={styles.colon}>: </span>}
        <JsonScalar value={value} />
      </div>
    );
  }

  const isArray = Array.isArray(value);
  const [openMark, closeMark] = isArray ? ['[', ']'] : ['{', '}'];
  const remaining = size - Math.min(shown, size);
  return (
    <div>
      <button
        type="button"
        className={`${styles.row} ${styles.toggle}`}
        onClick={() => setOpen((prev) => !prev)}
        aria-expanded={open}
      >
        <IconChevronDown
          size={12}
          className={`${styles.chevron} ${open ? '' : styles.chevronClosed}`}
          aria-hidden="true"
        />
        {label}
        {label && <span className={styles.colon}>: </span>}
        <span className={styles.mark}>{openMark}</span>
        {!open && (
          <>
            <span className={styles.summary}>
              <SummaryText value={value} />
            </span>
            <span className={styles.mark}>{closeMark}</span>
          </>
        )}
        {open && size === 0 && <span className={styles.mark}>{closeMark}</span>}
      </button>
      {open && size > 0 && (
        <div className={styles.children}>
          {childPage(value, shown).map(([key, child]) => (
            <JsonNode key={key} name={key} value={child} depth={depth + 1} />
          ))}
          {remaining > 0 && (
            <button
              type="button"
              className={styles.more}
              onClick={() => setShown((count) => count + CHILD_PAGE)}
            >
              {t('taproot_logs.json.show_more_children', {
                count: Math.min(CHILD_PAGE, remaining),
                remaining,
              })}
            </button>
          )}
        </div>
      )}
      {open && size > 0 && (
        <div className={`${styles.row} ${styles.mark} ${styles.close}`}>{closeMark}</div>
      )}
    </div>
  );
});

function JsonScalar({ value }: { value: unknown }) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const [shown, setShown] = useState(STRING_PREVIEW);
  const blobs = useContext(BlobsContext);
  if (typeof value !== 'string') {
    return <span className={styles[jsonKind(value)]}>{String(value)}</span>;
  }
  const segments = splitBlobRefs(value, blobs);
  if (segments.some((segment) => typeof segment !== 'string')) {
    return (
      <span className={styles.string}>
        &quot;
        {segments.map((segment, index) =>
          typeof segment === 'string' ? (
            mask(segment)
          ) : (
            <BlobRef key={index} hash={segment.hash} blob={segment.blob} />
          )
        )}
        &quot;
      </span>
    );
  }
  const text = mask(value);
  const { visible, remaining } = textWindow(text, shown);
  return (
    <span className={styles.string}>
      &quot;{visible}
      {remaining > 0 ? '…' : '"'}
      {remaining > 0 && (
        <button
          type="button"
          className={styles.inlineMore}
          onClick={() => setShown((count) => count + STRING_STEP)}
        >
          {t('taproot_logs.json.show_more_text', { count: remaining })}
        </button>
      )}
    </span>
  );
}

function SummaryText({ value }: { value: unknown }) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const summary = containerSummary(value);
  if (summary.kind === 'array')
    return <>{t('taproot_logs.json.items', { count: summary.count })}</>;
  const keys = summary.keys.join(', ') + (summary.more > 0 ? ', …' : '');
  return <>{mask(summary.hint ? `${summary.hint} · ${keys}` : keys)}</>;
}
