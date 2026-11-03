import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { BlockerFunction } from 'react-router';
import { IconPlus, IconTrash2 } from '@/components/ui/icons';
import { Button } from '@/components/ui/Button';
import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';
import { useNotificationStore } from '@/stores';
import { taprootApi, type Price } from '@/services/api/taproot';
import { useTaprootQuery } from '../hooks/useTaprootQuery';
import {
  PRICE_FIELDS,
  draftsChanged,
  draftsToPrices,
  matchPrice,
  toDrafts,
  validateDrafts,
  type PriceDraft,
  type PriceField,
  type PriceFieldError,
} from '../logic/prices';
import { DASH, formatDateTime } from '../logic/format';
import styles from '../TaprootUsage.module.scss';

const FIELD_LABELS: Record<PriceField, string> = {
  input: 'taproot_usage.pricing.input',
  output: 'taproot_usage.pricing.output',
  cacheRead: 'taproot_usage.pricing.cache_read',
  cacheWrite5m: 'taproot_usage.pricing.cache_write_5m',
  cacheWrite1h: 'taproot_usage.pricing.cache_write_1h',
};

const ERROR_LABELS: Record<PriceFieldError, string> = {
  required: 'taproot_usage.pricing.error_required',
  duplicate: 'taproot_usage.pricing.error_duplicate',
  number: 'taproot_usage.pricing.error_number',
};

interface Props {
  /** Models seen in the selected range, for the coverage list. */
  models: readonly string[];
  onSaved: () => void;
}

