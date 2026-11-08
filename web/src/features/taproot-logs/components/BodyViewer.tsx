import { useContext, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconCopy } from '@/components/ui/icons';
import { useNotificationStore } from '@/stores';
import { useEmailMask } from '@/hooks/useEmailMask';
import { copyToClipboard } from '@/utils/clipboard';
import { expandedLength, hasBlobRef } from '../model/blobs';
import { BlobsContext } from './blobsContext';
import {
  COLLAPSE_BYTES,
  SSE_PAGE,
  TEXT_CHUNK,
  parseJsonBody,
  parseSse,
  sniffBody,
  textWindow,
  type ParsedJson,
} from '../model/body';
import { formatBytes } from '../model/format';
import { JsonTree } from './JsonTree';
import styles from './BodyViewer.module.scss';

/** Above this, "show all" is not offered; the raw download is the way to read it whole. */
const SHOW_ALL_LIMIT = 4 * 1024 * 1024;

type Mode = 'formatted' | 'raw';

/**
 * A request or response body. Large bodies start collapsed, JSON is parsed only after the body is
 * opened (after a paint, so the click responds first), and raw text renders in chunks.
 *
 * `text` may hold blob placeholders; `full` then loads the original, which copy uses.
 */
export function BodyViewer({
  text: raw,
  size,
  full,
}: {
  text: string;
  size?: number;
  full?: () => Promise<string>;
}) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);
  const blobs = useContext(BlobsContext);
  // Views mask what they draw: raw text as text, JSON after parsing. Copy keeps the real body.
  const kind = useMemo(() => sniffBody(raw), [raw]);
  const bytes = useMemo(() => size ?? expandedLength(raw, blobs), [size, raw, blobs]);
  const [open, setOpen] = useState(raw.length <= COLLAPSE_BYTES);
  const [mode, setMode] = useState<Mode>(kind === 'json' || kind === 'sse' ? 'formatted' : 'raw');

  if (kind === 'empty') return <div className={styles.empty}>{t('taproot_logs.body.empty')}</div>;

  const copy = async () => {
    const ok = full && hasBlobRef(raw) ? await copyPending(full()) : await copyToClipboard(raw);
    showNotification(
      t(ok ? 'taproot_logs.body.copied' : 'taproot_logs.body.copy_failed'),
      ok ? 'success' : 'error'
    );
  };

  if (!open) {
    return (
      <div className={styles.collapsed}>
        <span>{t('taproot_logs.body.large', { size: formatBytes(bytes) })}</span>
        <Button size="sm" variant="secondary" onClick={() => setOpen(true)}>
          {t('taproot_logs.body.open')}
        </Button>
      </div>
    );
  }

  return (
    <div className={styles.viewer}>
      <div className={styles.toolbar}>
        {(kind === 'json' || kind === 'sse') && (
          <div className={styles.modes} role="group" aria-label={t('taproot_logs.body.view_mode')}>
            {(['formatted', 'raw'] as const).map((value) => (
              <button
                key={value}
                type="button"
                className={`${styles.mode} ${mode === value ? styles.modeActive : ''}`}
                aria-pressed={mode === value}
                onClick={() => setMode(value)}
              >
                {t(`taproot_logs.body.mode_${value}`)}
              </button>
            ))}
          </div>
        )}
        <span className={styles.size}>{formatBytes(bytes)}</span>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => void copy()}
          aria-label={t('taproot_logs.body.copy')}
        >
          <IconCopy size={14} />
        </Button>
      </div>
      {mode === 'raw' ? (
        <RawText text={raw} />
      ) : kind === 'json' ? (
        <FormattedJson text={raw} />
      ) : (
        <EventStream text={raw} />
      )}
    </div>
  );
}

/**
 * Copies text that is still loading. Safari and Firefox let a click write to the clipboard only
 * briefly, so a write after the fetch would fail; a ClipboardItem holding the pending text is
 * written within the click.
 */
