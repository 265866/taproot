import { useId, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Select } from '@/components/ui/Select';
import { IconSearch } from '@/components/ui/icons';
import {
  getAuthFileIcon,
  getThemeSurfaceIconBackground,
  getTypeLabel,
  isThemeSurfaceIconProvider,
} from '@/features/authFiles/constants';
import { useNow } from '@/hooks/useNow';
import type { AuthFileItem, ResolvedTheme } from '@/types';
import type { QuotaFileEntry } from '../live/logic';
import type { ProviderGroup } from '../model/fromApi';
import {
  LEDGER_SORTS,
  buildLedger,
  credentialRetryAt,
  matchesSearch,
  sortLedgerRows,
  type CredentialFacts,
  type LedgerColumn,
  type LedgerRow as LedgerRowModel,
  type LedgerSort,
} from '../model/ledger';
import { LiveQuotaGrid } from './LiveQuota';
import { LedgerRow } from './LedgerRow';
import { windowTitle } from './ledgerText';
import styles from './Ledger.module.scss';

type LedgerView = 'ledger' | 'cards';
type Grouping = 'provider' | 'none';

interface LedgerProps {
  groups: ProviderGroup[];
  /** Credential files by the account key the rows use. */
  fileByIndex: ReadonlyMap<string, AuthFileItem>;
  showEmails: boolean;
  resolvedTheme: ResolvedTheme;
  liveByAuthIndex: Map<string, QuotaFileEntry>;
  /** The search text; the owner keeps it so it can act on the rows shown. */
  query: string;
  onQueryChange: (query: string) => void;
  /** Hides rows besides the search, such as a status filter. */
  rowFilter?: (row: LedgerRowModel, file: AuthFileItem | undefined) => boolean;
  /** Controls placed after the view, grouping, and sort selects. */
  toolbarExtra?: ReactNode;
  renderLeading?: (row: LedgerRowModel, file: AuthFileItem | undefined) => ReactNode;
  renderTrailing?: (row: LedgerRowModel, file: AuthFileItem | undefined) => ReactNode;
}

function ProviderIcon({ provider, theme }: { provider: string; theme: ResolvedTheme }) {
  const { t } = useTranslation();
  const icon = getAuthFileIcon(provider, theme);
  return (
    <span
      className={styles.icon}
      style={
        isThemeSurfaceIconProvider(provider)
          ? { background: getThemeSurfaceIconBackground(theme) }
          : undefined
      }
      aria-hidden="true"
    >
      {icon ? <img src={icon} alt="" /> : getTypeLabel(t, provider).slice(0, 1).toUpperCase()}
    </span>
  );
}