export function PricingEditor({ models, onSaved }: Props) {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((s) => s.showNotification);
  const query = useTaprootQuery('prices', () => taprootApi.prices());
  const [saved, setSaved] = useState<Price[] | null>(null);
  const [drafts, setDrafts] = useState<PriceDraft[]>([]);
  const [saving, setSaving] = useState(false);
  const [showErrors, setShowErrors] = useState(false);
  const newRowCounter = useRef(0);

  useEffect(() => {
    if (!query.data) return;
    setSaved(query.data.prices);
    setDrafts(toDrafts(query.data.prices));
    setShowErrors(false);
  }, [query.data]);

  const errors = useMemo(() => validateDrafts(drafts), [drafts]);
  const dirty = saved !== null && draftsChanged(drafts, saved);

  const shouldBlock = useCallback<BlockerFunction>(
    ({ currentLocation, nextLocation }) => {
      if (!dirty) return false;
      if (currentLocation.pathname !== nextLocation.pathname) return true;
      return new URLSearchParams(nextLocation.search).get('tab') !== 'pricing';
    },
    [dirty]
  );
  const dialog = useMemo(
    () => ({
      title: t('taproot_usage.pricing.unsaved_title'),
      message: t('taproot_usage.pricing.unsaved_message'),
      confirmText: t('taproot_usage.pricing.unsaved_confirm'),
      cancelText: t('taproot_usage.pricing.unsaved_cancel'),
    }),
    [t]
  );
  useUnsavedChangesGuard({ shouldBlock, dialog });

  const update = (rowId: string, patch: (draft: PriceDraft) => PriceDraft) =>
    setDrafts((list) => list.map((d) => (d.rowId === rowId ? patch(d) : d)));

  const addRow = () => {
    newRowCounter.current += 1;
    setDrafts((list) => [
      ...list,
      {
        rowId: `new-${newRowCounter.current}`,
        modelPattern: '',
        values: { input: '', output: '', cacheRead: '', cacheWrite5m: '0', cacheWrite1h: '0' },
        source: '',
        updatedAt: '',
      },
    ]);
  };

  const save = async () => {
    if (errors.size > 0) {
      setShowErrors(true);
      showNotification(t('taproot_usage.pricing.invalid'), 'error');
      return;
    }
    setSaving(true);
    try {
      const result = await taprootApi.putPrices(draftsToPrices(drafts));
      setSaved(result.prices);
      setDrafts(toDrafts(result.prices));
      setShowErrors(false);
      showNotification(t('taproot_usage.pricing.saved'), 'success');
      onSaved();
    } catch (err) {
      showNotification(
        t('taproot_usage.pricing.save_failed', {
          message: err instanceof Error ? err.message : String(err),
        }),
        'error'
      );
    } finally {
      setSaving(false);
    }
  };

  const coverage = useMemo(() => {
    const patterns = drafts.map((d) => ({ modelPattern: d.modelPattern.trim() }));
    return [...models].sort().map((model) => ({ model, match: matchPrice(model, patterns) }));
  }, [drafts, models]);

  if (query.error && !saved) {
    return (
      <div className={styles.error} role="alert">
        <span>{t('taproot_usage.load_failed', { message: query.error.message })}</span>
        <button type="button" className={styles.ghostButton} onClick={query.reload}>
          {t('taproot_usage.retry')}
        </button>
      </div>
    );
  }

  return (
    <>
      <section className={styles.panel}>
        <div className={styles.panelHead}>
          <div>
            <h2 className={styles.panelTitle}>{t('taproot_usage.pricing.title')}</h2>
            <p className={styles.panelDesc}>{t('taproot_usage.pricing.desc')}</p>
          </div>
        </div>
        <p className={styles.callout}>{t('taproot_usage.pricing.reprice_note')}</p>
        <p className={styles.panelDesc} style={{ marginBottom: 12 }}>
          {t('taproot_usage.pricing.openai_note')}
        </p>
        {query.loading && !saved ? <div className={styles.loadingBar} /> : null}
        {saved && drafts.length === 0 ? (
          <div className={styles.empty}>{t('taproot_usage.pricing.empty')}</div>
        ) : null}
        {drafts.length > 0 ? (
          <div className={styles.tableScroll}>
            <table className={`${styles.table} ${styles.priceTable}`}>
              <thead>
                <tr>
                  <th>{t('taproot_usage.pricing.pattern')}</th>
                  {PRICE_FIELDS.map((field) => (
                    <th key={field} className={styles.num}>
                      {t(FIELD_LABELS[field])}
                    </th>
                  ))}
                  <th>{t('taproot_usage.pricing.source')}</th>
                  <th>{t('taproot_usage.pricing.updated')}</th>
                  <th>
                    <span className={styles.srOnly}>{t('common.delete')}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {drafts.map((d) => {
                  const rowErrors = showErrors ? errors.get(d.rowId) : undefined;
                  const patternLabel = d.modelPattern.trim() || t('taproot_usage.pricing.new_row');
                  return (
                    <tr key={d.rowId}>
                      <td>
                        <input
                          className={`${styles.textInput} ${styles.mono} ${styles.patternInput}`}
                          value={d.modelPattern}
                          aria-label={t('taproot_usage.pricing.pattern')}
                          aria-invalid={Boolean(rowErrors?.modelPattern)}
                          onChange={(e) =>
                            update(d.rowId, (row) => ({ ...row, modelPattern: e.target.value }))
                          }
                          spellCheck={false}
                        />
                        {rowErrors?.modelPattern ? (
                          <span className={styles.cellError}>
                            {t(ERROR_LABELS[rowErrors.modelPattern])}
                          </span>
                        ) : null}
                      </td>
                      {PRICE_FIELDS.map((field) => {
                        const error = rowErrors?.values[field];
                        return (
                          <td key={field}>
                            <input
                              className={`${styles.numberInput} ${styles.num}`}
                              inputMode="decimal"
                              value={d.values[field]}
                              aria-label={`${patternLabel} ${t(FIELD_LABELS[field])}`}
                              aria-invalid={Boolean(error)}
                              onChange={(e) =>
                                update(d.rowId, (row) => ({
                                  ...row,
                                  values: { ...row.values, [field]: e.target.value },
                                }))
                              }
                            />
                            {error ? (
                              <span className={styles.cellError}>{t(ERROR_LABELS[error])}</span>
                            ) : null}
                          </td>
                        );
                      })}
                      <td className={styles.sourceCell}>{d.source || DASH}</td>
                      <td className={styles.muted}>
                        {d.updatedAt ? formatDateTime(d.updatedAt) : DASH}
                      </td>
                      <td>
                        <button
                          type="button"
                          className={styles.iconButton}
                          aria-label={t('taproot_usage.pricing.remove', { pattern: patternLabel })}
                          onClick={() =>
                            setDrafts((list) => list.filter((row) => row.rowId !== d.rowId))
                          }
                        >
                          <IconTrash2 size={16} />
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
        <div className={styles.actionBar}>
          <button type="button" className={styles.ghostButton} onClick={addRow} disabled={!saved}>
            <IconPlus size={16} />
            {t('taproot_usage.pricing.add')}
          </button>
          <Button onClick={save} disabled={!dirty || saving} loading={saving}>
            {saving ? t('taproot_usage.pricing.saving') : t('taproot_usage.pricing.save')}
          </Button>
          <button
            type="button"
            className={styles.ghostButton}
            disabled={!dirty || saving}
            onClick={() => {
              if (!saved) return;
              setDrafts(toDrafts(saved));
              setShowErrors(false);
            }}
          >
            {t('taproot_usage.pricing.reset')}
          </button>
        </div>
      </section>

      {coverage.length > 0 ? (
        <section className={styles.panel}>
          <div className={styles.panelHead}>
            <div>
              <h2 className={styles.panelTitle}>{t('taproot_usage.pricing.coverage_title')}</h2>
              <p className={styles.panelDesc}>{t('taproot_usage.pricing.coverage_desc')}</p>
            </div>
          </div>
          <div className={styles.tableScroll}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>{t('taproot_usage.pricing.coverage_model')}</th>
                  <th>{t('taproot_usage.pricing.coverage_price')}</th>
                </tr>
              </thead>
              <tbody>
                {coverage.map(({ model, match }) => (
                  <tr key={model}>
                    <td className={styles.mono}>{model}</td>
                    <td>
                      {match ? (
                        <span className={styles.mono}>{match.modelPattern}</span>
                      ) : (
                        <span className={`${styles.badge} ${styles.badgeWarn}`}>
                          {t('taproot_usage.pricing.coverage_none')}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
    </>
  );
}