async function copyPending(text: Promise<string>): Promise<boolean> {
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    try {
      const blob = text.then((value) => new Blob([value], { type: 'text/plain' }));
      await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]);
      return true;
    } catch {
      // Fall back to writing the text once it has loaded.
    }
  }
  try {
    return await copyToClipboard(await text);
  } catch {
    return false;
  }
}

function RawText({ text: raw }: { text: string }) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const text = useMemo(() => mask(raw), [mask, raw]);
  const [shown, setShown] = useState(TEXT_CHUNK);
  const { visible, remaining } = textWindow(text, shown);
  return (
    <>
      <pre className={styles.raw} tabIndex={0}>
        {visible}
      </pre>
      {remaining > 0 && (
        <div className={styles.more}>
          <span>{t('taproot_logs.body.truncated', { size: formatBytes(remaining) })}</span>
          <Button size="sm" variant="secondary" onClick={() => setShown((n) => n + TEXT_CHUNK * 4)}>
            {t('taproot_logs.body.show_more')}
          </Button>
          {text.length <= SHOW_ALL_LIMIT && (
            <Button size="sm" variant="ghost" onClick={() => setShown(text.length)}>
              {t('taproot_logs.body.show_all')}
            </Button>
          )}
        </div>
      )}
    </>
  );
}

/** Parses after the first paint so opening a 30 MB body shows "Formatting…" instead of a frozen tab. */
function useDeferredJson(text: string): ParsedJson | null {
  const [parsed, setParsed] = useState<{ text: string; result: ParsedJson } | null>(null);
  useEffect(() => {
    let cancelled = false;
    const timer = window.setTimeout(() => {
      const result = parseJsonBody(text);
      if (!cancelled) setParsed({ text, result });
    }, 16);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [text]);
  return parsed && parsed.text === text ? parsed.result : null;
}

function FormattedJson({ text }: { text: string }) {
  const { t } = useTranslation();
  const parsed = useDeferredJson(text);
  if (!parsed) return <div className={styles.pending}>{t('taproot_logs.body.formatting')}</div>;
  if (!parsed.ok) {
    return (
      <>
        <div className={styles.notice}>
          {t(
            parsed.reason === 'too_large'
              ? 'taproot_logs.body.too_large_to_format'
              : 'taproot_logs.body.not_json'
          )}
        </div>
        <RawText text={text} />
      </>
    );
  }
  return <JsonTree value={parsed.value} />;
}

function EventStream({ text }: { text: string }) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(SSE_PAGE);
  const { events, total } = useMemo(() => parseSse(text, shown), [text, shown]);
  const { mask } = useEmailMask();
  return (
    <ol className={styles.events}>
      {events.map((event, index) => (
        <li key={index} className={styles.event}>
          <span className={styles.eventName}>{mask(event.event)}</span>
          <EventData data={event.data} />
        </li>
      ))}
      {total > events.length && (
        <li className={styles.more}>
          <Button size="sm" variant="secondary" onClick={() => setShown((n) => n + SSE_PAGE)}>
            {t('taproot_logs.body.show_more_events', { count: total - events.length })}
          </Button>
        </li>
      )}
    </ol>
  );
}

/** One event: compact text first, a tree on request, so a long stream stays one line per event. */
function EventData({ data }: { data: string }) {
  const { t } = useTranslation();
  const [formatted, setFormatted] = useState(false);
  const parsed = useMemo(
    () => (formatted && sniffBody(data) === 'json' ? parseJsonBody(data) : null),
    [data, formatted]
  );
  const { mask } = useEmailMask();
  const { visible, remaining } = textWindow(mask(data), 600);
  return (
    <div className={styles.eventBody}>
      {parsed?.ok ? (
        <JsonTree value={parsed.value} />
      ) : (
        <pre className={styles.eventText}>
          {visible}
          {remaining > 0 && '…'}
        </pre>
      )}
      {sniffBody(data) === 'json' && (
        <button
          type="button"
          className={styles.eventToggle}
          aria-pressed={formatted}
          onClick={() => setFormatted((value) => !value)}
        >
          {t(formatted ? 'taproot_logs.body.mode_raw' : 'taproot_logs.body.mode_formatted')}
        </button>
      )}
    </div>
  );
}
