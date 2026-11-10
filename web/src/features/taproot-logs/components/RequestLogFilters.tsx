import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { MaskedValueChip } from '@/components/common/MaskedValueChip';
import { useHidesValue } from '@/hooks/useEmailMask';
import { Button } from '@/components/ui/Button';
import { Select } from '@/components/ui/Select';
import { IconSearch, IconX } from '@/components/ui/icons';
import {
  RANGE_PRESETS,
  STATUS_FILTERS,
  activeFilterCount,
  DEFAULT_QUERY,
  type RequestLogQuery,
} from '../model/query';
import styles from './RequestLogFilters.module.scss';

const DEBOUNCE_MS = 300;

/** A text field that reports after typing pauses, and at once on Enter or blur. */
function DebouncedInput({
  value,
  onCommit,
  label,
  placeholder,
  list,
  icon,
}: {
  value: string;
  onCommit: (value: string) => void;
  label: string;
  placeholder: string;
  list?: string;
  icon?: boolean;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  useEffect(() => {
    if (draft === value) return;
    const timer = window.setTimeout(() => onCommit(draft), DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [draft, value, onCommit]);
  return (
    <div className={styles.field}>
      {icon && <IconSearch size={15} className={styles.fieldIcon} aria-hidden="true" />}
      <input
        className={`input ${styles.input} ${icon ? styles.withIcon : ''}`}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') onCommit(draft);
          if (event.key === 'Escape' && draft) {
            setDraft('');
            onCommit('');
          }
        }}
        onBlur={() => draft !== value && onCommit(draft)}
        aria-label={label}
        placeholder={placeholder}
        list={list}
        spellCheck={false}
        autoComplete="off"
      />
      {draft && (
        <button
          type="button"
          className={styles.clear}
          onClick={() => {
            setDraft('');
            onCommit('');
          }}
          aria-label={t('taproot_logs.filters.clear_field', { field: label })}
        >
          <IconX size={14} />
        </button>
      )}
    </div>
  );
}

export function RequestLogFilters({
  query,
  onChange,
  accounts,
  models,
}: {
  query: RequestLogQuery;
  onChange: (next: RequestLogQuery) => void;
  accounts: { value: string; label: string }[];
  models: string[];
}) {
  const { t } = useTranslation();
  const modelListId = useId();
  const hideSearch = useHidesValue(query.q);
  const hideSession = useHidesValue(query.sessionId);
  const set = <K extends keyof RequestLogQuery>(key: K, value: RequestLogQuery[K]) =>
    onChange({ ...query, [key]: value });
  const active = activeFilterCount(query);
  const accountOptions = [
    { value: '', label: t('taproot_logs.filters.all_accounts') },
    ...accounts,
  ];
  if (query.authIndex && !accounts.some((option) => option.value === query.authIndex)) {
    accountOptions.push({ value: query.authIndex, label: query.authIndex });
  }

  return (
    <div className={styles.filters} role="search" aria-label={t('taproot_logs.filters.label')}>
      <div className={styles.search}>
        {hideSearch ? (
          <MaskedValueChip
            value={query.q}
            label={t('taproot_logs.filters.search')}
            onClear={() => set('q', '')}
          />
        ) : (
          <DebouncedInput
            value={query.q}
            onCommit={(value) => set('q', value)}
            label={t('taproot_logs.filters.search')}
            placeholder={t('taproot_logs.filters.search_placeholder')}
            icon
          />
        )}
      </div>
      <Select
        className={styles.select}
        fullWidth={false}
        size="sm"
        value={query.range}
        onChange={(value) => set('range', value as RequestLogQuery['range'])}
        ariaLabel={t('taproot_logs.filters.range')}
        options={RANGE_PRESETS.map((range) => ({
          value: range,
          label: t(`taproot_logs.filters.range_${range}`),
        }))}
      />
      <Select
        className={styles.select}
        fullWidth={false}
        size="sm"
        value={query.status}
        onChange={(value) => set('status', value)}
        ariaLabel={t('taproot_logs.filters.status')}
        options={[
          ...STATUS_FILTERS.map((status) => ({
            value: status,
            label: t(`taproot_logs.filters.status_${status || 'all'}`),
          })),
          ...(query.status && !(STATUS_FILTERS as readonly string[]).includes(query.status)
            ? [{ value: query.status, label: query.status }]
            : []),
        ]}
      />
      <Select
        className={styles.select}
        fullWidth={false}
        size="sm"
        value={query.authIndex}
        onChange={(value) => set('authIndex', value)}
        ariaLabel={t('taproot_logs.filters.account')}
        options={accountOptions}
      />
      <div className={styles.small}>
        <DebouncedInput
          value={query.model}
          onCommit={(value) => set('model', value)}
          label={t('taproot_logs.filters.model')}
          placeholder={t('taproot_logs.filters.model')}
          list={modelListId}
        />
        <datalist id={modelListId}>
          {models.map((model) => (
            <option key={model} value={model} />
          ))}
        </datalist>
      </div>
      <div className={styles.small}>
        {hideSession ? (
          <MaskedValueChip
            value={query.sessionId}
            label={t('taproot_logs.filters.session')}
            onClear={() => set('sessionId', '')}
          />
        ) : (
          <DebouncedInput
            value={query.sessionId}
            onCommit={(value) => set('sessionId', value)}
            label={t('taproot_logs.filters.session')}
            placeholder={t('taproot_logs.filters.session')}
          />
        )}
      </div>
      {active > 0 && (
        <Button variant="ghost" size="sm" onClick={() => onChange(DEFAULT_QUERY)}>
          {t('taproot_logs.filters.reset', { count: active })}
        </Button>
      )}
    </div>
  );
}