/** The account ledger under the quota charts, after the reference account list. */
export function Ledger({
  groups,
  fileByIndex,
  showEmails,
  resolvedTheme,
  liveByAuthIndex,
  query,
  onQueryChange,
  rowFilter,
  toolbarExtra,
  renderLeading,
  renderTrailing,
}: LedgerProps) {
  const { t } = useTranslation();
  const now = useNow();
  const searchId = useId();
  const [sort, setSort] = useState<LedgerSort>('default');
  const [grouping, setGrouping] = useState<Grouping>('provider');
  const [view, setView] = useState<LedgerView>('ledger');
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set());

  const facts = useMemo(() => {
    const out = new Map<string, CredentialFacts>();
    for (const [index, file] of fileByIndex) {
      out.set(index, {
        fileName: file.name,
        nextRetryAfter: credentialRetryAt(file),
        ...(Number.isSafeInteger(file.priority) ? { priority: file.priority } : {}),
      });
    }
    return out;
  }, [fileByIndex]);
  const ledger = useMemo(() => buildLedger(groups, facts, now), [groups, facts, now]);

  const filtered = ledger.map((group) => ({
    ...group,
    shown: sortLedgerRows(
      group.rows.filter(
        (row) =>
          matchesSearch(row, query) &&
          (!rowFilter || rowFilter(row, fileByIndex.get(row.authIndex)))
      ),
      sort
    ),
  }));
  const anyShown = filtered.some((group) => group.shown.length > 0);

  const toggle = (provider: string) =>
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(provider)) next.delete(provider);
      else next.add(provider);
      return next;
    });

  const renderRow = (row: LedgerRowModel, columns: LedgerColumn[]) => {
    const file = fileByIndex.get(row.authIndex);
    return (
      <LedgerRow
        key={row.authIndex}
        row={row}
        columns={columns}
        now={now}
        showEmails={showEmails}
        icon={<ProviderIcon provider={row.provider} theme={resolvedTheme} />}
        leading={renderLeading?.(row, file)}
        trailing={renderTrailing?.(row, file)}
      />
    );
  };

  const flatRows = () => {
    const columnsByProvider = new Map(filtered.map((g) => [g.provider, g.columns]));
    const span = Math.max(1, ...filtered.map((g) => g.rows.length));
    const rows = filtered.flatMap((group, groupIndex) =>
      group.shown.map((row) => ({ ...row, order: groupIndex * span + row.order }))
    );
    return sortLedgerRows(rows, sort).map((row) =>
      renderRow(row, columnsByProvider.get(row.provider) ?? [])
    );
  };

  const visibleEntries = filtered
    .flatMap((group) => (hidden.has(group.provider) && grouping === 'provider' ? [] : group.shown))
    .map((row) => liveByAuthIndex.get(row.authIndex))
    .filter((entry): entry is QuotaFileEntry => entry !== undefined);

  return (
    <section className={styles.ledger} aria-label={t('taproot_quota.ledger.title')}>
      <div className={styles.toolbar}>
        <label className={styles.search} htmlFor={searchId}>
          <IconSearch size={16} aria-hidden="true" />
          <span className={styles.srOnly}>{t('taproot_quota.ledger.search_label')}</span>
          <input
            id={searchId}
            type="search"
            value={query}
            placeholder={t('taproot_quota.ledger.search')}
            onChange={(event) => onQueryChange(event.target.value)}
          />
        </label>
        <div className={styles.selects}>
          <Select
            value={view}
            onChange={(value) => setView(value as LedgerView)}
            options={[
              { value: 'ledger', label: t('taproot_quota.ledger.view_ledger') },
              { value: 'cards', label: t('taproot_quota.ledger.view_cards') },
            ]}
            ariaLabel={t('taproot_quota.ledger.view_label')}
            size="sm"
            fullWidth={false}
          />
          <Select
            value={grouping}
            onChange={(value) => setGrouping(value as Grouping)}
            options={[
              { value: 'provider', label: t('taproot_quota.ledger.group_provider') },
              { value: 'none', label: t('taproot_quota.ledger.group_none') },
            ]}
            ariaLabel={t('taproot_quota.ledger.group_label')}
            size="sm"
            fullWidth={false}
          />
          <Select
            value={sort}
            onChange={(value) => setSort(value as LedgerSort)}
            options={LEDGER_SORTS.map((value) => ({
              value,
              label: t(`taproot_quota.ledger.sort_${value}`),
            }))}
            ariaLabel={t('taproot_quota.ledger.sort_label')}
            size="sm"
            fullWidth={false}
          />
          {toolbarExtra}
        </div>
      </div>

      {!anyShown ? (
        <p className={styles.empty}>
          {query.trim()
            ? t('taproot_quota.ledger.no_match', { query: query.trim() })
            : t('taproot_quota.ledger.no_accounts')}
        </p>
      ) : view === 'cards' ? (
        <LiveQuotaGrid entries={visibleEntries} />
      ) : grouping === 'none' ? (
        <ul className={styles.rows}>{flatRows()}</ul>
      ) : (
        filtered.map((group) => {
          if (group.shown.length === 0) return null;
          const open = !hidden.has(group.provider);
          const label = getTypeLabel(t, group.provider);
          const listId = `${searchId}-${group.provider}`;
          const showToggle = (
            <button
              type="button"
              className={styles.showToggle}
              aria-expanded={open}
              aria-controls={listId}
              aria-label={t(
                open ? 'taproot_quota.ledger.hide_rows' : 'taproot_quota.ledger.show_rows',
                { provider: label }
              )}
              onClick={() => toggle(group.provider)}
            >
              {t(open ? 'taproot_quota.ledger.hide' : 'taproot_quota.ledger.show')}
            </button>
          );
          return (
            <div key={group.provider} className={styles.group}>
              <div className={styles.summary}>
                <div className={styles.summaryHead}>
                  <ProviderIcon provider={group.provider} theme={resolvedTheme} />
                  <h3 className={styles.summaryTitle}>{label}</h3>
                  <span className={styles.summaryCount}>
                    {group.shown.length === group.rows.length
                      ? t('taproot_quota.ledger.credentials', { count: group.rows.length })
                      : t('taproot_quota.ledger.credentials_filtered', {
                          shown: group.shown.length,
                          count: group.rows.length,
                        })}
                  </span>
                  {group.subPools.length === 0 && showToggle}
                </div>
                {group.subPools.length > 0 && (
                  <div className={styles.summaryFoot}>
                    <dl className={styles.subPools}>
                      {group.subPools.map((pool) => (
                        <div key={pool.window}>
                          <dt>{windowTitle(t, pool)}</dt>
                          <dd>
                            <strong>{pool.remainingPercent}%</strong>{' '}
                            <span>
                              {t('taproot_quota.ledger.sub_pool_of', {
                                capacity: `${pool.capacityPercent}%`,
                              })}
                            </span>
                          </dd>
                        </div>
                      ))}
                    </dl>
                    {showToggle}
                  </div>
                )}
              </div>
              <ul id={listId} className={styles.rows} hidden={!open}>
                {open && group.shown.map((row) => renderRow(row, group.columns))}
              </ul>
            </div>
          );
        })
      )}
    </section>
  );
}
